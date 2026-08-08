package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	// ResumeInterruptedTask продолжает незавершённую задачу главного агента
	// после рестарта (если сессия помечена resume_prompt в БД).
	ResumeInterruptedTask(ctx context.Context, peerID int64)
	// ClearAllSlots очищает все серверные слоты и их KV-cache файлы для
	// текущей модели (best-effort, для стартового сброса -r).
	ClearAllSlots(ctx context.Context)
	SetThinkingCallback(cb func(peerID int64, content string) error)
	GetContextStats(peerID int64) (charCount int, tokenCount int, err error)
	TestLlamaServer(ctx context.Context) (model string, responseTime time.Duration, tokensPerSec float64, err error)
	GetModelHolder() *modelsconfig.Holder
	GetSlotManager() *SlotManager
	GetSlots() *SlotClient
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
	currentAlias     string
	modelMu          sync.Mutex
	slots            *SlotClient
	slotMgr          *SlotManager
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

	// Лимит контекста для текущей модели.
	// Приоритет: ContextResolver (models.json context или реальный контекст сервера),
	// иначе models.json, иначе реальный контекст с сервера, иначе config.MaxTokens.
	var tokenizer *tokenizers.LlamaServerTokenizer
	if config.ContextResolver != nil {
		ctx, err := config.ContextResolver.Resolve()
		if err != nil {
			return nil, fmt.Errorf("resolve model context: %w", err)
		}
		config.MaxTokens = ctx
		if l != nil {
			l.InfoLogf("Using model context for %s: %d tokens", alias, ctx)
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
		slots:        newSlotClient(),
		slotMgr:      slotMgr,
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

// resolveMaxTokens возвращает лимит контекста модели: из models.json (поле context),
// если задан, иначе fallback.
func resolveMaxTokens(h *modelsconfig.Holder, alias string, fallback int) int {
	if h == nil {
		return fallback
	}
	if ctx := h.GetModelContext(alias); ctx > 0 {
		return ctx
	}
	return fallback
}

// syncCurrentModel пересоздаёт токенайзер и компрессор при смене текущей модели.
// Вызывается перед обработкой каждого промпта, поэтому после /r <alias> агент
// начинает использовать новую модель/сервер для токенизации и компакции.
func (al *agentLoop) syncCurrentModel() error {
	al.modelMu.Lock()
	defer al.modelMu.Unlock()

	if al.config.ModelHolder == nil {
		return nil
	}

	alias, modelName, llamaURL := al.config.ModelHolder.GetCurrent()
	if llamaURL == "" {
		llamaURL = "http://127.0.0.1:8081"
	}
	if modelName == "" {
		modelName = "local-model"
	}
	if alias == al.currentAlias {
		return nil
	}

	al.currentAlias = alias

	if al.config.ContextResolver != nil {
		ctx, err := al.config.ContextResolver.Resolve()
		if err != nil {
			return fmt.Errorf("resolve model context for %s: %w", alias, err)
		}
		al.config.MaxTokens = ctx
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
	return nil
}

// syncVisionTool регистрирует image2text тул, если текущая модель
// поддерживает изображения (vision), и разрегистрирует в противном случае.
// Возвращает true, если тул в итоге зарегистрирован.
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
		if al.log != nil {
			al.log.InfoLogf("image2text tool registered (vision model)")
		}
		return true
	}
	if !vision && reg.IsRegistered("image2text") {
		reg.Unregister("image2text")
		if al.log != nil {
			al.log.InfoLogf("image2text tool unregistered (model is not vision-capable)")
		}
		return false
	}
	return vision
}

// currentModelSlotSave возвращает true, если текущая модель настроена на
// сохранение/восстановление KV-cache слота llama-server (slot-save в models.json).
func (al *agentLoop) currentModelSlotSave() bool {
	if al.config.ModelHolder == nil {
		return false
	}
	return al.config.ModelHolder.GetCurrentSlotSave()
}

// restoreSlotInto восстанавливает KV-cache из {model}_slot{N}.bin в слот slotID
// перед запросом. Ошибки обрабатываются по правилам доступности:
//   - отсутствует файл (первый запуск) — debug-лог, продолжаем cold;
//   - ошибка конфигурации (нет --slot-save-path) — хост помечается недоступным
//     и логируется один раз на уровне info, дальше save/restore пропускаются;
//   - прочие ошибки — warn-лог.
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

// saveSlotFrom сохраняет KV-cache слота slotID в {model}_slot{N}.bin после ответа.
// При ошибке конфигурации хост помечается недоступным (один info-лог), прочие
// ошибки — warn. Ошибки save никогда не валят обработку запроса.
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

// sanitizeSlotName приводит имя модели к безопасному имени файла.
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

func (al *agentLoop) ProcessPrompt(ctx context.Context, prompt string, peerID int64) (string, error) {
	if err := al.syncCurrentModel(); err != nil {
		return "", err
	}
	sess := al.getOrCreateSession(peerID)

	// Помечаем обработку незавершённой: если процесс упадёт/будет перезапущен
	// посреди задачи, resume_prompt в БД позволит продолжить её на старте.
	sess.SetResumePrompt(prompt)
	defer sess.SetResumePrompt("")

	if al.log != nil {
		al.log.InfoLogf("Prompt received from peer %d: %s", peerID, truncate(prompt, 100))
	}

	al.dispatcher.Emit(NewEvent(EventPromptReceived, peerID))

	sess.AddUserMessage(prompt)

	if al.config.EnableCompression {
		al.checkAndCompressOpenCode(ctx, sess, peerID)
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

		// Get or assign slot for this session (LRU eviction if all occupied).
		slotID, evictedSessionID := al.slotMgr.GetOrAssign(sessionID, al.slotMgr.TotalSlots())
		if al.slotMgr.IsAvailable(host) && slotID >= 0 {
			assignedSlotID = slotID

			// On eviction, persist the evicted session's KV-cache into its
			// slot file, then clear the server slot so the new session does
			// NOT inherit the evicted session's context (slot-keyed files
			// are shared by slot, so we must start the new owner cold).
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
				// Same session reuses its slot → restore its own cache.
				// Fresh slot → restore is a no-op (no file yet).
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
		// KV-cache уже сохранён per-response внутри agent_impl (SlotSaver);
		// здесь только обновляем LRU-метку сессии.
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
	// Pin agent to its session's slot for KV-cache continuity. SlotSaver
	// сохраняет KV-cache после каждого ответа LLM (только при slot-save).
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
		ToolOutputMaxLines:             al.config.ToolOutputMaxLines,
		ToolOutputMaxBytes:             al.config.ToolOutputMaxBytes,
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
		Dir:         filepath.Join(tools.WorkingDir, "tool-output"),
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

	if al.log != nil {
		al.log.InfoLogf("[OPENCODE-COMPACT] Peer %d: Overflow detected (%d/%d), compacting",
			peerID, tokensBefore, al.config.MaxTokens)
	}

	tailTurns := al.config.TailTurns
	if tailTurns <= 0 {
		tailTurns = 2
	}

	// Компактируем по сырой истории (без FilterCompacted): TailStartID из
	// select() должен совпадать с индексами session.messages для MarkCompaction.
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

	// Auto-continuation: после авто-компактизации добавляем синтетическое
	// user-сообщение (как в opencode compaction.ts:444-526), чтобы цикл агента
	// продолжался автоматически после инъекции summary.
	if result.Summary != "" {
		sess.AddUserMessage(tokenizers.CompactionAutoContinueText)
	}

	// After compaction, the KV-cache in the slot is stale: history was rewritten,
	// so cached prompt tokens no longer match. Invalidate the slot fully —
	// delete the cache file and clear the server slot — then release it in the
	// SlotManager. The next request for this session allocates a fresh slot and
	// starts cold (no stale context is restored into any session that reuses it).
	sessionID := sess.GetSessionID()
	al.invalidateSessionSlot(ctx, sessionID)
}

// invalidateSessionSlot стирает KV-cache слота сессии в памяти сервера
// (action=erase) и освобождает слот в SlotManager. Используется при компакции
// и сбросе сессии — история переписана, кэш устарел. Файл на диске не удаляется
// (llama-server не поддерживает это через HTTP); он перезапишется при следующем
// save этого слота.
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
		// No slot assigned (e.g. feature unavailable) — nothing to invalidate.
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
	// Не сбрасываем сессию (модель opencode): головная часть помечается как
	// compacted, хвост сохраняется через tail_start_id, а в начале контекста
	// после компактизации всегда идут /pin-промпты (см. GetContextMessages).
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

	// PruneMessages работает над сырой историей (без FilterCompacted), чтобы
	// индексы совпадали с сессией, а маркеры компактизации сохранялись.
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

	// Применяем обрезку на месте — без Reset(): история, маркеры компактизации
	// и tail_start_id остаются нетронутыми. Затрагиваем ТОЛЬКО новые обрезки:
	// сообщения, уже помеченные compacted предыдущей компактизацией, содержат
	// исходный head-контент (нужен для будущих summaries) — их не трогаем.
	for i := range pruned {
		if pruned[i].Compacted && !raw[i].Compacted {
			sess.MarkMessageCompacted(i, compress.PRUNED_OUTPUT_PLACEHOLDER)
		}
	}
}

func (al *agentLoop) convertHistoryToMessages(history []session.Message) []tokenizers.Message {
	return compress.FilterCompacted(al.convertHistoryToRawMessages(history))
}

// convertHistoryToRawMessages конвертирует историю 1:1 (без FilterCompacted),
// сохраняя выравнивание индексов с session.messages — нужно для TailStartID
// при повторной компактизации и для runPruning.
func (al *agentLoop) convertHistoryToRawMessages(history []session.Message) []tokenizers.Message {
	messages := make([]tokenizers.Message, len(history))
	for i, msg := range history {
		content := msg.Content
		// Tool call аргументы учитываются в оценке (как в opencode)
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

// deleteSessionSlot очищает слот сессии при сбросе: удаляет файл KV-cache,
// очищает серверный слот и возвращает слот в свободный пул SlotManager.
// Использует исключительно отображение SlotManager (никаких сессийно-ключёвых
// имён файлов). Если у сессии нет слота — no-op.
func (al *agentLoop) deleteSessionSlot(ctx context.Context, peerID int64, sessionID string) {
	_ = peerID
	al.invalidateSessionSlot(ctx, sessionID)
}

// ClearAllSlots очищает все серверные слоты и удаляет их KV-cache файлы для
// текущей модели. Best-effort: используется при старте с флагом -r, чтобы
// устранить устаревшие файлы от предыдущего запуска. Если сервер недоступен
// или флаг слотов не поддерживается — тихо пропускается.
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

// ResumeInterruptedTask продолжает незавершённую задачу главного агента после
// рестарта. Если при остановке процесса сессия была помечена как in-progress
// (resume_prompt не пуст), история уже восстановлена из БД — отправляем модели
// continuation-промпт, и она продолжает с места обрыва. No-op без флага.
func (al *agentLoop) ResumeInterruptedTask(ctx context.Context, peerID int64) {
	sess := al.GetSession(peerID)
	if sess == nil || sess.GetResumePrompt() == "" {
		return
	}
	if al.log != nil {
		al.log.InfoLogf("[RESUME] continuing interrupted task for peer %d (resume_prompt=%q)", peerID, truncate(sess.GetResumePrompt(), 80))
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
