package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/debug"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/instructions"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/prompt"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type FileReadTool = tools.FileReadTool
type FileWriteTool = tools.FileWriteTool
type TimeGetTool = tools.TimeGetTool
type DirListTool = tools.DirListTool
type ShellExecuteTool = tools.ShellExecuteTool
type WebFetchTool = tools.WebFetchTool
type WebSearchTool = tools.WebSearchTool
type GlobTool = tools.GlobTool
type GrepTool = tools.GrepTool
type CalcTool = tools.CalcTool
type EditTool = tools.EditTool
type ApplyPatchTool = tools.ApplyPatchTool
type QuestionTool = tools.QuestionTool

type ThinkingCallback func(peerID int64, content string) error

type CheckpointFn func(lastToolCall string)

type CheckpointSetter interface {
	SetCheckpoint(fn CheckpointFn)
}

type agentImpl struct {
	config            Config
	sessions          map[int64]*session.Session
	toolsRegistry     *tools.Registry
	mu                sync.RWMutex
	client            *http.Client
	compactor         *compress.Compactor
	systemPrompt      string
	thinkingCallback  ThinkingCallback
	toolSchemas       []map[string]interface{}
	toolExecutor      ToolExecutor
	debugLog          debug.Logger
	permissionChecker PermissionChecker
	responseLoops     map[int64]*responseLoopState
	checkpointFn      CheckpointFn
}

type PermissionChecker interface {
	Check(toolName string) string
	Evaluate(permission, pattern string) string
	Approve(permission, pattern string)
}

func NewAgent(config Config) *agentImpl {
	agent := &agentImpl{
		config:       config,
		sessions:     make(map[int64]*session.Session),
		toolsRegistry: tools.NewRegistry(),
		client: &http.Client{
			Timeout: 2 * time.Hour,
			Transport: &http.Transport{
				DisableKeepAlives: true,
			},
		},
		debugLog:      debug.NewLogger(config.Debug),
		responseLoops: make(map[int64]*responseLoopState),
	}

	agent.loadSystemPrompt()

	if config.EnableTools {
		agent.registerDefaultTools()
	}

	if config.EnableCompression {
		agent.initCompactor()
	}

	return agent
}

func (a *agentImpl) loadSystemPrompt() {
	defaultPrompt := "You are a helpful assistant."

	if a.config.PromptsDir != "" {
		a.loadFromTemplates()
		if a.systemPrompt != "" {
			return
		}
	}

	if a.config.SystemPromptFile != "" {
		data, err := os.ReadFile(a.config.SystemPromptFile)
		if err == nil && strings.TrimSpace(string(data)) != "" {
			a.systemPrompt = strings.TrimSpace(string(data))
			a.debugLog.Info("Loaded system prompt from '%s' (%d bytes)",
				a.config.SystemPromptFile, len(a.systemPrompt))
			return
		}
	}

	a.systemPrompt = defaultPrompt
}

func (a *agentImpl) loadFromTemplates() {
	engine := prompt.NewEngine(a.config.PromptsDir)

	provider := prompt.DetectProvider(a.config.Model)
	toolNames := a.config.ToolsList
	if toolNames == nil {
		toolNames = extractToolNames(a.toolsRegistry.GetAll())
	}

	result, err := engine.Resolve(prompt.Config{
		Model:       a.config.Model,
		Provider:    provider,
		WorkingDir:  tools.WorkingDir,
		Tools:       toolNames,
		MaxTokens:   a.config.MaxTokens,
		Temperature: a.config.Temperature,
		Mode:        a.config.Mode,
	})
	if err != nil {
		a.debugLog.Warn("Template resolution failed: %v, falling back", err)
		return
	}

	a.systemPrompt = result
	a.debugLog.Info("Loaded system prompt from templates (%s, %d chars)",
		provider, len(result))
}

func extractToolNames(toolList []tools.Tool) []string {
	if toolList == nil {
		return nil
	}
	names := make([]string, 0, len(toolList))
	for _, t := range toolList {
		names = append(names, t.Name())
	}
	return names
}

func (a *agentImpl) GetSystemPrompt() string {
	return a.systemPrompt
}

func (a *agentImpl) initCompactor() {
	compressor := compress.NewLLMCompressor(a.config.LlamaServerURL, a.config.Model, a.config.Temperature)
	a.compactor = compress.NewCompactor(compressor)
}

func (a *agentImpl) registerDefaultTools() {
	a.toolsRegistry.Register(&FileReadTool{})
	a.toolsRegistry.Register(&FileWriteTool{})
	a.toolsRegistry.Register(&TimeGetTool{})
	a.toolsRegistry.Register(&DirListTool{})
	a.toolsRegistry.Register(&ShellExecuteTool{})
	a.toolsRegistry.Register(&WebFetchTool{})
	a.toolsRegistry.Register(&WebSearchTool{})
	a.toolsRegistry.Register(&GlobTool{})
	a.toolsRegistry.Register(&GrepTool{})
	a.toolsRegistry.Register(&CalcTool{})
	a.toolsRegistry.Register(&EditTool{})
	a.toolsRegistry.Register(&ApplyPatchTool{})
	a.toolsRegistry.Register(&QuestionTool{})
}

func (a *agentImpl) RegisterTools(registry *tools.Registry) {
	if registry == nil {
		return
	}
	for _, tool := range registry.GetAll() {
		if !a.toolsRegistry.IsRegistered(tool.Name()) {
			a.toolsRegistry.Register(tool)
		}
	}

	a.toolSchemas = a.toolsRegistry.ToOpenAISchema()
}

func (a *agentImpl) ReplaceTools(registry *tools.Registry) {
	if registry == nil {
		return
	}
	a.toolsRegistry = registry
	a.toolSchemas = registry.ToOpenAISchema()
}

func (a *agentImpl) ProcessMessage(ctx context.Context, message string, peerID int64) (string, error) {
	a.debugLog.Debug("ProcessMessage called: peerID=%d, message=%q, tools=%d", peerID, message, len(a.toolsRegistry.GetAll()))

	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	s := a.getSession(peerID)

	if s.IsLoopDetected() {
		alert := s.GetLoopAlertMessage()
		if alert != "" {
			message = "[LOOP DETECTED] " + alert + "\n\n" + message
		}
	}

	history := s.GetHistory()
	if len(history) == 0 || history[len(history)-1].Role != session.UserRole || history[len(history)-1].Content != message {
		s.AddUserMessage(message)
		a.resetResponseLoop(peerID)
		history = s.GetHistory()
	}

	a.promoteSteers(ctx, s)

	if a.compactor != nil {
		a.compactIfNeeded(ctx, s, true)
		history = s.GetHistory()
	}

	apiMessages := a.convertHistoryToAPIMessages(s.GetContextMessages())

	workingDir := s.GetWorkingDir()
	if workingDir == "" {
		workingDir = tools.WorkingDir
	}
	apiMessages = a.injectInstructions(apiMessages, workingDir)

	if err := ctx.Err(); err != nil {
		return "", err
	}

	if a.config.EnableTools {

		result, err := a.processWithTools(ctx, apiMessages, s)
		if err != nil {
			return "", fmt.Errorf("process with tools: %w", err)
		}
		return result.Response, nil
	}

	return a.processStreaming(ctx, apiMessages, s)
}

func (a *agentImpl) promoteSteers(ctx context.Context, s *session.Session) bool {
	if ctx.Err() != nil || s == nil {
		return false
	}
	in := s.GetPeerInput()
	if in == nil {
		return false
	}
	msgs := in.Drain()
	if len(msgs) == 0 {
		return false
	}
	for _, m := range msgs {
		s.AddUserMessage(m)
		a.debugLog.Debug("promoted mid-turn user message into session: %q", m)
	}
	return true
}

func (a *agentImpl) compactIfNeeded(ctx context.Context, s *session.Session, addAutoContinue bool) bool {
	history := s.GetHistory()

	visible := a.convertSessionHistory(history)
	tokensBefore := compress.EstimateMessagesTokensSimple(visible)
	if !compress.IsOverflowWithLimits(tokensBefore, a.config.MaxTokens, a.config.ModelLimitInput, a.config.CompactionReserved) {
		return false
	}

	tailTurns := a.config.TailTurns
	if tailTurns <= 0 {
		tailTurns = 2
	}

	result, err := a.compactor.CompactWithOpenCode(ctx, a.convertSessionHistoryRaw(history), a.config.MaxTokens, tailTurns, a.config.PreserveRecentTokens)
	if err != nil {
		a.debugLog.Warn("LLM compaction failed: %v, falling back to aggressive head pruning", err)

		fbResult := a.compactionFallback(history, tailTurns, a.config.MaxTokens)
		if fbResult != nil {
			a.markCompactedHead(s, fbResult.TailStartID)
			s.MarkCompaction(fbResult.TailStartID, compactionFallbackSummary)
		}
		return false
	}

	if result.Summary == "" {
		return false
	}

	s.MarkCompaction(result.TailStartID, result.Summary)

	if addAutoContinue && shouldAddAutoContinue(s) {
		s.AddUserMessage(tokenizers.CompactionAutoContinueText)
	}

	return true
}

func (a *agentImpl) markCompactedHead(s *session.Session, tailStartID int) {
	for i := 0; i < tailStartID && i < len(s.GetHistory()); i++ {
		msg := s.GetHistory()[i]
		if msg.Role != session.SystemRole {
			s.MarkMessageCompacted(i, compress.PRUNED_OUTPUT_PLACEHOLDER)
		}
	}
	a.debugLog.Info("Compaction fallback: marked %d head messages as compacted", tailStartID)
}

const compactionFallbackSummary = "## Goal\n- [context compacted — summary unavailable]\n\n## Constraints & Preferences\n- (none)\n\n## Progress\n### Done\n- (compact failed)\n\n### In Progress\n- (truncated)\n\n### Blocked\n- context overflow during summarization\n\n## Key Decisions\n- (lost during compaction fallback)\n\n## Next Steps\n- continue current task\n\n## Critical Context\n- [compaction summary could not be generated]\n\n## Relevant Files\n- (none)"

func (a *agentImpl) convertSessionHistory(history []session.Message) []tokenizers.Message {
	return compress.FilterCompacted(a.convertSessionHistoryRaw(history))
}

func (a *agentImpl) convertSessionHistoryRaw(history []session.Message) []tokenizers.Message {
	messages := make([]tokenizers.Message, len(history))
	for i, msg := range history {
		content := msg.Content

		for _, tc := range msg.ToolCalls {
			content += tc.Function.Arguments
		}
		messages[i] = tokenizers.Message{
			Role:        string(msg.Role),
			Content:     content,
			Summary:     msg.Summary,
			Compacted:   msg.Compacted,
			TailStartID: msg.TailStartID,
		}
	}
	return messages
}

func (a *agentImpl) ResetSession(peerID int64) {
	s := a.getSession(peerID)
	s.Reset()
}

func (a *agentImpl) compactionFallback(history []session.Message, tailTurns int, maxTokens int) *compress.SelectResult {
	if len(history) == 0 {
		return nil
	}

	raw := a.convertSessionHistoryRaw(history)
	budget := compress.PreserveRecentBudget(maxTokens, a.config.PreserveRecentTokens)
	selected := compress.SelectMessages(raw, tailTurns, budget)

	if selected.TailStartID <= 0 || len(selected.Head) == 0 {
		return nil
	}

	return &selected
}

func (a *agentImpl) GetSession(peerID int64) *session.Session {
	return a.getSession(peerID)
}

func (a *agentImpl) SetThinkingCallback(cb ThinkingCallback) {
	a.thinkingCallback = cb
}

func (a *agentImpl) SetTools(toolSchemas []map[string]interface{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolSchemas = toolSchemas
}

func (a *agentImpl) SetToolExecutor(executor ToolExecutor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolExecutor = executor
}

func (a *agentImpl) SetPermissionChecker(checker PermissionChecker) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.permissionChecker = checker
}

func (a *agentImpl) SetCheckpoint(fn CheckpointFn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checkpointFn = fn
}

func (a *agentImpl) fireCheckpoint(roundLabel string) {
	a.mu.RLock()
	fn := a.checkpointFn
	a.mu.RUnlock()
	if fn == nil {
		return
	}
	fn(roundLabel)
}

func (a *agentImpl) getSession(peerID int64) *session.Session {
	a.mu.RLock()
	s, exists := a.sessions[peerID]
	a.mu.RUnlock()

	if !exists {
		a.mu.Lock()

		s, exists = a.sessions[peerID]
		if !exists {
			config := a.config.SessionConfig
			config.PeerID = peerID
			config.SystemPrompt = a.systemPrompt
			s = session.NewSession(config)
			a.sessions[peerID] = s

			if wd := s.GetWorkingDir(); wd != "" {
				tools.SetWorkingDir(wd)
			}
		}
		a.mu.Unlock()
	}

	return s
}

func (a *agentImpl) processStreaming(ctx context.Context, messages []Message, session *session.Session) (string, error) {

	if a.promoteSteers(ctx, session) {
		messages = a.convertHistoryToAPIMessages(session.GetContextMessages())
	}

	streamConfig := StreamingConfig{
		Model:       a.config.Model,
		MaxTokens:   a.config.MaxTokens,
		Temperature: a.config.Temperature,
		Stream:      true,
	}

	responseText, reasoningText, _, _, promptTokens, completionTokens, err := a.streamAndCollect(ctx, streamConfig, messages)
	if err != nil {
		return "", err
	}

	loopRepeats := a.checkResponseLoop(session.GetPeerID(), responseText, reasoningText, nil)
	if loopRepeats > 0 {
		a.injectLoopCorrection(session, loopRepeats)
	}

	if reasoningText != "" {
		parsed := ParseXMLToolCalls(reasoningText)
		if len(parsed.ToolCalls) > 0 {

			result, err := a.processWithTools(ctx, messages, session)
			if err != nil {
				return "", err
			}
			return result.Response, nil
		}
	}

	if reasoningText != "" && a.thinkingCallback != nil {
		cleanedReasoning := reasoningText
		parsed := ParseXMLToolCalls(reasoningText)
		if len(parsed.ToolCalls) > 0 {
			cleanedReasoning = parsed.Content
		}
		if cleanedReasoning != "" {
			if err := a.thinkingCallback(session.GetPeerID(), cleanedReasoning); err != nil {
				a.debugLog.Warn("Failed to send thinking message: %v", err)
			}
		}
	}

	a.sendThinkingTokens(session.GetPeerID(), promptTokens, completionTokens)

	if responseText == "" && reasoningText != "" {
		return "", nil
	}

	responseText = a.stripThinkingTags(responseText, session.GetPeerID())

	session.AddAssistantMessage(responseText)
	return responseText, nil
}

func (a *agentImpl) injectInstructions(messages []Message, workingDir string) []Message {
	content := instructions.Build(workingDir)
	if content == "" {
		return messages
	}

	out := make([]Message, len(messages))
	copy(out, messages)
	for i := range out {
		if out[i].Role == "system" {
			out[i].Content += "\n\n" + content
			return out
		}
	}

	return append([]Message{{Role: "system", Content: content}}, out...)
}

func (a *agentImpl) convertHistoryToAPIMessages(history []session.Message) []Message {
	apiMessages := make([]Message, len(history))
	for i, msg := range history {
		content := msg.Content
		if msg.Role == session.ToolRole {
			content = compress.TruncateToolOutput(content)
		}
		apiMsg := Message{
			Role:       string(msg.Role),
			Content:    content,
			ToolCallID: msg.ToolCallID,
			Name:       msg.Name,
		}

		if len(msg.ToolCalls) > 0 {
			apiMsg.ToolCalls = make([]ToolCall, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				apiMsg.ToolCalls[j] = ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: json.RawMessage(tc.Function.Arguments),
					},
				}
			}
		}
		apiMessages[i] = apiMsg
	}
	return apiMessages
}
