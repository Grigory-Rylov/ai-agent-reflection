package agent

import (
		"context"
		"fmt"
	"strings"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type contextKey string

const toolCallDepthKey contextKey = "tool_call_depth"

const maxToolCallDepth = 50

func (a *agentImpl) processToolResults(ctx context.Context, originalMessages []Message, assistantContent string, toolCalls []ToolCall, toolResults []ToolCallResult, session *sess.Session, executed map[string]bool) (string, error) {
	depth, _ := ctx.Value(toolCallDepthKey).(int)
	if a.config.LlamaServerURL == "" {
		return "", nil
	}

	if depth >= maxToolCallDepth {
		prefix := a.agentPrefix()
		limitMessage := fmt.Sprintf("[TOOL] Tool call recursion limit reached (%d batches in one turn), stopping to avoid an unbounded loop.", maxToolCallDepth)
		fmt.Printf("%s%s\n", prefix, limitMessage)
		logger.DebugToFile(prefix+"%s", limitMessage)
		a.sendThinking(session.GetPeerID(), "[TOOL] Tool call recursion limit reached, finishing turn")
		return limitMessage, nil
	}

	sessionToolCalls := make([]sess.MsgToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		sessionToolCalls[i] = sess.MsgToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: sess.MsgToolCallFunc{
				Name:      tc.Function.Name,
				Arguments: string(tc.Function.Arguments),
			},
		}
	}
	session.AddAssistantMessageWithToolCalls(assistantContent, sessionToolCalls)

	for _, tr := range toolResults {
		session.AddToolMessage(tr.ToolCallID, tr.ToolName, tr.Content)
	}

	var messages []Message
	if a.promoteSteers(ctx, session) {
		messages = a.buildToolResultMessagesFromSession(session)
	} else {
		messages = a.buildToolResultMessages(originalMessages, assistantContent, toolCalls, toolResults)
	}

	if a.compactor != nil {
		messages = a.compactIfNeededBeforeLLM(ctx, session, messages, assistantContent, toolCalls, toolResults)
	}

	streamConfig := StreamingConfig{
		Model:       a.config.Model,
		MaxTokens:   a.config.MaxTokens,
		Temperature: a.config.Temperature,
		Tools:       a.toolsRegistry.ToOpenAISchema(),
		Stream:      true,
	}

	responseText, reasoningText, finishReason, streamToolCalls, promptTokens, completionTokens, err := a.streamAndCollect(ctx, streamConfig, messages)
	if err != nil {

		if IsContextOverflowError(err) && a.compactor != nil {
			prefix := a.agentPrefix()
			fmt.Printf(prefix+"[OPENCODE-COMPACT] Reactive overflow recovery for peer %d\n", session.GetPeerID())
			logger.DebugToFile(prefix+"[OPENCODE-COMPACT] Peer %d: Reactive overflow recovery, compacting", session.GetPeerID())

			a.compactIfNeeded(ctx, session, false)

			if !a.sessionHasToolResults(session, toolResults) {
				sessionToolCalls := make([]sess.MsgToolCall, len(toolCalls))
				for i, tc := range toolCalls {
					sessionToolCalls[i] = sess.MsgToolCall{
						ID:   tc.ID,
						Type: tc.Type,
						Function: sess.MsgToolCallFunc{
							Name:      tc.Function.Name,
							Arguments: string(tc.Function.Arguments),
						},
					}
				}
				session.AddAssistantMessageWithToolCalls(assistantContent, sessionToolCalls)
				for _, tr := range toolResults {
					session.AddToolMessage(tr.ToolCallID, tr.ToolName, compress.TruncateToolOutput(tr.Content))
				}
			}

			if shouldAddAutoContinue(session) {
				session.AddUserMessage(tokenizers.CompactionOverflowContinueText)
			}

			messages = a.buildToolResultMessagesFromSession(session)

			responseText, reasoningText, finishReason, streamToolCalls, promptTokens, completionTokens, err = a.streamAndCollect(ctx, streamConfig, messages)
			if err != nil {
				err = a.handleOverflowAfterCompaction(ctx, session, streamConfig, &messages, &err, &responseText, &reasoningText, &finishReason, &streamToolCalls, &promptTokens, &completionTokens)
				if err != nil {

					prefix := a.agentPrefix()
					fmt.Printf(prefix+"[ERROR] Context overflow after reactive compaction: %v\n", err)
					return "", fmt.Errorf("context overflow after compaction: %w", err)
				}
			}
		} else {
			return "", err
		}
	}

	if !isTerminalResponse(responseText, len(streamToolCalls) > 0, reasoningText != "") {
		responseText, reasoningText, finishReason, streamToolCalls, promptTokens, completionTokens, err = a.retryEmptyResponse(ctx, streamConfig, messages, session)
		if err != nil {
			return "", err
		}
	}

	loopRepeats := a.checkResponseLoop(session.GetPeerID(), responseText, reasoningText, streamToolCalls)
	if loopRepeats > 0 {
		a.injectLoopCorrection(session, loopRepeats)
	}

	a.sendThinkingIfNeeded(session, reasoningText)

	a.sendThinkingTokens(session.GetPeerID(), promptTokens, completionTokens)

	prefix := a.agentPrefix()
	logger.DebugToFile("%sprocessToolResults: content=%d, reasoning=%d, tool_calls=%d, finish=%q",
		prefix, len(responseText), len(reasoningText), len(streamToolCalls), finishReason)
	if len(responseText) > 0 {
		logger.DebugToFile("\n---------------- response content ----------------------")
		logger.DebugToFile("%s%s", prefix, responseText)
	}
	if len(reasoningText) > 0 {
		logger.DebugToFile("\n---------------- response reasoning ------------------")
		logger.DebugToFile("%s%s", prefix, reasoningText)
	}
	logger.DebugToFile("\n====================================================")

	if len(streamToolCalls) > 0 {
		a.debugLog.Debug("NATIVE format: detected %d tool calls in tool results response", len(streamToolCalls))

		var uniqueCalls []ToolCall
		var sawDuplicate bool
		for _, tc := range streamToolCalls {
			sig := toolCallSignature(tc)
			if executed[sig] {
				sawDuplicate = true
				a.debugLog.Debug("NATIVE duplicate skipped in tool results: %s", tc.Function.Name)
				continue
			}
			executed[sig] = true
			uniqueCalls = append(uniqueCalls, tc)
		}

		if len(uniqueCalls) == 0 && sawDuplicate {
			repeats := a.checkResponseLoop(session.GetPeerID(), responseText, reasoningText, streamToolCalls)
			if repeats > 0 {
				a.injectLoopCorrection(session, repeats)
			}
		}
		if len(uniqueCalls) == 0 {
			a.debugLog.Debug("All native tool calls are duplicates, returning existing response without execution")
			if responseText == "" {
				hist := session.GetHistory()
				if len(hist) > 0 {
					last := hist[len(hist)-1]
					if last.Role == sess.AssistantRole && last.Content != "" {
						return last.Content, nil
					}
				}
			}
			return responseText, nil
		}

		result := a.executeAllTools(ctx, uniqueCalls, session.GetPeerID())
		if len(result.ToolCalls) > 0 {
			recursiveCtx := context.WithValue(ctx, toolCallDepthKey, depth+1)
			return a.processToolResults(recursiveCtx, messages, "", uniqueCalls, result.ToolCalls, session, executed)
		}
	}

	textToCheck := responseText
	if len(reasoningText) > len(textToCheck) {
		textToCheck = reasoningText
	}

	preview := textToCheck
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}
	logger.DebugToFile("processToolResults: textToCheck preview: %q", preview)
	logger.DebugToFile("--------------------------------")

	parsed := ParseXMLToolCalls(textToCheck)
	logger.DebugToFile("processToolResults: parsed %d XML tool calls from textToCheck", len(parsed.ToolCalls))

	if len(parsed.ToolCalls) == 0 && responseText != reasoningText {
		parsed = ParseXMLToolCalls(responseText)
	}

	if len(parsed.ToolCalls) > 0 {
		a.debugLog.Debug("XML fallback: detected %d tool calls in tool results response", len(parsed.ToolCalls))
		toolCalls := convertXMLToolCalls(parsed.ToolCalls)

		var uniqueCalls []ToolCall
		for _, tc := range toolCalls {
			sig := toolCallSignature(tc)
			if executed[sig] {
				a.debugLog.Debug("XML duplicate skipped in tool results: %s", tc.Function.Name)
				continue
			}
			executed[sig] = true
			uniqueCalls = append(uniqueCalls, tc)
		}
		if len(uniqueCalls) > 0 {
			result := a.executeAllTools(ctx, uniqueCalls, session.GetPeerID())
			if len(result.ToolCalls) > 0 {
				recursiveCtx := context.WithValue(ctx, toolCallDepthKey, depth+1)
				return a.processToolResults(recursiveCtx, messages, parsed.Content, uniqueCalls, result.ToolCalls, session, executed)
			}
		}
	}

	jsonParsed := ParseJSONToolCalls(responseText)
	if len(jsonParsed.ToolCalls) > 0 {
		a.debugLog.Debug("JSON fallback: detected %d tool calls in tool results response", len(jsonParsed.ToolCalls))
		responseText = jsonParsed.Content

		toolCalls := convertXMLToolCalls(jsonParsed.ToolCalls)
		var uniqueCalls []ToolCall
		for _, tc := range toolCalls {
			sig := toolCallSignature(tc)
			if executed[sig] {
				a.debugLog.Debug("JSON duplicate skipped in tool results: %s", tc.Function.Name)
				continue
			}
			executed[sig] = true
			uniqueCalls = append(uniqueCalls, tc)
		}
		if len(uniqueCalls) > 0 {
			result := a.executeAllTools(ctx, uniqueCalls, session.GetPeerID())
			if len(result.ToolCalls) > 0 {
				recursiveCtx := context.WithValue(ctx, toolCallDepthKey, depth+1)
				return a.processToolResults(recursiveCtx, messages, jsonParsed.Content, uniqueCalls, result.ToolCalls, session, executed)
			}
		}
	}

	if responseText != "" {
		parsedResp := ParseXMLToolCalls(responseText)
		responseText = parsedResp.Content
		responseText = a.stripThinkingTags(responseText, session.GetPeerID())
	}

	if responseText == "" {
		hist := session.GetHistory()
		if len(hist) > 0 {
			last := hist[len(hist)-1]
			if last.Role == sess.AssistantRole && last.Content != "" {
				return last.Content, nil
			}
		}
		return "", nil
	}

	session.AddAssistantMessage(responseText)
	return responseText, nil
}

func (a *agentImpl) handleInvalidXMLToolCall(ctx context.Context, messages []Message, session *sess.Session, executed map[string]bool) (FunctionCallResult, error) {
	a.sendThinking(session.GetPeerID(), "[TOOL] Error: Invalid XML tool call format. Send the model a corrective message.")
	logger.DebugToFile("%s[TOOL] Invalid XML: sending format error to model", a.agentPrefix())

	errMsg := "FORMAT ERROR: You tried to use XML tags (<tool_call>, <function=...>) for tool calls, but this format is incorrect. " +
		"You must use native function calling: declare the function name and arguments in the tool_calls array provided by the API. " +
		"Do NOT write XML tool tags yourself. If you included any text along with the tool call, re-send it with the correct format."

	toolCall := ToolCall{
		ID:   "format_error",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "__format_error",
			Arguments: []byte(`{}`),
		},
	}
	toolResult := ToolCallResult{
		ToolCallID: "format_error",
		ToolName:   "__format_error",
		Content:    errMsg,
		IsError:    true,
	}

	executed[toolCallSignature(toolCall)] = true

	finalResponse, err := a.processToolResults(ctx, messages, "", []ToolCall{toolCall}, []ToolCallResult{toolResult}, session, executed)
	if err != nil {
		return FunctionCallResult{}, fmt.Errorf("process format error: %w", err)
	}
	return FunctionCallResult{Success: true, Response: finalResponse}, nil
}

func isTerminalResponse(responseText string, hasToolCalls, hasReasoning bool) bool {
	if len(strings.TrimSpace(responseText)) > 0 {
		return true
	}

	return hasToolCalls || hasReasoning
}

const maxEmptyRetries = 3

func (a *agentImpl) retryEmptyResponse(ctx context.Context, streamConfig StreamingConfig, messages []Message, session *sess.Session) (string, string, string, []ToolCall, int, int, error) {
	for attempt := 0; attempt < maxEmptyRetries; attempt++ {
		prefix := a.agentPrefix()
		fmt.Printf(prefix+"[WARN] LLM returned empty response (attempt %d/%d), retrying\n", attempt+1, maxEmptyRetries)

		messages = append(messages, Message{
			Role:    "user",
			Content: "[SYSTEM] Your previous response was empty. Please generate a text response based on the tool results above.",
		})

		responseText, reasoningText, finishReason, streamToolCalls, promptTokens, completionTokens, err := a.streamAndCollect(ctx, streamConfig, messages)
		if err != nil {
			return "", "", "stop", nil, 0, 0, err
		}
		if isTerminalResponse(responseText, len(streamToolCalls) > 0, reasoningText != "") {
			return responseText, reasoningText, finishReason, streamToolCalls, promptTokens, completionTokens, nil
		}
	}

	prefix := a.agentPrefix()
	fmt.Printf(prefix+"[WARN] LLM returned empty response after %d retries\n", maxEmptyRetries)
	return "", "", "stop", nil, 0, 0, nil
}

func (a *agentImpl) buildToolResultMessages(originalMessages []Message, assistantContent string, toolCalls []ToolCall, toolResults []ToolCallResult) []Message {
	messages := make([]Message, len(originalMessages))
	copy(messages, originalMessages)

	reqToolCalls := make([]ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		reqToolCalls[i] = buildToolCallForRequest(tc)
	}
	messages = append(messages, Message{
		Role:      "assistant",
		Content:   assistantContent,
		ToolCalls: reqToolCalls,
	})

	for _, tr := range toolResults {
		messages = append(messages, Message{
			Role:       "tool",
			ToolCallID: tr.ToolCallID,
			Name:       tr.ToolName,
			Content:    tr.Content,
		})
	}

	return messages
}

func (a *agentImpl) compactIfNeededBeforeLLM(ctx context.Context, session *sess.Session, messages []Message, assistantContent string, toolCalls []ToolCall, toolResults []ToolCallResult) []Message {
	tokenMessages := make([]tokenizers.Message, len(messages))
	for i, m := range messages {
		content := m.Content

		for _, tc := range m.ToolCalls {
			content += string(tc.Function.Arguments)
		}
		tokenMessages[i] = tokenizers.Message{
			Role:    m.Role,
			Content: content,
		}
	}

	tokens := compress.EstimateMessagesTokensSimple(tokenMessages)
	if !compress.IsOverflowWithLimits(tokens, a.config.MaxTokens, a.config.ModelLimitInput, a.config.CompactionReserved) {
		return messages
	}

	prefix := a.agentPrefix()
	fmt.Printf(prefix+"[OPENCODE-COMPACT] Tool results overflow (%d tokens), compacting before LLM request\n", tokens)
	logger.DebugToFile(prefix+"[OPENCODE-COMPACT] Peer %d: Tool results overflow (%d/%d), compacting",
		session.GetPeerID(), tokens, a.config.MaxTokens)

	a.compactIfNeeded(ctx, session, false)

	hasLastToolResult := a.sessionHasToolResults(session, toolResults)

	if !hasLastToolResult {
		sessionToolCalls := make([]sess.MsgToolCall, len(toolCalls))
		for i, tc := range toolCalls {
			sessionToolCalls[i] = sess.MsgToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: sess.MsgToolCallFunc{
					Name:      tc.Function.Name,
					Arguments: string(tc.Function.Arguments),
				},
			}
		}
		session.AddAssistantMessageWithToolCalls(assistantContent, sessionToolCalls)

		for _, tr := range toolResults {
			session.AddToolMessage(tr.ToolCallID, tr.ToolName, compress.TruncateToolOutput(tr.Content))
		}
	}

	return a.buildToolResultMessagesFromSession(session)
}

func (a *agentImpl) sessionHasToolResults(session *sess.Session, toolResults []ToolCallResult) bool {
	if len(toolResults) == 0 {
		return true
	}
	visible := session.GetContextMessages()
	for _, tr := range toolResults {
		found := false
		for _, m := range visible {
			if m.Role == sess.ToolRole && m.ToolCallID == tr.ToolCallID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
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

func (a *agentImpl) buildToolResultMessagesFromSession(session *sess.Session) []Message {
	return a.convertHistoryToAPIMessages(session.GetContextMessages())
}

func (a *agentImpl) handleOverflowAfterCompaction(
	ctx context.Context,
	session *sess.Session,
	config StreamingConfig,
	messages *[]Message,
	errPtr *error,
	responseText *string,
	reasoningText *string,
	finishReason *string,
	toolCalls *[]ToolCall,
	promptTokens *int,
	completionTokens *int,
) error {
	err := *errPtr
	if !IsContextOverflowError(err) || a.compactor == nil {
		return err
	}

	prefix := a.agentPrefix()
	fmt.Printf("%s[OPENCODE-COMPACT] Aggressive pruning after overflow post-compaction\n", prefix)

	prunedCount := a.applyAggressivePruning(session)
	if prunedCount == 0 {

		fmt.Printf("%s[ERROR] Context overflow after reactive compaction and pruning: %v\n", prefix, err)
		return fmt.Errorf("context overflow after compaction and aggressive pruning: %w", err)
	}

	fmt.Printf("%s[OPENCODE-COMPACT] Pruned %d tool outputs, retrying\n", prefix, prunedCount)

	*messages = a.buildToolResultMessagesFromSession(session)
	*responseText, *reasoningText, *finishReason, *toolCalls, *promptTokens, *completionTokens, err = a.streamAndCollect(ctx, config, *messages)
	if err != nil {
		return fmt.Errorf("context overflow after compaction and aggressive pruning: %w", err)
	}

	*errPtr = nil
	return nil
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
	for i, m := range pruned {
		if m.Compacted && !raw[i].Compacted {
			session.MarkMessageCompacted(i, compress.PRUNED_OUTPUT_PLACEHOLDER)
			prunedCount++
		}
	}

	return prunedCount
}
