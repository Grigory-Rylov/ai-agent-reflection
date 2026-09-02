package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agent"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentpolicy"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/util/stringutil"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type AgentLoop interface {
	ProcessPrompt(ctx context.Context, prompt string, peerID int64) (string, error)
	ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error)

	ProcessPromptWithSystemPrompt(ctx context.Context, prompt string, peerID int64, systemPrompt string) (string, error)
	Start(ctx context.Context)
	Stop()
	ResetSession(peerID int64)

	ClearPeerSession(peerID int64)

	GetSessionConfig(peerID int64) (session.Config, bool)
	GetSession(peerID int64) *session.Session
	EnsureSession(peerID int64) *session.Session

	ResumeInterruptedTask(ctx context.Context, peerID int64)

	ClearAllSlots(ctx context.Context)
	SetThinkingCallback(cb func(peerID int64, content string) error)
	GetContextStats(peerID int64) (charCount int, tokenCount int, err error)
	TestLlamaServer(ctx context.Context) (model string, responseTime time.Duration, tokensPerSec float64, err error)
	GetModelHolder() *modelsconfig.Holder
	GetSlotManager() *SlotManager
	GetSlots() *SlotClient

	GetStore() store.Store
}

type agentLoop struct {
	config           LoopConfig
	sessionM         sync.Map
	sessionCreateMu  sync.Mutex
	vk               VKClient
	registry         ToolRegistry
	compactor        compress.CompactorInterface
	tokenizer        tokenizers.Tokenizer
	dispatcher       *EventDispatcher
	stopCh           chan struct{}
	isRunning        bool
	mu               sync.Mutex
	log              Logger
	aiHistory        []string
	historyMu        sync.Mutex
	thinkingCallback func(peerID int64, content string) error
	currentAlias     string
	modelMu          sync.Mutex
	engineGate       *engineGate
	watchdogCancel   context.CancelFunc
	shutdownMu       sync.Mutex
	slots            *SlotClient
	slotMgr          *SlotManager
	specMu           sync.Mutex
	speculative      map[int64]*speculativeCompact
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

	var tokenizer *tokenizers.LlamaServerTokenizer
	if config.ContextResolver != nil {
		ctx, err := config.ContextResolver.Resolve()
		if err != nil {
			if l != nil {
				l.WarnLogf("Could not resolve model context for %s: %v (using fallback maxTokens=%d)", alias, err, config.MaxTokens)
			}
		} else {
			config.MaxTokens = ctx
			if l != nil {
				l.InfoLogf("Using model context for %s: %d tokens", alias, ctx)
			}
		}
		tokenizer = tokenizers.NewLlamaServerTokenizer(llamaURL, modelName, config.MaxTokens)
		if config.EnableLogging {
			tokenizer.SetDebug(true)
		}
	} else {
		modelCtx := config.ModelHolder.GetModelContext(alias)
		config.MaxTokens = resolveMaxTokens(config.ModelHolder, alias, config.MaxTokens)
		tokenizer = tokenizers.NewLlamaServerTokenizer(llamaURL, modelName, config.MaxTokens)
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
	}

	llmCompressor := compress.NewLLMCompressor(llamaURL, modelName, config.Temperature)
	compactor := compress.NewCompactor(llmCompressor)

	if l != nil {
		l.InfoLogf("AgentLoop initialized: model=%s host=%s maxTokens=%d", modelName, llamaURL, config.MaxTokens)
	}

	slotMgr := NewSlotManager(newSlotClient())
	slotMgr.SetLogger(l)

	return &agentLoop{
		config:       config,
		vk:           vk,
		registry:     registry,
		compactor:    compactor,
		tokenizer:    tokenizer,
		stopCh:       make(chan struct{}),
		dispatcher:   NewEventDispatcher(),
		log:          l,
		currentAlias: alias,
		engineGate:   newEngineGate(),
		slots:        newSlotClient(),
		slotMgr:      slotMgr,
		speculative:  map[int64]*speculativeCompact{},
	}, nil
}

func (al *agentLoop) GetModelHolder() *modelsconfig.Holder {
	return al.config.ModelHolder
}

func (al *agentLoop) GetSlotManager() *SlotManager {
	return al.slotMgr
}

func (al *agentLoop) GetSlots() *SlotClient {
	return al.slots
}

func (al *agentLoop) GetStore() store.Store {
	return al.config.SessionConfig.Store
}

func resolveMaxTokens(h *modelsconfig.Holder, alias string, fallback int) int {
	if h == nil {
		return fallback
	}
	if ctx := h.GetModelContext(alias); ctx > 0 {
		return ctx
	}
	return fallback
}

func (al *agentLoop) syncCurrentModel(ctx context.Context, peerID int64) error {
	al.modelMu.Lock()
	if al.config.ModelHolder == nil {
		al.modelMu.Unlock()
		return nil
	}
	alias, _, _ := al.config.ModelHolder.GetCurrent()
	if alias == al.currentAlias {
		al.modelMu.Unlock()
		return nil
	}
	needEngine := al.engineDecisionNeeded(alias)
	al.currentAlias = alias
	al.modelMu.Unlock()

	if needEngine {
		if err := al.awaitEngineTransition(ctx, alias, peerID); err != nil {
			return err
		}
	}

	al.refreshModelResources(alias)
	return nil
}

func (al *agentLoop) engineDecisionNeeded(alias string) bool {
	if al.config.Engine == nil {
		return false
	}
	return al.config.ModelHolder.GetModelStartScript(alias) != ""
}

func (al *agentLoop) awaitEngineTransition(ctx context.Context, alias string, peerID int64) error {
	notify := func(status string) {
		al.notifyEngineStatus(ctx, peerID, status)
	}
	return al.engineGate.AwaitTransition(ctx, alias, func(runCtx context.Context) error {
		return al.config.Engine.Transition(runCtx, alias, notify)
	})
}

func (al *agentLoop) notifyEngineStatus(_ context.Context, peerID int64, status string) {
	if al.log != nil {
		al.log.InfoLogf("[ENGINE] %s", status)
	}
	text, toUser := engineStatusMessage(status)
	if !toUser {
		return
	}
	if al.vk == nil {
		return
	}
	target := peerID
	if target <= 0 {
		target = al.config.ThinkingPeerID
	}
	if target <= 0 {
		return
	}
	if _, err := al.vk.SendMessage(target, text); err != nil && al.log != nil {
		al.log.WarnLogf("[ENGINE] failed to deliver status to peer %d: %v", target, err)
	}
}

func engineStatusMessage(status string) (string, bool) {
	switch {
	case strings.Contains(status, "Engine ready"):
		return "✅ Движок модели готов, продолжаю работу.", true
	case strings.Contains(status, "failed") || strings.Contains(status, "failure"):
		return "⚠️ Автоматический перезапуск движка не удался — возможно, нужен ручной вход.", true
	default:
		return "", false
	}
}

func (al *agentLoop) refreshModelResources(alias string) {
	al.modelMu.Lock()
	defer al.modelMu.Unlock()
	if alias != al.currentAlias {
		return
	}
	_, modelName, llamaURL := al.config.ModelHolder.GetCurrent()
	if llamaURL == "" {
		llamaURL = "http://127.0.0.1:8081"
	}
	if modelName == "" {
		modelName = "local-model"
	}

	if al.config.ContextResolver != nil {
		resolvedCtx, err := al.config.ContextResolver.Resolve()
		if err != nil {
			if al.log != nil {
				al.log.WarnLogf("Could not resolve model context for %s: %v (keeping maxTokens=%d)", alias, err, al.config.MaxTokens)
			}
		} else {
			al.config.MaxTokens = resolvedCtx
		}
	} else {
		al.config.MaxTokens = resolveMaxTokens(al.config.ModelHolder, alias, al.config.MaxTokens)
	}

	tok := tokenizers.NewLlamaServerTokenizer(llamaURL, modelName, al.config.MaxTokens)
	if al.config.EnableLogging {
		tok.SetDebug(al.config.Debug)
	}
	al.tokenizer = tok
	al.compactor = compress.NewCompactor(compress.NewLLMCompressor(llamaURL, modelName, al.config.Temperature))

	if al.log != nil {
		al.log.InfoLogf("Model switched: %s (%s) at %s, maxTokens=%d", alias, modelName, llamaURL, al.config.MaxTokens)
	}

	al.syncVisionTool()
}

func (al *agentLoop) syncVisionTool() bool {
	if al.config.ModelHolder == nil {
		return false
	}

	reg, ok := al.registry.(*tools.Registry)
	if !ok {
		return false
	}

	vision := al.config.ModelHolder.GetCurrentVision()
	if vision && !reg.IsRegistered("image2text") {
		reg.Register(&tools.Image2TextTool{})
		reg.Register(&tools.Video2TextTool{})
		if al.log != nil {
			al.log.InfoLogf("image2text and video2text tools registered (vision model)")
		}
		return true
	}
	if !vision && reg.IsRegistered("image2text") {
		reg.Unregister("image2text")
		reg.Unregister("video2text")
		if al.log != nil {
			al.log.InfoLogf("image2text and video2text tools unregistered (model is not vision-capable)")
		}
		return false
	}
	return vision
}

func (al *agentLoop) currentModelSlotSave() bool {
	if al.config.ModelHolder == nil {
		return false
	}
	return al.config.ModelHolder.GetCurrentSlotSave()
}

func (al *agentLoop) restoreSlotInto(ctx context.Context, host, modelName string, slotID int, sessionID string) {
	filename := SlotFileName(modelName, slotID)
	err := al.slots.restoreSlot(ctx, host, slotID, modelName, filename)
	if err == nil {
		if al.log != nil {
			al.log.InfoLogf("[SLOT] restore slot %d for session %s (%s)", slotID, sessionID, filename)
		}
		return
	}
	switch {
	case IsSlotConfigError(err):
		if al.slotMgr.MarkUnavailable(host) && al.slotMgr.ShouldLogUnavailable(host) && al.log != nil {
			al.log.InfoLogf("[SLOT] KV-cache save/restore unavailable on %s, skipping slot feature: %v", host, err)
		}
	case IsSlotMissingFileError(err):
		if al.log != nil {
			al.log.DebugLogf("[SLOT] restore slot %d for session %s: no cache file yet (cold start)", slotID, sessionID)
		}
	default:
		if al.log != nil {
			al.log.WarnLogf("[SLOT] restore slot %d for session %s: %v", slotID, sessionID, err)
		}
	}
}

func (al *agentLoop) saveSlotFrom(ctx context.Context, host, modelName string, slotID int, sessionID string) {
	filename := SlotFileName(modelName, slotID)
	err := al.slots.saveSlot(ctx, host, slotID, modelName, filename)
	if err == nil {
		if al.log != nil {
			al.log.InfoLogf("[SLOT] save slot %d for session %s (%s)", slotID, sessionID, filename)
		}
		return
	}
	if IsSlotConfigError(err) {
		if al.slotMgr.MarkUnavailable(host) && al.slotMgr.ShouldLogUnavailable(host) && al.log != nil {
			al.log.InfoLogf("[SLOT] KV-cache save/restore unavailable on %s, skipping slot feature: %v", host, err)
		}
		return
	}
	if al.log != nil {
		al.log.WarnLogf("[SLOT] save slot %d for session %s: %v", slotID, sessionID, err)
	}
}

func sanitizeSlotName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "model"
	}
	return b.String()
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

func (al *agentLoop) ProcessPromptWithSystemPrompt(ctx context.Context, prompt string, peerID int64, systemPrompt string) (string, error) {
	sess := al.getOrCreateSession(peerID)
	original := sess.GetSystemPrompt()
	logger.DebugToFile("[#lead] ProcessPromptWithSystemPrompt: systemPrompt=%d chars, original=%d chars", len(systemPrompt), len(original))
	if strings.TrimSpace(systemPrompt) != "" {
		sess.UpdateSystemPrompt(strings.TrimSpace(systemPrompt))
		defer sess.UpdateSystemPrompt(original)
	}
	return al.ProcessPrompt(ctx, prompt, peerID)
}

func (al *agentLoop) ProcessPrompt(ctx context.Context, prompt string, peerID int64) (string, error) {
	if err := al.syncCurrentModel(ctx, peerID); err != nil {
		return "", err
	}
	sess := al.getOrCreateSession(peerID)

	sess.SetResumePrompt(prompt)
	defer sess.SetResumePrompt("")

	if al.log != nil {
		al.log.InfoLogf("Prompt received from peer %d: %s", peerID, stringutil.Truncate(prompt, 100, "..."))
	}

	al.dispatcher.Emit(NewEvent(EventPromptReceived, peerID))

	sess.AddUserMessage(prompt)

	if al.config.EnableCompression {
		al.checkAndCompressOpenCode(ctx, sess, peerID)
		al.maybeStartSpeculativeCompact(ctx, sess, peerID)
	}

	messages := al.buildAPIMessages(sess)

	slotSave := al.currentModelSlotSave()
	sessionID := sess.GetSessionID()
	assignedSlotID := -1

	if slotSave {
		_, modelName, host := al.config.ModelHolder.GetCurrent()
		if host != "" {
			al.slotMgr.CheckAvailability(ctx, host, modelName)
		}

		slotID, evictedSessionID := al.slotMgr.GetOrAssign(sessionID, al.slotMgr.TotalSlots())
		if al.slotMgr.IsAvailable(host) && slotID >= 0 {
			assignedSlotID = slotID

			if evictedSessionID != "" {
				al.saveSlotFrom(ctx, host, modelName, slotID, evictedSessionID)
				if err := al.slots.eraseSlot(ctx, host, slotID, modelName); err != nil {
					if al.log != nil {
						al.log.DebugLogf("[SLOT] erase slot %d after eviction: %v", slotID, err)
					}
				}
				if al.log != nil {
					al.log.InfoLogf("[SLOT] evicted session %s from slot %d, new session %s starts cold", evictedSessionID, slotID, sessionID)
				}
			} else {

				al.restoreSlotInto(ctx, host, modelName, slotID, sessionID)
			}
		}
	}

	response, err := al.sendToLLM(ctx, messages, sess, peerID, prompt, assignedSlotID)
	if err != nil {
		if al.log != nil {
			al.log.ErrorLogf("LLM request failed: %v", err)
		}
		return "", fmt.Errorf("LLM request failed: %w", err)
	}

	if slotSave {

		al.slotMgr.Touch(sessionID)
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
		al.log.InfoLogf("Thinking sent to peer %d: %s", al.config.ThinkingPeerID, stringutil.Truncate(content, 80, "..."))
	}
}

func (al *agentLoop) getOrCreateSession(peerID int64) *session.Session {
	if val, ok := al.sessionM.Load(peerID); ok {
		return val.(*session.Session)
	}

	al.sessionCreateMu.Lock()
	defer al.sessionCreateMu.Unlock()
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
		content := msg.Content
		if msg.Role == session.ToolRole {
			content = compress.TruncateToolOutput(content)
		}
		messages[i] = agent.Message{
			Role:    string(msg.Role),
			Content: content,
		}
	}

	return messages
}

func (al *agentLoop) sendToLLM(ctx context.Context, messages []agent.Message, sess *session.Session, peerID int64, prompt string, slotID int) (string, error) {
	if al.log != nil {
		al.log.DebugLog("[sendToLLM] creating agent")
	}

	agentConfig := al.buildAgentConfig()
	agentConfig.SessionConfig.Store = nil
	agentConfig.SessionConfig.SessionFile = ""

	if wd := sess.GetWorkingDir(); wd != "" {
		tools.SetWorkingDir(wd)
		agentConfig.SessionConfig.WorkingDir = wd
	}

	agentConfig.SlotID = slotID
	if slotID >= 0 && al.currentModelSlotSave() {
		agentConfig.SlotSave = true
		agentConfig.SlotSaver = NewSlotSaver(al.slotMgr, al.slots, al.config.ModelHolder, sess.GetSessionID(), al.log)
	}
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

	if al.config.EnableTools && al.registry != nil {
		al.registerToolsToAgent(a, al.registry)
	}

	agentSess := a.GetSession(peerID)

	agentSess.SetPeerInput(sess.GetPeerInput())

	if sp := sess.GetSystemPrompt(); strings.TrimSpace(sp) != "" {
		logger.DebugToFile("[sendToLLM] applying session prompt (%d chars) to agent", len(sp))
		agentSess.UpdateSystemPrompt(sp)
	}
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

	seededLen := len(agentSess.GetHistory())

	response, err := a.ProcessMessage(ctx, prompt, peerID)

	if err != nil && (errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled")) {
		return "", err
	}

	al.mirrorAgentSession(sess, agentSess, seededLen)

	if in := sess.GetPeerInput(); in != nil {
		for _, m := range in.TakePromoted() {
			sess.AddUserMessage(m)
		}
	}

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

	return response, nil
}

func (al *agentLoop) mirrorAgentSession(sess *session.Session, agentSess *session.Session, seededLen int) {
	history := agentSess.GetHistory()
	for i := seededLen; i < len(history); i++ {
		m := history[i]
		switch m.Role {
		case session.UserRole:
			sess.AddUserMessage(m.Content)
		case session.AssistantRole:
			if len(m.ToolCalls) > 0 {
				sess.AddAssistantMessageWithToolCalls(m.Content, m.ToolCalls)
			} else if m.Content != "" || i == len(history)-1 {
				sess.AddAssistantMessage(m.Content)
			}
		case session.ToolRole:
			sess.AddToolMessage(m.ToolCallID, m.Name, m.Content)
		}
	}
}

func (al *agentLoop) buildAgentConfig() agent.Config {
	_, modelName, llamaURL := al.config.ModelHolder.GetCurrent()
	cfg := agent.Config{
		LlamaServerURL:                 llamaURL,
		EngineType:                     al.config.ModelHolder.GetCurrentEngineType(),
		Model:                          modelName,
		MaxTokens:                      al.config.MaxTokens,
		Temperature:                    al.config.Temperature,
		StreamIdleTimeout:              al.config.StreamIdleTimeout,
		SessionConfig:                  al.config.SessionConfig,
		SystemPromptFile:               al.config.SystemPromptFile,
		EnableTools:                    al.config.EnableTools,
		ToolOutputMaxLines:             al.config.ToolOutputMaxLines,
		ToolOutputMaxBytes:             al.config.ToolOutputMaxBytes,
		Debug:                          al.config.Debug,
		SkipShellPermissionForPathless: al.config.SkipShellPermissionForPathless,
		MaxToolCallDepth:               al.config.MaxToolCallDepth,
	}

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
				result = al.truncateToolOutput(result)
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

func (al *agentLoop) truncateToolOutput(content string) string {
	var hasTask bool
	if al.registry != nil {
		_, hasTask = al.registry.Get("task")
	}
	opts := tools.TruncateOptions{
		Dir:         filepath.Join(tools.BaseDir, "tool-output"),
		MaxLines:    al.config.ToolOutputMaxLines,
		MaxBytes:    al.config.ToolOutputMaxBytes,
		HasTaskTool: hasTask,
	}

	res, err := tools.TruncateToolResult(content, opts)
	if err != nil {
		if al.log != nil {
			al.log.ErrorLogf("tool output truncation failed: %v", err)
		}
		return content
	}
	if res.Truncated && al.log != nil {
		al.log.InfoLogf("Tool output truncated, full output saved to %s", res.OutputPath)
	}
	return res.Content
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

	visible := al.convertHistoryToMessages(history)
	tokensBefore := compress.EstimateMessagesTokensSimple(visible)

	if al.log != nil {
		al.log.DebugLogf("[OPENCODE-COMPACT] Peer %d: %d messages, ~%d tokens",
			peerID, len(visible), tokensBefore)
	}

	if !compress.IsOverflowWithLimits(tokensBefore, al.config.MaxTokens, al.config.ModelLimitInput, al.config.CompactionReserved) {
		if al.log != nil {
			al.log.DebugLogf("[OPENCODE-COMPACT] Peer %d: No overflow, skipping", peerID)
		}
		return
	}

	if al.tryApplySpeculativeCompact(sess, peerID) {
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

	result, err := al.compactor.CompactWithOpenCode(ctx, al.convertHistoryToRawMessages(history), al.config.MaxTokens, tailTurns, al.config.PreserveRecentTokens)
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

	if result.Summary != "" {
		sess.AddUserMessage(tokenizers.CompactionAutoContinueText)
	}

	sessionID := sess.GetSessionID()
	al.invalidateSessionSlot(ctx, sessionID)
}

func (al *agentLoop) invalidateSessionSlot(ctx context.Context, sessionID string) {
	if al.config.ModelHolder == nil {
		return
	}
	_, modelName, host := al.config.ModelHolder.GetCurrent()
	if host == "" {
		return
	}
	slotID := al.slotMgr.GetSlotID(sessionID)
	if slotID < 0 {

		return
	}
	if err := al.slots.eraseSlot(ctx, host, slotID, modelName); err != nil {
		if al.log != nil {
			al.log.DebugLogf("[SLOT] invalidate: erase slot %d: %v", slotID, err)
		}
	} else if al.log != nil {
		al.log.InfoLogf("[SLOT] invalidate: erased slot %d (%s) for session %s", slotID, SlotFileName(modelName, slotID), sessionID)
	}
	al.slotMgr.Release(sessionID)
	if al.log != nil {
		al.log.InfoLogf("[SLOT] invalidated slot %d for session %s", slotID, sessionID)
	}
}

func (al *agentLoop) applyOpenCodeCompactResult(sess *session.Session, result *compress.OpenCodeCompactResult) {

	if result.Summary == "" {
		return
	}
	sess.MarkCompaction(result.TailStartID, result.Summary)
}

func (al *agentLoop) runPruning(sess *session.Session) {
	if !al.config.EnablePruning {
		return
	}
	history := sess.GetHistory()

	raw := al.convertHistoryToRawMessages(history)
	pruned := compress.PruneMessages(raw, compress.PRUNE_PROTECTED_TOOLS...)

	prunedCount := 0
	for i, m := range pruned {
		if m.Compacted && !raw[i].Compacted {
			prunedCount++
		}
	}
	if prunedCount == 0 {
		return
	}

	if al.log != nil {
		al.log.DebugLogf("[PRUNE] Peer %d: Pruned %d tool outputs", sess.GetPeerID(), prunedCount)
	}

	for i := range pruned {
		if pruned[i].Compacted && !raw[i].Compacted {
			sess.MarkMessageCompacted(i, compress.PRUNED_OUTPUT_PLACEHOLDER)
		}
	}
}

func (al *agentLoop) convertHistoryToMessages(history []session.Message) []tokenizers.Message {
	return compress.FilterCompacted(al.convertHistoryToRawMessages(history))
}

func (al *agentLoop) convertHistoryToRawMessages(history []session.Message) []tokenizers.Message {
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

func (al *agentLoop) Start(ctx context.Context) {
	al.mu.Lock()
	al.isRunning = true
	al.mu.Unlock()

	if al.log != nil {
		al.log.InfoLog("AgentLoop started")
	}

	al.startEngineWatchdog(ctx)

	go func() {
		select {
		case <-ctx.Done():
			al.Stop()
		case <-al.stopCh:
		}
	}()
}

func (al *agentLoop) startEngineWatchdog(ctx context.Context) {
	if al.config.Engine == nil {
		return
	}
	al.shutdownMu.Lock()
	defer al.shutdownMu.Unlock()
	if al.watchdogCancel != nil {
		return
	}
	notify := func(status string) {
		peerID := al.config.ThinkingPeerID
		al.notifyEngineStatus(ctx, peerID, status)
	}
	al.watchdogCancel = al.config.Engine.StartWatchdog(ctx, notify)
}

func (al *agentLoop) Stop() {
	al.mu.Lock()
	al.isRunning = false
	al.mu.Unlock()

	close(al.stopCh)

	al.stopEngineWatchdog()

	if al.log != nil {
		al.log.InfoLog("AgentLoop stopped")
	}
}

func (al *agentLoop) stopEngineWatchdog() {
	al.shutdownMu.Lock()
	defer al.shutdownMu.Unlock()
	if al.watchdogCancel != nil {
		al.watchdogCancel()
		al.watchdogCancel = nil
	}
}

func (al *agentLoop) deleteSessionSlot(ctx context.Context, peerID int64, sessionID string) {
	_ = peerID
	al.invalidateSessionSlot(ctx, sessionID)
}

func (al *agentLoop) ClearAllSlots(ctx context.Context) {
	if al.config.ModelHolder == nil {
		return
	}
	_, modelName, host := al.config.ModelHolder.GetCurrent()
	if host == "" {
		return
	}
	if !al.currentModelSlotSave() {
		return
	}
	al.slotMgr.CheckAvailability(ctx, host, modelName)
	if !al.slotMgr.IsAvailable(host) {
		return
	}
	total := al.slotMgr.TotalSlots()
	if total <= 0 {
		return
	}
	al.slots.ClearAllSlots(ctx, host, modelName, total, al.log)
	if al.log != nil {
		al.log.InfoLogf("[SLOT] startup reset: cleared %d slots for model %s", total, modelName)
	}
}

func (al *agentLoop) ResetSession(peerID int64) {
	al.cancelSpeculative(peerID)
	if val, ok := al.sessionM.Load(peerID); ok {
		sess := val.(*session.Session)
		al.deleteSessionSlot(context.Background(), peerID, sess.GetSessionID())
		sess.Reset()
		sess.ClearPinned()
		if al.log != nil {
			al.log.InfoLogf("Session reset for peer %d", peerID)
		}
	}
}

func (al *agentLoop) ClearPeerSession(peerID int64) {
	preservedWD := ""
	if val, ok := al.sessionM.Load(peerID); ok {
		preservedWD = val.(*session.Session).GetWorkingDir()
	}
	al.ResetSession(peerID)
	if err := al.clearPeerStore(peerID, preservedWD); err != nil && al.log != nil {
		al.log.WarnLogf("ClearPeerSession: clear store for peer %d: %v", peerID, err)
	}
}

func (al *agentLoop) clearPeerStore(peerID int64, preservedWD string) error {
	st := al.config.SessionConfig.Store
	if st == nil {
		return nil
	}
	if err := st.ClearMessages(peerID); err != nil {
		return err
	}
	workDir := preservedWD
	if workDir == "" {
		workDir = al.config.SessionConfig.WorkingDir
	}
	return st.SaveSession(&store.SessionData{
		PeerID:     peerID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		WorkingDir: workDir,
	})
}

func (al *agentLoop) GetSessionConfig(peerID int64) (session.Config, bool) {
	if val, ok := al.sessionM.Load(peerID); ok {
		sess := val.(*session.Session)
		cfg := al.config.SessionConfig
		cfg.PeerID = peerID
		cfg.SystemPrompt = sess.GetSystemPrompt()
		cfg.WorkingDir = sess.GetWorkingDir()
		return cfg, true
	}
	cfg := al.config.SessionConfig
	cfg.PeerID = peerID
	return cfg, false
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

func (al *agentLoop) ResumeInterruptedTask(ctx context.Context, peerID int64) {
	sess := al.GetSession(peerID)
	if sess == nil || sess.GetResumePrompt() == "" {
		return
	}
	if al.log != nil {
		al.log.InfoLogf("[RESUME] continuing interrupted task for peer %d (resume_prompt=%q)", peerID, stringutil.Truncate(sess.GetResumePrompt(), 80, "..."))
	}
	const contPrompt = "The process was restarted. Continue your task from where you left off."
	if _, err := al.ProcessPrompt(ctx, contPrompt, peerID); err != nil {
		if al.log != nil {
			al.log.WarnLogf("[RESUME] continue interrupted task for peer %d failed: %v", peerID, err)
		}
	}
}

func (al *agentLoop) SetThinkingCallback(cb func(peerID int64, content string) error) {
	al.thinkingCallback = cb
}

func (al *agentLoop) TestLlamaServer(ctx context.Context) (model string, responseTime time.Duration, tokensPerSec float64, err error) {
	_, modelName, llamaURL := al.config.ModelHolder.GetCurrent()
	result := TestLlamaServer(ctx, llamaURL, modelName)
	return result.Model, result.ResponseTime, result.TokensPerSec, result.Error
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
