package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/opencode/llama-client/pkg/agent"
	"github.com/opencode/llama-client/pkg/agentpolicy"
	"github.com/opencode/llama-client/pkg/compress"
	"github.com/opencode/llama-client/pkg/modelsconfig"
	"github.com/opencode/llama-client/pkg/tokenizers"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

type AgentLoop interface {
	ProcessPrompt(ctx context.Context, prompt string, peerID int64) (string, error)
	ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error)
	Start(ctx context.Context)
	Stop()
	ResetSession(peerID int64)
	GetSession(peerID int64) *session.Session
	EnsureSession(peerID int64) *session.Session
	SetThinkingCallback(cb func(peerID int64, content string) error)
	GetContextStats(peerID int64) (charCount int, tokenCount int, err error)
	TestLlamaServer(ctx context.Context) (model string, responseTime time.Duration, tokensPerSec float64, err error)
	GetModelHolder() *modelsconfig.Holder
}

type agentLoop struct {
	config           LoopConfig
	sessionM         sync.Map
	vk               VKClient
	registry         ToolRegistry
	compactor        *compress.Compactor
	tokenizer        tokenizers.Tokenizer
	dispatcher       *EventDispatcher
	stopCh           chan struct{}
	isRunning        bool
	mu               sync.Mutex
	log              Logger
	aiHistory        []string
	historyMu        sync.Mutex
	thinkingCallback func(peerID int64, content string) error
}

func NewAgentLoop(config LoopConfig, vk VKClient, registry ToolRegistry) (AgentLoop, error) {
	alias, modelName, llamaURL := config.ModelHolder.GetCurrent()

	if llamaURL == "" {
		llamaURL = "http://127.0.0.1:8081"
	}
	if modelName == "" {
		modelName = "local-model"
	}

	var l Logger
	if config.Logger != nil {
		l = config.Logger
	} else if config.EnableLogging {
		l = NewDefaultLogger(config.Debug)
	}

	// Лимит контекста для текущей модели:
	// 1) явно задан в models.json (поле context) — приоритет,
	// 2) иначе — реальный контекст с сервера (аргумент --ctx-size/-c),
	// 3) иначе — config.MaxTokens.
	modelCtx := config.ModelHolder.GetModelContext(alias)
	if modelCtx > 0 {
		config.MaxTokens = modelCtx
	}

	tokenizer := tokenizers.NewLlamaServerTokenizer(llamaURL, modelName, config.MaxTokens)
	if config.EnableLogging {
		tokenizer.SetDebug(true)
	}

	switch {
	case modelCtx > 0:
		if l != nil {
			l.InfoLogf("Using model context from models.json for %s: %d tokens", alias, modelCtx)
		}
	case tokenizer.InitializeContextLimit() == nil:
		actualCtx := tokenizer.GetActualContextLimit()
		if l != nil {
			l.InfoLogf("Using actual server context for %s: %d tokens (config had %d)", alias, actualCtx, config.MaxTokens)
		}
		config.MaxTokens = actualCtx
	default:
		if l != nil {
			l.WarnLogf("Failed to get actual context limit from server (using configured maxTokens=%d)", config.MaxTokens)
		}
	}

	llmCompressor := compress.NewLLMCompressor(llamaURL, modelName, config.Temperature)
	compactor := compress.NewCompactor(llmCompressor)

	if l != nil {
		l.InfoLogf("AgentLoop initialized: model=%s host=%s maxTokens=%d", modelName, llamaURL, config.MaxTokens)
	}

	return &agentLoop{
		config:     config,
		vk:         vk,
		registry:   registry,
		compactor:  compactor,
		tokenizer:  tokenizer,
		stopCh:     make(chan struct{}),
		dispatcher: NewEventDispatcher(),
		log:        l,
	}, nil
}

func (al *agentLoop) GetModelHolder() *modelsconfig.Holder {
	return al.config.ModelHolder
}

func (al *agentLoop) GetContextStats(peerID int64) (charCount int, tokenCount int, err error) {
	s := al.GetSession(peerID)
	if s == nil {
		return 0, 0, fmt.Errorf("session not found for peer %d", peerID)
	}

	history := s.GetHistory()

	for _, msg := range history {
		charCount += len([]rune(msg.Content))
	}

	if al.tokenizer != nil && len(history) > 0 {
		messages := make([]tokenizers.Message, len(history))
		for i, msg := range history {
			messages[i] = tokenizers.Message{
				Role:    string(msg.Role),
				Content: msg.Content,
			}
		}
		tokenCount, err = al.tokenizer.CountMessagesTokens(messages)
		if err != nil {
			if al.log != nil {
				al.log.WarnLogf("Failed to count tokens for peer %d: %v", peerID, err)
			}
			tokenCount = 0
		}
	} else if al.tokenizer == nil && len(history) > 0 {
		if al.log != nil {
			al.log.WarnLogf("Tokenizer is nil, cannot count tokens for peer %d", peerID)
		}
	}

	return charCount, tokenCount, nil
}

func (al *agentLoop) ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error) {
	return al.ProcessPrompt(ctx, prompt, peerID)
}

func (al *agentLoop) ProcessPrompt(ctx context.Context, prompt string, peerID int64) (string, error) {
	sess := al.getOrCreateSession(peerID)

	if al.log != nil {
		al.log.InfoLogf("Prompt received from peer %d: %s", peerID, truncate(prompt, 100))
	}

	al.dispatcher.Emit(NewEvent(EventPromptReceived, peerID))

	sess.AddUserMessage(prompt)

	if al.config.EnableCompression {
		al.checkAndCompressOpenCode(ctx, sess, peerID)
	}

	messages := al.buildAPIMessages(sess)

	response, err := al.sendToLLM(ctx, messages, sess, peerID, prompt)
	if err != nil {
		if al.log != nil {
			al.log.ErrorLogf("LLM request failed: %v", err)
		}
		return "", fmt.Errorf("LLM request failed: %w", err)
	}

	if al.config.EnablePruning {
		al.runPruning(sess)
	}

	if al.checkLoopDetection(response, peerID) {
		if al.log != nil {
			al.log.WarnLogf("Adding loop alert to next prompt for peer %d", peerID)
		}
	}

	al.dispatcher.Emit(NewEvent(EventResponseDone, peerID))

	return response, nil
}

func (al *agentLoop) sendThinking(peerID int64, content string) {
	if !al.config.EnableThinking || al.config.ThinkingPeerID <= 0 {
		return
	}

	if al.thinkingCallback != nil {
		err := al.thinkingCallback(al.config.ThinkingPeerID, content)
		if err != nil {
			if al.log != nil {
				al.log.ErrorLogf("Failed to send thinking message: %v", err)
			}
			return
		}
	} else if al.vk != nil {
		_, err := al.vk.SendThinking(al.config.ThinkingPeerID, content)
		if err != nil {
			if al.log != nil {
				al.log.ErrorLogf("Failed to send thinking message: %v", err)
			}
			return
		}
	}

	al.dispatcher.Emit(NewEvent(EventThinking, peerID))

	if al.log != nil {
		al.log.InfoLogf("Thinking sent to peer %d: %s", al.config.ThinkingPeerID, truncate(content, 80))
	}
}

func (al *agentLoop) getOrCreateSession(peerID int64) *session.Session {
	if val, ok := al.sessionM.Load(peerID); ok {
		return val.(*session.Session)
	}

	config := al.config.SessionConfig
	config.PeerID = peerID

	if al.config.SystemPromptFile != "" {
		data, err := os.ReadFile(al.config.SystemPromptFile)
		if err == nil && strings.TrimSpace(string(data)) != "" {
			config.SystemPrompt = strings.TrimSpace(string(data))
			if al.log != nil {
				al.log.InfoLogf("Loaded system prompt from '%s'", al.config.SystemPromptFile)
			}
		} else {
			if al.log != nil {
				al.log.WarnLogf("Failed to read system prompt file: %v, using default", err)
			}
		}
	}

	if al.log != nil {
		al.log.InfoLogf("Creating session for peer %d, SessionFile: '%s'", peerID, config.SessionFile)
	}

	sess := session.NewSession(config)

	if al.log != nil {
		al.log.InfoLogf("Created new session for peer %d, history length: %d", peerID, sess.HistoryLength())
	}

	al.sessionM.Store(peerID, sess)

	return sess
}

func (al *agentLoop) checkLoopDetection(response string, peerID int64) bool {
	if !al.config.EnableLoopDetection {
		return false
	}

	al.historyMu.Lock()
	defer al.historyMu.Unlock()

	al.aiHistory = append(al.aiHistory, response)

	maxHistory := 5
	if len(al.aiHistory) > maxHistory {
		al.aiHistory = al.aiHistory[len(al.aiHistory)-maxHistory:]
	}

	if len(al.aiHistory) < 2 {
		return false
	}

	current := strings.TrimSpace(response)
	for i := len(al.aiHistory) - 2; i >= 0; i-- {
		previous := strings.TrimSpace(al.aiHistory[i])
		if similarity(current, previous) >= al.config.LoopThreshold {
			al.logLoopDetection(peerID, current, previous)
			al.aiHistory = []string{}
			return true
		}
	}

	return false
}

func (al *agentLoop) logLoopDetection(peerID int64, current, previous string) {
	if al.log != nil {
		al.log.WarnLogf("Loop detected for peer %d: response repeating", peerID)
	}
	al.dispatcher.Emit(NewEvent(EventLoopDetected, peerID))
}

func similarity(a, b string) float64 {
	if a == b {
		return 1.0
	}

	wordsA := strings.Fields(strings.ToLower(a))
	wordsB := strings.Fields(strings.ToLower(b))

	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0.0
	}

	common := 0
	for _, wA := range wordsA {
		for _, wB := range wordsB {
			if wA == wB {
				common++
				break
			}
		}
	}

	minLen := len(wordsA)
	if len(wordsB) < minLen {
		minLen = len(wordsB)
	}

	if minLen == 0 {
		return 0.0
	}

	return float64(common) / float64(minLen)
}

func (al *agentLoop) buildAPIMessages(sess *session.Session) []agent.Message {
	history := sess.GetContextMessages()
	messages := make([]agent.Message, len(history))

	for i, msg := range history {
		messages[i] = agent.Message{
			Role:    string(msg.Role),
			Content: msg.Content,
		}
	}

	return messages
}

func (al *agentLoop) sendToLLM(ctx context.Context, messages []agent.Message, sess *session.Session, peerID int64, prompt string) (string, error) {
	if al.log != nil {
		al.log.DebugLog("[sendToLLM] creating agent")
	}

	agentConfig := al.buildAgentConfig()
	var a agent.Agent = agent.NewAgent(agentConfig)

	if ps, ok := a.(interface{ SetPermissionChecker(agent.PermissionChecker) }); ok {
		ps.SetPermissionChecker(agentpolicy.NewPermissionAdapter(agentpolicy.UserFacingPermission()))
	}

	a.SetThinkingCallback(func(cbPeerID int64, content string) error {
		if !al.config.EnableThinking || al.config.ThinkingPeerID <= 0 {
			return nil
		}
		if al.thinkingCallback != nil {
			return al.thinkingCallback(al.config.ThinkingPeerID, content)
		} else if al.vk != nil {
			_, err := al.vk.SendThinking(al.config.ThinkingPeerID, content)
			return err
		}
		return nil
	})

	if wd := sess.GetWorkingDir(); wd != "" {
		tools.SetWorkingDir(wd)
	}

	if al.config.EnableTools && al.registry != nil {
		al.registerToolsToAgent(a, al.registry)
	}

	agentSess := a.GetSession(peerID)
	if len(agentSess.GetHistory()) <= 1 {
		for _, msg := range messages {
			switch msg.Role {
			case "system":
				continue
			case "assistant":
				agentSess.AddAssistantMessage(msg.Content)
			case "tool":
				agentSess.AddUserMessage(msg.Content)
			case "user":
				agentSess.AddUserMessage(msg.Content)
			}
		}
	} else if al.log != nil {
		al.log.DebugLog("[sendToLLM] agent session already has %d messages, skipping pre-seed", len(agentSess.GetHistory()))
	}

	response, err := a.ProcessMessage(ctx, prompt, peerID)
	if err != nil {
		return "", err
	}

	if response == "" {
		hist := agentSess.GetHistory()
		if len(hist) > 0 {
			last := hist[len(hist)-1]
			if last.Role == session.AssistantRole && last.Content != "" {
				response = last.Content
			}
		}
	}

	sess.AddAssistantMessage(response)

	return response, nil
}

func (al *agentLoop) buildAgentConfig() agent.Config {
	_, modelName, llamaURL := al.config.ModelHolder.GetCurrent()
	cfg := agent.Config{
		LlamaServerURL:                 llamaURL,
		Model:                          modelName,
		MaxTokens:                      al.config.MaxTokens,
		Temperature:                    al.config.Temperature,
		SessionConfig:                  al.config.SessionConfig,
		SystemPromptFile:               al.config.SystemPromptFile,
		EnableTools:                    al.config.EnableTools,
		MaxToolCalls:                   al.config.MaxToolCalls,
		Debug:                          al.config.Debug,
		SkipShellPermissionForPathless: al.config.SkipShellPermissionForPathless,
	}

	// Передаём список инструментов из реестра (включая MCP) в системный промпт
	if al.registry != nil {
		schemas := al.registry.ToOpenAISchema()
		if len(schemas) > 0 {
			names := make([]string, 0, len(schemas))
			for _, s := range schemas {
				if n, ok := s["name"].(string); ok {
					names = append(names, n)
				}
			}
			cfg.ToolsList = names
		}
	}

	return cfg
}

func (al *agentLoop) registerToolsToAgent(a agent.Agent, reg ToolRegistry) {
	if reg == nil {
		return
	}

	type toolInserter interface {
		RegisterTools(registry *tools.Registry)
	}
	if inserter, ok := a.(toolInserter); ok {
		if r, ok := reg.(*tools.Registry); ok {
			inserter.RegisterTools(r)
			return
		}
	}

	toolSchemas := reg.ToOpenAISchema()
	if len(toolSchemas) > 0 {
		a.SetTools(toolSchemas)
	}

	if al.log != nil {
		al.log.InfoLogf("Registered %d tools from registry", len(toolSchemas))
	}
}

func (al *agentLoop) processToolCalls(ctx context.Context, toolCalls []map[string]interface{}, sess *session.Session, peerID int64) ([]map[string]interface{}, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}

	if al.log != nil {
		al.log.InfoLogf("Processing %d tool calls for peer %d", len(toolCalls), peerID)
	}

	al.dispatcher.Emit(NewEvent(EventToolCall, peerID))

	results := make([]map[string]interface{}, len(toolCalls))

	for i, tc := range toolCalls {
		toolName := getStringField(tc, "name")

		if al.log != nil {
			al.log.InfoLogf("Executing tool: %s", toolName)
		}

		al.sendThinking(peerID, "Executing tool: "+toolName)

		var result string
		var execErr error

		if al.registry != nil {
			tool, ok := al.registry.Get(toolName)
			if !ok {
				result = fmt.Sprintf(`{"success": false, "error": "tool %s not found in registry"}`, toolName)
				execErr = fmt.Errorf("tool not found: %s", toolName)
			} else {
				argsRaw, _ := tc["arguments"].(string)
				var args map[string]string
				if argsRaw != "" {
					if err := json.Unmarshal([]byte(argsRaw), &args); err != nil {
						args = make(map[string]string)
					}
				} else {
					args = make(map[string]string)
				}

				toolResult, err := tool.Execute(ctx, args)
				if err != nil {
					result = tools.MarshalToolResult(toolResult)
					execErr = err
				} else {
					result = tools.MarshalToolResult(toolResult)
					if !toolResult.Success {
						execErr = fmt.Errorf("%s", toolResult.Error)
					}
				}
			}
		} else {
			result = fmt.Sprintf(`{"success": false, "error": "no tool registry"}`)
			execErr = fmt.Errorf("no tool registry")
		}

		results[i] = map[string]interface{}{
			"tool_name": toolName,
			"result":    result,
			"error":     execErr,
		}

		al.dispatcher.Emit(NewEvent(EventToolResult, peerID))

		if al.log != nil {
			if execErr != nil {
				al.log.ErrorLogf("Tool %s failed: %v", toolName, execErr)
			} else {
				al.log.InfoLogf("Tool %s completed", toolName)
			}
		}
	}

	return results, nil
}

func getStringField(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func (al *agentLoop) checkAndCompressOpenCode(ctx context.Context, sess *session.Session, peerID int64) {
	history := sess.GetHistory()

	messages := al.convertHistoryToMessages(history)
	tokensBefore := compress.EstimateMessagesTokensSimple(messages)

	if al.log != nil {
		al.log.DebugLogf("[OPENCODE-COMPACT] Peer %d: %d messages, ~%d tokens",
			peerID, len(messages), tokensBefore)
	}

	if !compress.IsOverflow(tokensBefore, al.config.MaxTokens, al.config.CompactionReserved) {
		if al.log != nil {
			al.log.DebugLogf("[OPENCODE-COMPACT] Peer %d: No overflow, skipping", peerID)
		}
		return
	}

	if al.log != nil {
		al.log.InfoLogf("[OPENCODE-COMPACT] Peer %d: Overflow detected (%d/%d), compacting",
			peerID, tokensBefore, al.config.MaxTokens)
	}

	tailTurns := al.config.TailTurns
	if tailTurns <= 0 {
		tailTurns = 2
	}

	result, err := al.compactor.CompactWithOpenCode(ctx, messages, al.config.MaxTokens, tailTurns, al.config.PreserveRecentTokens)
	if err != nil {
		if al.log != nil {
			al.log.WarnLogf("[OPENCODE-COMPACT] Peer %d: Compaction failed: %v", peerID, err)
		}
		return
	}

	if al.log != nil {
		al.log.InfoLogf("[OPENCODE-COMPACT] Peer %d: %d -> %d tokens (%.1f%% reduction)",
			peerID, result.TokensBefore, result.TokensAfter,
			(float64(result.TokensBefore-result.TokensAfter)/float64(result.TokensBefore))*100)
	}

	al.applyOpenCodeCompactResult(sess, result)
}

func (al *agentLoop) applyOpenCodeCompactResult(sess *session.Session, result *compress.OpenCodeCompactResult) {
	sess.Reset()

	if result.SummaryMsg.Content != "" {
		sess.AddAssistantMessage(
			"<<CONVERSATION CHECKPOINT>>\n" + result.SummaryMsg.Content,
		)
	}

	for _, msg := range result.KeptTail {
		switch msg.Role {
		case "system":
			sess.UpdateSystemPrompt(msg.Content)
		case "user":
			sess.AddUserMessage(msg.Content)
		case "assistant":
			sess.AddAssistantMessage(msg.Content)
		case "tool":
			sess.AddUserMessage(msg.Content)
		}
	}
}

func (al *agentLoop) runPruning(sess *session.Session) {
	if !al.config.EnablePruning {
		return
	}
	history := sess.GetHistory()
	messages := al.convertHistoryToMessages(history)
	pruned := compress.PruneMessages(messages)

	if len(pruned) == len(messages) {
		return
	}

	if al.log != nil {
		prunedCount := 0
		for i, m := range pruned {
			if m.Compacted && !messages[i].Compacted {
				prunedCount++
			}
		}
		if prunedCount > 0 {
			al.log.DebugLogf("[PRUNE] Peer %d: Pruned %d tool outputs", sess.GetPeerID(), prunedCount)
		}
	}

	sess.Reset()
	for _, msg := range pruned {
		switch msg.Role {
		case "system":
			sess.UpdateSystemPrompt(msg.Content)
		case "user":
			sess.AddUserMessage(msg.Content)
		case "assistant":
			sess.AddAssistantMessage(msg.Content)
		case "tool":
			sess.AddUserMessage(msg.Content)
		}
	}
}

func (al *agentLoop) convertHistoryToMessages(history []session.Message) []tokenizers.Message {
	messages := make([]tokenizers.Message, len(history))
	for i, msg := range history {
		messages[i] = tokenizers.Message{
			Role:    string(msg.Role),
			Content: msg.Content,
		}
	}
	return messages
}

func (al *agentLoop) Start(ctx context.Context) {
	al.mu.Lock()
	al.isRunning = true
	al.mu.Unlock()

	if al.log != nil {
		al.log.InfoLog("AgentLoop started")
	}

	go func() {
		select {
		case <-ctx.Done():
			al.Stop()
		case <-al.stopCh:
		}
	}()
}

func (al *agentLoop) Stop() {
	al.mu.Lock()
	al.isRunning = false
	al.mu.Unlock()

	close(al.stopCh)

	if al.log != nil {
		al.log.InfoLog("AgentLoop stopped")
	}
}

func (al *agentLoop) ResetSession(peerID int64) {
	if val, ok := al.sessionM.Load(peerID); ok {
		sess := val.(*session.Session)
		sess.Reset()
		sess.ClearPinned()
		if al.log != nil {
			al.log.InfoLogf("Session reset for peer %d", peerID)
		}
	}
}

func (al *agentLoop) GetSession(peerID int64) *session.Session {
	if val, ok := al.sessionM.Load(peerID); ok {
		sess := val.(*session.Session)
		if al.log != nil {
			al.log.DebugLogf("GetSession: found session for peer %d, history length: %d", peerID, sess.HistoryLength())
		}
		return sess
	}
	if al.log != nil {
		al.log.DebugLogf("GetSession: no session found for peer %d", peerID)
	}
	return nil
}

func (al *agentLoop) EnsureSession(peerID int64) *session.Session {
	return al.getOrCreateSession(peerID)
}

func (al *agentLoop) SetThinkingCallback(cb func(peerID int64, content string) error) {
	al.thinkingCallback = cb
}

func (al *agentLoop) TestLlamaServer(ctx context.Context) (model string, responseTime time.Duration, tokensPerSec float64, err error) {
	_, modelName, llamaURL := al.config.ModelHolder.GetCurrent()
	result := TestLlamaServer(ctx, llamaURL, modelName)
	return result.Model, result.ResponseTime, result.TokensPerSec, result.Error
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func NewDefaultLogger(debug bool) Logger {
	return newDefaultLogger(debug)
}

type defaultLogger struct {
	debug bool
}

func newDefaultLogger(debug bool) Logger {
	return &defaultLogger{debug: debug}
}

func (l *defaultLogger) DebugLog(msg string, args ...interface{}) {
	if l.debug {
		fmt.Printf("[DEBUG] "+msg+"\n", args...)
	}
}

func (l *defaultLogger) InfoLog(msg string, args ...interface{}) {
	fmt.Printf("[INFO] "+msg+"\n", args...)
}

func (l *defaultLogger) WarnLog(msg string, args ...interface{}) {
	fmt.Printf("[WARN] "+msg+"\n", args...)
}

func (l *defaultLogger) ErrorLog(msg string, args ...interface{}) {
	fmt.Printf("[ERROR] "+msg+"\n", args...)
}

func (l *defaultLogger) DebugLogf(format string, args ...interface{}) {
	l.DebugLog(format, args...)
}

func (l *defaultLogger) InfoLogf(format string, args ...interface{}) {
	l.InfoLog(format, args...)
}

func (l *defaultLogger) WarnLogf(format string, args ...interface{}) {
	l.WarnLog(format, args...)
}

func (l *defaultLogger) ErrorLogf(format string, args ...interface{}) {
	l.ErrorLog(format, args...)
}
