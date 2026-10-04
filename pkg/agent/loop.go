package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/internalmsg"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type FunctionCallResult struct {
	Success   bool
	Response  string
	ToolCalls []ToolCallResult
}

const (
	maxReasoningLength           = 5000
	maxEmptyRetries              = 3
	maxMalformedCallRetries      = 3
	maxLengthContinuations       = 8
	defaultMaxTurnIterations     = 50
	duplicateToolCallNotice      = "[DUPLICATE] This tool call was already executed earlier in this turn and its result is already in your context. Re-running it produces no new information. Take a different action or provide the final answer."
	formatCorrectionMessage      = "FORMAT ERROR: You tried to use XML tool call tags (<tool_call>, <function=...>) but the format was malformed or incomplete. Use native function calling provided by the API. Re-send your message with the correct format or answer in plain text."
	emptyResponseReminder        = "[SYSTEM] Your previous response was empty. Please generate a text response based on the tool results above."
	lengthTruncationContinuation = "Your previous response was truncated by the output token limit. Continue exactly where you stopped."
	truncatedToolCallCorrection  = "[SYSTEM] Your previous tool call was cut off before its arguments were complete, so the JSON was invalid and could not run. Re-send the tool call with complete, valid JSON. Keep large arguments (a prompt, file contents) concise enough that the entire tool call fits within the output limit."

	subagentHoldWait     = 30 * time.Second
	subagentHoldMaxTotal = 60 * time.Minute
)

type turnContext struct {
	session             *sess.Session
	executed            map[string]bool
	iterations          int
	limit               int
	lengthContinuations int
	malformedRetries    int
	finalText           string
}

func (a *agentImpl) newTurnContext(s *sess.Session) *turnContext {
	limit := a.config.MaxToolCallDepth
	if limit <= 0 {
		limit = defaultMaxTurnIterations
	}
	return &turnContext{
		session:  s,
		executed: make(map[string]bool),
		limit:    limit,
	}
}

func (a *agentImpl) runTurn(ctx context.Context, s *sess.Session) (string, error) {
	tc := a.newTurnContext(s)

	pending := a.drainSteering(s)

	for {
		text, done, err := a.runInnerLoop(ctx, tc, pending)
		if err != nil {
			return "", err
		}
		if done {
			return text, nil
		}

		pending = a.drainSteering(s)
		if len(pending) == 0 {
			pending = a.awaitSubagentResult(ctx, s)
		}
		if len(pending) == 0 {
			return tc.finalText, nil
		}
	}
}

func (a *agentImpl) awaitSubagentResult(ctx context.Context, s *sess.Session) []string {
	w := a.config.SubagentWatch
	if w == nil {
		return nil
	}
	owner := a.subagentOwner(s)
	if w.PendingSubagents(owner) == 0 {
		return nil
	}
	deadline := time.Now().Add(subagentHoldMaxTotal)
	for time.Now().Before(deadline) && w.PendingSubagents(owner) > 0 {
		if ctx.Err() != nil {
			break
		}
		if w.WaitForSubagent(ctx, owner, subagentHoldWait) {
			if pending := a.drainSteering(s); len(pending) > 0 {
				return pending
			}
		}
	}
	return a.drainSteering(s)
}

func (a *agentImpl) subagentOwner(s *sess.Session) string {
	if a.config.BGOwner != "" {
		return a.config.BGOwner
	}
	return "main:" + strconv.FormatInt(s.GetPeerID(), 10)
}

func (a *agentImpl) runInnerLoop(ctx context.Context, tc *turnContext, pending []string) (string, bool, error) {
	hasMore := true

	for hasMore || len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}

		tc.iterations++
		if tc.iterations > tc.limit {
			return a.finishAtIterationLimit(tc), true, nil
		}

		a.appendSteeringMessages(tc.session, pending)
		pending = nil

		round, messages, err := a.streamWithOverflowRecovery(ctx, tc)
		if err != nil {
			return "", false, err
		}

		tc.session.RecordAssistantUsage(round.promptTokens, round.completionTokens)

		if tc.iterations > 1 {
			if repeats := a.checkResponseLoop(tc.session.GetPeerID(), round.text, round.reasoning, round.toolCalls); repeats > 0 {
				a.injectLoopCorrection(tc.session, repeats)
			}
		}
		calls := a.resolveToolCalls(ctx, tc, messages, round)
		if calls.retryRequested {
			hasMore = true
			continue
		}

		if len(calls.toolCalls) == 0 {
			hasMore = false
			if round.finishReason == "length" && tc.lengthContinuations < maxLengthContinuations {
				tc.lengthContinuations++
				a.appendAssistantText(tc.session, round.text)
				tc.session.AddUserMessage(lengthTruncationContinuation)
				hasMore = true
			} else {
				tc.finalText = a.finishWithTextResponse(tc.session, round.text)
			}
		} else {
			a.executeAndAppendBatch(ctx, tc, calls.assistantContent, calls.toolCalls)
			hasMore = true
		}

		pending = a.drainSteering(tc.session)
	}

	return tc.finalText, false, nil
}

func (a *agentImpl) finishAtIterationLimit(tc *turnContext) string {
	limitMessage := fmt.Sprintf("[TOOL] Tool call iteration limit reached (%d rounds in one turn), stopping to avoid an unbounded loop.", tc.limit)
	fmt.Printf("%s%s\n", a.agentPrefix(), limitMessage)
	logger.DebugToFile("%s%s", a.agentPrefix(), limitMessage)
	a.sendThinking(tc.session.GetPeerID(), "[TOOL] Tool call iteration limit reached, finishing turn")
	a.appendAssistantText(tc.session, limitMessage)
	tc.finalText = limitMessage
	return limitMessage
}

type roundResult struct {
	text             string
	reasoning        string
	finishReason     string
	toolCalls        []ToolCall
	promptTokens     int
	completionTokens int
}

type turnCalls struct {
	assistantContent string
	toolCalls        []ToolCall
	retryRequested   bool
}

func (a *agentImpl) streamWithOverflowRecovery(ctx context.Context, tc *turnContext) (roundResult, []Message, error) {
	messages, err := a.prepareContext(ctx, tc.session)
	if err != nil {
		return roundResult{}, nil, err
	}
	messages = a.ensureContextFits(ctx, tc, messages)

	round, err := a.streamRound(ctx, tc, messages)
	if err == nil || !IsContextOverflowError(err) || a.compactor == nil {
		return round, messages, err
	}

	return a.recoverFromOverflow(ctx, tc, messages, err)
}

func (a *agentImpl) ensureContextFits(ctx context.Context, tc *turnContext, messages []Message) []Message {
	if a.config.MaxTokens <= 0 || a.requestFitsContext(messages) {
		return messages
	}
	a.logContextGuard(tc.session, len(messages))

	if a.compactor != nil && a.forceCompact(ctx, tc.session, false) {
		messages = a.prepareContextNoCompact(tc.session)
		if a.requestFitsContext(messages) {
			return messages
		}
	}
	if a.applyAggressivePruning(tc.session) == 0 {
		return messages
	}
	return a.prepareContextNoCompact(tc.session)
}

func (a *agentImpl) recoverFromOverflow(ctx context.Context, tc *turnContext, rejected []Message, cause error) (roundResult, []Message, error) {
	a.recordOverflowPromptTokens(tc.session, cause)
	fmt.Printf("%s[OMP-COMPACT] Reactive overflow recovery for peer %d\n", a.agentPrefix(), tc.session.GetPeerID())
	logger.DebugToFile("%s[OMP-COMPACT] Peer %d: reactive overflow, compacting", a.agentPrefix(), tc.session.GetPeerID())

	if a.forceCompact(ctx, tc.session, false) && shouldAddAutoContinue(tc.session) {
		tc.session.AddUserMessage(tokenizers.CompactionOverflowContinueText)
	}

	messages := a.prepareContextNoCompact(tc.session)
	if !a.requestFitsContext(messages) && a.applyAggressivePruning(tc.session) > 0 {
		messages = a.prepareContextNoCompact(tc.session)
	}
	if !payloadShrank(rejected, messages) {
		logger.DebugToFile("%s[OMP-COMPACT] Peer %d: recovery reduced nothing, keeping original error",
			a.agentPrefix(), tc.session.GetPeerID())
		return roundResult{}, nil, cause
	}

	round, err := a.streamRound(ctx, tc, messages)
	return round, messages, err
}

func payloadShrank(rejected, retry []Message) bool {
	return requestPayloadTokens(retry, nil) < requestPayloadTokens(rejected, nil)
}

func (a *agentImpl) requestFitsContext(messages []Message) bool {
	estimate := requestPayloadTokens(messages, a.currentToolSchemas())
	return compress.FitsContext(estimate, compress.OutputCapForWindow(a.config.MaxTokens), a.config.MaxTokens)
}

func (a *agentImpl) recordOverflowPromptTokens(s *sess.Session, err error) {
	promptTokens, _, isOverflow := ContextOverflowStats(err)
	if !isOverflow || promptTokens <= 0 {
		return
	}
	s.RecordOverflowPromptTokens(promptTokens)
}

func (a *agentImpl) logContextGuard(s *sess.Session, messageCount int) {
	fmt.Printf("%s[OMP-COMPACT] Pre-flight: %d messages do not fit window %d, compacting peer %d\n",
		a.agentPrefix(), messageCount, a.config.MaxTokens, s.GetPeerID())
	logger.DebugToFile("%s[OMP-COMPACT] Pre-flight: peer %d, %d messages, window %d",
		a.agentPrefix(), s.GetPeerID(), messageCount, a.config.MaxTokens)
}

func (a *agentImpl) prepareContext(ctx context.Context, s *sess.Session) ([]Message, error) {
	if a.compactor != nil {
		a.compactIfNeeded(ctx, s, true)
	}
	return a.prepareContextNoCompact(s), nil
}
func (a *agentImpl) prepareContextNoCompact(s *sess.Session) []Message {
	messages := repairToolCallPairing(a.convertHistoryToAPIMessages(s.GetContextMessages()))

	workingDir := s.GetWorkingDir()
	if workingDir == "" {
		workingDir = tools.WorkingDir
	}
	tools.SetWorkingDir(workingDir)

	return a.injectInstructions(messages, workingDir)
}

func repairToolCallPairing(messages []Message) []Message {
	present := make(map[string]bool, len(messages))
	for _, m := range messages {
		if m.Role == "tool" {
			present[m.ToolCallID] = true
		}
	}

	out := make([]Message, 0, len(messages)+4)
	for _, m := range messages {
		out = append(out, m)
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		for _, call := range m.ToolCalls {
			if present[call.ID] {
				continue
			}
			out = append(out, Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    "[RECOVERED] This tool call was interrupted before execution and produced no result.",
			})
		}
	}
	return out
}

func (a *agentImpl) streamRound(ctx context.Context, tc *turnContext, messages []Message) (roundResult, error) {
	round, err := a.streamOnce(ctx, tc, messages)
	if err != nil {
		return round, err
	}

	if isTerminalResponse(round.text, len(round.toolCalls) > 0, round.reasoning != "") {
		return round, nil
	}

	for attempt := 1; attempt <= maxEmptyRetries; attempt++ {
		fmt.Printf("%s[WARN] LLM returned empty response (attempt %d/%d), retrying\n", a.agentPrefix(), attempt, maxEmptyRetries)
		retryMessages := append(append([]Message{}, messages...), Message{Role: "user", Content: emptyResponseReminder})

		round, err = a.streamOnce(ctx, tc, retryMessages)
		if err != nil {
			return round, err
		}
		if isTerminalResponse(round.text, len(round.toolCalls) > 0, round.reasoning != "") {
			return round, nil
		}
	}

	fmt.Printf("%s[WARN] LLM returned empty response after %d retries\n", a.agentPrefix(), maxEmptyRetries)
	return round, nil
}

func (a *agentImpl) streamOnce(ctx context.Context, tc *turnContext, messages []Message) (roundResult, error) {
	responseText, reasoningText, finishReason, toolCalls, promptTokens, completionTokens, err := a.collectStreamAndLog(ctx, messages)
	if err != nil {
		return roundResult{}, err
	}

	a.sendThinkingIfNeeded(tc.session, reasoningText)
	a.sendThinkingTokens(tc.session.GetPeerID(), promptTokens, completionTokens)

	return roundResult{
		text:             responseText,
		reasoning:        reasoningText,
		finishReason:     finishReason,
		toolCalls:        toolCalls,
		promptTokens:     promptTokens,
		completionTokens: completionTokens,
	}, nil
}

func (a *agentImpl) resolveToolCalls(ctx context.Context, tc *turnContext, messages []Message, round roundResult) turnCalls {
	if name := unparseableToolCallName(round.toolCalls); name != "" {
		return a.retryUnparseableToolCall(tc, name)
	}
	if calls, ok := a.nativeToolCalls(ctx, messages, round); ok {
		return turnCalls{toolCalls: calls}
	}

	if calls, content, ok := parseEmbeddedXMLToolCalls(round); ok {
		return turnCalls{assistantContent: content, toolCalls: calls}
	}

	if calls, content, ok := parseEmbeddedJSONToolCalls(round); ok {
		return turnCalls{assistantContent: content, toolCalls: calls}
	}

	if isMalformedToolCallResponse(round) && tc.malformedRetries < maxMalformedCallRetries {
		tc.malformedRetries++
		fmt.Printf("%s[TOOL] Malformed tool call in response, sending correction (%d/%d)\n", a.agentPrefix(), tc.malformedRetries, maxMalformedCallRetries)
		logger.DebugToFile("%s[TOOL] Malformed tool call response, correction %d/%d", a.agentPrefix(), tc.malformedRetries, maxMalformedCallRetries)
		tc.session.AddUserMessage(formatCorrectionMessage)
		return turnCalls{retryRequested: true}
	}

	return turnCalls{}
}

func (a *agentImpl) retryUnparseableToolCall(tc *turnContext, name string) turnCalls {
	if tc.malformedRetries >= maxMalformedCallRetries {
		logger.DebugToFile("%s[TOOL] Tool call '%s' still invalid after %d corrections, skipping execution", a.agentPrefix(), name, maxMalformedCallRetries)
		return turnCalls{}
	}

	tc.malformedRetries++
	fmt.Printf("%s[TOOL] Tool call '%s' has incomplete/invalid JSON arguments, sending correction (%d/%d)\n", a.agentPrefix(), name, tc.malformedRetries, maxMalformedCallRetries)
	logger.DebugToFile("%s[TOOL] Tool call '%s' invalid JSON arguments, correction %d/%d", a.agentPrefix(), name, tc.malformedRetries, maxMalformedCallRetries)
	tc.session.AddUserMessage(truncatedToolCallCorrection)
	return turnCalls{retryRequested: true}
}

func (a *agentImpl) nativeToolCalls(ctx context.Context, messages []Message, round roundResult) ([]ToolCall, bool) {
	if round.finishReason != "tool_calls" && len(round.toolCalls) == 0 {
		return nil, false
	}
	if len(round.toolCalls) > 0 {
		return round.toolCalls, true
	}

	fmt.Printf("%s[WARN] LLM returned tool_calls but none collected, trying non-streaming\n", a.agentPrefix())
	calls, err := a.getToolCallsFromResponse(ctx, messages, a.toolsRegistry.ToOpenAISchema())
	if err != nil || len(calls) == 0 {
		return nil, false
	}
	return calls, true
}

func parseEmbeddedXMLToolCalls(round roundResult) ([]ToolCall, string, bool) {
	textToCheck := round.text
	if len(round.reasoning) > len(textToCheck) {
		textToCheck = round.reasoning
	}

	parsed := ParseXMLToolCalls(textToCheck)
	if len(parsed.ToolCalls) == 0 && round.text != round.reasoning {
		parsed = ParseXMLToolCalls(round.text)
	}
	if len(parsed.ToolCalls) == 0 {
		return nil, "", false
	}
	return convertXMLToolCalls(parsed.ToolCalls), parsed.Content, true
}

func parseEmbeddedJSONToolCalls(round roundResult) ([]ToolCall, string, bool) {
	parsed := ParseJSONToolCalls(round.text)
	if len(parsed.ToolCalls) == 0 {
		return nil, "", false
	}
	return convertXMLToolCalls(parsed.ToolCalls), parsed.Content, true
}

func isMalformedToolCallResponse(round roundResult) bool {
	if round.finishReason == "" {
		return false
	}
	if round.text == "" && round.reasoning != "" &&
		(strings.Contains(round.reasoning, "<tool_call>") || hasPartialToolCall(round.reasoning)) {
		return true
	}
	if strings.Contains(round.text, "<tool_call") && len(ParseXMLToolCalls(round.text).ToolCalls) == 0 {
		return true
	}
	return false
}

func (a *agentImpl) executeAndAppendBatch(ctx context.Context, tc *turnContext, assistantContent string, calls []ToolCall) {
	unique, duplicates := a.partitionToolCalls(tc, calls)

	sessionCalls := make([]sess.MsgToolCall, len(calls))
	for i, tcAll := range calls {
		sessionCalls[i] = sess.MsgToolCall{
			ID:   tcAll.ID,
			Type: tcAll.Type,
			Function: sess.MsgToolCallFunc{
				Name:      tcAll.Function.Name,
				Arguments: string(tcAll.Function.Arguments),
			},
		}
	}
	tc.session.AddAssistantMessageWithToolCalls(assistantContent, sessionCalls)

	for _, dup := range duplicates {
		tc.session.AddToolMessage(dup.ID, dup.Function.Name, duplicateToolCallNotice)
	}

	if len(unique) > 0 {
		result := a.executeAllTools(ctx, unique, tc.session.GetPeerID())
		for _, tr := range result.ToolCalls {
			tc.session.AddToolMessage(tr.ToolCallID, tr.ToolName, tr.Content)
		}
		a.fireCheckpoint(toolCallNames(unique))
	}

	logger.DebugToFile("%s[OMP-LOOP] batch executed: %d unique, %d duplicates", a.agentPrefix(), len(unique), len(duplicates))
}

func (a *agentImpl) partitionToolCalls(tc *turnContext, calls []ToolCall) ([]ToolCall, []ToolCall) {
	var unique, duplicates []ToolCall
	for _, call := range calls {
		sig := toolCallSignature(call)
		if tc.executed[sig] {
			duplicates = append(duplicates, call)
			continue
		}
		tc.executed[sig] = true
		unique = append(unique, call)
	}
	return unique, duplicates
}

func (a *agentImpl) drainSteering(s *sess.Session) []string {
	in := s.GetPeerInput()
	if in == nil {
		return nil
	}
	return in.Drain()
}

func (a *agentImpl) appendSteeringMessages(s *sess.Session, pending []string) {
	for _, m := range pending {
		s.AddUserMessage(m)
		logger.DebugToFile("%s[OMP-LOOP] promoted steering message into session: %q", a.agentPrefix(), truncateForLog(m))
	}
}

func truncateForLog(m string) string {
	if len(m) > 80 {
		return m[:80] + "..."
	}
	return m
}

func (a *agentImpl) appendAssistantText(s *sess.Session, text string) {
	cleaned := cleanAssistantText(a, s, text)
	if cleaned != "" {
		s.AddAssistantMessage(cleaned)
	}
}

func (a *agentImpl) finishWithTextResponse(s *sess.Session, text string) string {
	cleaned := cleanAssistantText(a, s, text)
	if cleaned == "" {
		if content, ok := lastAssistantContent(s); ok {
			logger.DebugToFile("%s[OMP-LOOP] empty final response, falling back to last assistant content", a.agentPrefix())
			return content
		}
		return ""
	}
	s.AddAssistantMessage(cleaned)
	return cleaned
}

func cleanAssistantText(a *agentImpl, s *sess.Session, text string) string {
	parsed := ParseXMLToolCalls(text)
	cleaned := a.stripThinkingTags(parsed.Content, s.GetPeerID())
	return internalmsg.Strip(cleaned)
}

func isTerminalResponse(responseText string, hasToolCalls, hasReasoning bool) bool {
	if len(strings.TrimSpace(responseText)) > 0 {
		return true
	}
	return hasToolCalls || hasReasoning
}

func lastAssistantContent(session *sess.Session) (string, bool) {
	hist := session.GetHistory()
	for i := len(hist) - 1; i >= 0; i-- {
		msg := hist[i]
		if msg.Role != sess.AssistantRole {
			continue
		}
		if !publishableAssistantMessage(msg) {
			continue
		}
		return msg.Content, true
	}
	return "", false
}

func publishableAssistantMessage(msg sess.Message) bool {
	if msg.Internal {
		return false
	}
	return publishableAssistantContent(msg.Content)
}

func publishableAssistantContent(content string) bool {
	if strings.TrimSpace(content) == "" {
		return false
	}
	if internalmsg.IsInternal(content) {
		return false
	}
	return !containsXMLToolTags(content)
}

func containsXMLToolTags(content string) bool {
	return strings.Contains(content, "<tool_call") || strings.Contains(content, "<function=")
}

func toolCallNames(calls []ToolCall) string {
	names := make([]string, 0, len(calls))
	for _, tc := range calls {
		names = append(names, tc.Function.Name)
	}
	return strings.Join(names, ",")
}

func shouldAddAutoContinue(session *sess.Session) bool {
	history := session.GetHistory()

	autoContinueIdx := -1
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		if m.Role == sess.UserRole && (m.Content == tokenizers.CompactionAutoContinueText ||
			m.Content == tokenizers.CompactionOverflowContinueText) {
			autoContinueIdx = i
			break
		}
	}

	if autoContinueIdx < 0 {
		return true
	}

	for j := autoContinueIdx + 1; j < len(history); j++ {
		m := history[j]
		if m.Role == sess.AssistantRole && !m.Summary {
			return true
		}
	}

	return false
}

func (a *agentImpl) applyAggressivePruning(session *sess.Session) int {
	history := session.GetHistory()
	raw := make([]tokenizers.Message, len(history))

	for i, msg := range history {
		content := msg.Content
		for _, tc := range msg.ToolCalls {
			content += tc.Function.Arguments
		}
		raw[i] = tokenizers.Message{
			Role:        string(msg.Role),
			Content:     content,
			Summary:     msg.Summary,
			Compacted:   msg.Compacted,
			TailStartID: msg.TailStartID,
		}
	}

	pruned := compress.PruneMessages(raw, compress.PRUNE_PROTECTED_TOOLS...)

	prunedCount := 0
	for i := range pruned {
		if pruned[i].Compacted && !raw[i].Compacted {
			session.MarkMessageCompacted(i, compress.PRUNED_OUTPUT_PLACEHOLDER)
			prunedCount++
		}
	}

	return prunedCount
}

func (a *agentImpl) collectStreamAndLog(ctx context.Context, messages []Message) (string, string, string, []ToolCall, int, int, error) {
	toolsSchema := a.toolsRegistry.ToOpenAISchema()
	streamConfig := a.buildToolsStreamConfig(toolsSchema)

	responseText, reasoningText, finishReason, streamToolCalls, promptTokens, completionTokens, err := a.streamAndCollect(ctx, streamConfig, messages)
	if err != nil {
		return "", "", "", nil, 0, 0, err
	}

	prefix := a.agentPrefix()
	logger.DebugToFile("%sstreaming response: content=%d, reasoning=%d, tool_calls=%d, finish=%q, in=%d, out=%d",
		prefix, len(responseText), len(reasoningText), len(streamToolCalls), finishReason, promptTokens, completionTokens)
	if len(responseText) > 0 {
		logger.DebugToFile("\n---------------- response content ----------------------")
		logger.DebugToFile("%s%s", prefix, responseText)
	}
	if len(reasoningText) > 0 {
		logger.DebugToFile("\n---------------- response reasoning ------------------")
		logger.DebugToFile("%s%s", prefix, reasoningText)
	}
	logger.DebugToFile("\n====================================================")

	return responseText, reasoningText, finishReason, streamToolCalls, promptTokens, completionTokens, nil
}

func (a *agentImpl) resolveToolSchemas(toolsSchema []map[string]interface{}) []map[string]interface{} {
	if toolsSchema == nil && len(a.toolSchemas) > 0 {
		return a.toolSchemas
	}
	return toolsSchema
}

func (a *agentImpl) currentToolSchemas() []map[string]interface{} {
	return a.resolveToolSchemas(a.toolsRegistry.ToOpenAISchema())
}

func (a *agentImpl) buildToolsStreamConfig(toolsSchema []map[string]interface{}) StreamingConfig {
	schema := a.resolveToolSchemas(toolsSchema)
	return StreamingConfig{
		Model:       a.config.Model,
		MaxTokens:   a.config.MaxTokens,
		Temperature: a.config.Temperature,
		Tools:       schema,
		Stream:      true,
	}
}

func (a *agentImpl) sendThinkingIfNeeded(session *sess.Session, reasoningText string) {
	if a.thinkingCallback == nil {
		return
	}
	parsed := ParseXMLToolCalls(reasoningText)
	cleanedReasoning := parsed.Content
	if cleanedReasoning == "" {
		return
	}

	if hasPartialToolCall(cleanedReasoning) {
		fmt.Print(a.agentPrefix() + "[TOOL] Stripped partial/malformed tool call fragments from reasoning\n")
		cleanedReasoning = stripPartialToolCall(cleanedReasoning)
		if cleanedReasoning == "" {
			return
		}
	}

	displayText := cleanedReasoning
	if len(displayText) > maxReasoningLength {
		displayText = displayText[:maxReasoningLength] + "…"
	}

	if err := a.thinkingCallback(session.GetPeerID(), displayText); err != nil {
		fmt.Printf("%s[WARN] Failed to send thinking message: %v\n", a.agentPrefix(), err)
	}
}
