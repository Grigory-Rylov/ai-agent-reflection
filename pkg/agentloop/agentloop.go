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
	"github.com/opencode/llama-client/pkg/compress"
	"github.com/opencode/llama-client/pkg/tokenizers"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

// ============================================================
// Интерфейс AgentLoop
// ============================================================

// AgentLoop определяет основной интерфейс цикла агента
type AgentLoop interface {
	// ProcessPrompt обрабатывает промпт пользователя и возвращает ответ AI
	ProcessPrompt(ctx context.Context, prompt string, peerID int64) (string, error)

	// ProcessMessage — алиас для ProcessPrompt (совместимость с agent.Agent)
	ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error)

	// Start запускает цикл агента (для долгосрочных сценариев)
	Start(ctx context.Context)

	// Stop gracefully завершает цикл
	Stop()

	// ResetSession сбрасывает сессию пользователя
	ResetSession(peerID int64)

	// GetSession возвращает сессию пользователя (nil если не существует)
	GetSession(peerID int64) *session.Session

	// EnsureSession гарантирует существование сессии (загружает из файла если нужно)
	EnsureSession(peerID int64) *session.Session

	// SetThinkingCallback устанавливает callback для отправки thinking сообщений
	SetThinkingCallback(cb func(peerID int64, content string) error)

	// GetContextStats возвращает статистику контекста: символы, токены
	GetContextStats(peerID int64) (charCount int, tokenCount int, err error)

	// TestLlamaServer тестирует соединение с llama-server и возвращает информацию о модели
	TestLlamaServer(ctx context.Context) (model string, responseTime time.Duration, tokensPerSec float64, err error)
}

// ============================================================
// Реализация AgentLoop
// ============================================================

// agentLoop — основная реализация цикла агента
type agentLoop struct {
	config           LoopConfig
	sessionM         sync.Map // peerID -> *session.Session
	vk               VKClient
	registry         ToolRegistry
	contextMgr       *compress.ContextManager
	compactor        *compress.Compactor      // New compactor
	artifactStore    *compress.FileArtifactStore
	tokenizer        tokenizers.Tokenizer
	dispatcher       *EventDispatcher
	stopCh           chan struct{}
	isRunning        bool
	mu               sync.Mutex
	log              Logger
	aiHistory        []string // История ответов AI для loop detection
	historyMu        sync.Mutex
	thinkingCallback func(peerID int64, content string) error
	contextState     map[int64]*compress.ContextState // peerID -> ContextState
	stateMu          sync.RWMutex
}

// NewAgentLoop создаёт новый цикл агента
func NewAgentLoop(config LoopConfig, vk VKClient, registry ToolRegistry) (AgentLoop, error) {
	if config.LlamaServerURL == "" {
		config.LlamaServerURL = DefaultLoopConfig().LlamaServerURL
	}
	if config.Model == "" {
		config.Model = DefaultLoopConfig().Model
	}

	var l Logger
	if config.Logger != nil {
		l = config.Logger
	} else if config.EnableLogging {
		l = NewDefaultLogger()
	}

	// Инициализируем токенайзер (всегда)
	tokenizer := tokenizers.NewLlamaServerTokenizer(config.LlamaServerURL, config.Model, config.MaxTokens)
	if config.EnableLogging {
		tokenizer.SetDebug(true)
	}

	// Инициализируем новый Compactor
	compactor := compress.NewCompactor(config.CompactionConfig, nil, nil)

	// Инициализируем artifact store
	var artifactStore *compress.FileArtifactStore
	if config.ArtifactStorePath != "" {
		var err error
		artifactStore, err = compress.NewFileArtifactStore(config.ArtifactStorePath)
		if err != nil && l != nil {
			l.WarnLogf("Failed to create artifact store: %v", err)
		}
	}

	// Legacy ContextManager (для совместимости)
	var contextMgr *compress.ContextManager
	if config.EnableCompression {
		compressor := compress.NewLLMCompressor(config.LlamaServerURL, config.Model, config.Temperature)
		if config.CompressionTokenThreshold <= 0 {
			config.CompressionTokenThreshold = 6000
		}
		if config.CompressionPercentageThreshold <= 0 {
			config.CompressionPercentageThreshold = 0.75
		}
		trigger := compress.CompressionTrigger{
			TokenThreshold:      config.CompressionTokenThreshold,
			PercentageThreshold: config.CompressionPercentageThreshold,
		}
		contextMgr = compress.NewContextManager(compressor, tokenizer, trigger)
	}

	return &agentLoop{
		config:        config,
		vk:         vk,
		registry:      registry,
		contextMgr:    contextMgr,
		compactor:     compactor,
		artifactStore: artifactStore,
		tokenizer:     tokenizer,
		stopCh:        make(chan struct{}),
		dispatcher:    NewEventDispatcher(),
		log:           l,
		contextState:  make(map[int64]*compress.ContextState),
	}, nil
}

// GetContextStats возвращает статистику контекста для указанного peer
func (al *agentLoop) GetContextStats(peerID int64) (charCount int, tokenCount int, err error) {
	s := al.GetSession(peerID)
	if s == nil {
		return 0, 0, fmt.Errorf("session not found for peer %d", peerID)
	}

	history := s.GetHistory()

	// Подсчёт символов
	for _, msg := range history {
		charCount += len([]rune(msg.Content))
	}

	// Подсчёт токенов через новый метод
	if al.tokenizer != nil && len(history) > 0 {
		// Конвертируем историю в формат tokenizers.Message
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

// ProcessMessage — алиас для ProcessPrompt (совместимость с agent.Agent)
func (al *agentLoop) ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error) {
	return al.ProcessPrompt(ctx, prompt, peerID)
}

// ============================================================
// ProcessPrompt — основной метод обработки промпта
// ============================================================

// ProcessPrompt обрабатывает промпт пользователя и возвращает ответ
func (al *agentLoop) ProcessPrompt(ctx context.Context, prompt string, peerID int64) (string, error) {
	// 1. Получаем или создаём сессию
	sess := al.getOrCreateSession(peerID)

	// 2. Логируем получение промпта
	if al.log != nil {
		al.log.InfoLogf("Prompt received from peer %d: %s", peerID, truncate(prompt, 100))
	}

	// 3. Эмитим событие
	al.dispatcher.Emit(NewEvent(EventPromptReceived, peerID))

	// 4. Добавляем промпт в историю сессии
	sess.AddUserMessage(prompt)

	// 5. Проверяем сжатие контекста
	if al.config.EnableCompression {
		al.checkAndCompress(ctx, sess, peerID)
	}

	// 6. Строим сообщения для API
	messages := al.buildAPIMessages(sess)

	// 7. Отправляем запрос в LLM
	response, err := al.sendToLLM(ctx, messages, sess, peerID, prompt)
	if err != nil {
		if al.log != nil {
			al.log.ErrorLogf("LLM request failed: %v", err)
		}
		return "", fmt.Errorf("LLM request failed: %w", err)
	}

	// 8. Проверяем loop detection
	if al.checkLoopDetection(response, peerID) {
		if al.log != nil {
			al.log.WarnLogf("Adding loop alert to next prompt for peer %d", peerID)
		}
	}

	// 9. Эмитим событие завершения
	al.dispatcher.Emit(NewEvent(EventResponseDone, peerID))

	// 10. Возвращаем ответ
	return response, nil
}

// ============================================================
// Внутренние методы
// ============================================================

// sendThinking отправляет thinking сообщение в thinking_peer_id
func (al *agentLoop) sendThinking(peerID int64, content string) {
	if !al.config.EnableThinking || al.config.ThinkingPeerID <= 0 {
		return
	}

	// Используем thinkingCallback если установлен
	if al.thinkingCallback != nil {
		err := al.thinkingCallback(al.config.ThinkingPeerID, content)
		if err != nil {
			if al.log != nil {
				al.log.ErrorLogf("Failed to send thinking message: %v", err)
			}
			return
		}
	} else if al.vk != nil {
		// Fallback на прямой вызов vk.SendThinking
		_, err := al.vk.SendThinking(al.config.ThinkingPeerID, content)
		if err != nil {
			if al.log != nil {
				al.log.ErrorLogf("Failed to send thinking message: %v", err)
			}
			return
		}
	}

	// Эмитим событие thinking
	al.dispatcher.Emit(NewEvent(EventThinking, peerID))

	if al.log != nil {
		al.log.InfoLogf("Thinking sent to peer %d: %s", al.config.ThinkingPeerID, truncate(content, 80))
	}
}

// getOrCreateSession возвращает существующую сессию или создаёт новую
func (al *agentLoop) getOrCreateSession(peerID int64) *session.Session {
	if val, ok := al.sessionM.Load(peerID); ok {
		return val.(*session.Session)
	}

	config := al.config.SessionConfig
	config.PeerID = peerID

	// Загружаем системный промпт из файла или используем дефолтный
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

	// Логируем информацию о файле сессии
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

// checkLoopDetection проверяет не зациклилась ли AI
func (al *agentLoop) checkLoopDetection(response string, peerID int64) bool {
	if !al.config.EnableLoopDetection {
		return false
	}

	al.historyMu.Lock()
	defer al.historyMu.Unlock()

	// Добавляем текущий ответ в историю
	al.aiHistory = append(al.aiHistory, response)

	// Проверяем последние N ответов (максимум 5)
	maxHistory := 5
	if len(al.aiHistory) > maxHistory {
		al.aiHistory = al.aiHistory[len(al.aiHistory)-maxHistory:]
	}

	// Если меньше 2 ответов — цикл невозможен
	if len(al.aiHistory) < 2 {
		return false
	}

	// Проверяем схожесть с предыдущими ответами
	current := strings.TrimSpace(response)
	for i := len(al.aiHistory) - 2; i >= 0; i-- {
		previous := strings.TrimSpace(al.aiHistory[i])
		if similarity(current, previous) >= al.config.LoopThreshold {
			// Цикл обнаружен!
			al.logLoopDetection(peerID, current, previous)
			// Очищаем историю после обнаружения цикла
			al.aiHistory = []string{}
			return true
		}
	}

	return false
}

// logLoopDetection логирует обнаружение цикла
func (al *agentLoop) logLoopDetection(peerID int64, current, previous string) {
	if al.log != nil {
		al.log.WarnLogf("Loop detected for peer %d: response repeating", peerID)
	}
	al.dispatcher.Emit(NewEvent(EventLoopDetected, peerID))
}

// similarity вычисляет схожесть двух строк (0.0-1.0)
func similarity(a, b string) float64 {
	if a == b {
		return 1.0
	}

	wordsA := strings.Fields(strings.ToLower(a))
	wordsB := strings.Fields(strings.ToLower(b))

	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0.0
	}

	// Word overlap coefficient
	common := 0
	for _, wA := range wordsA {
		for _, wB := range wordsB {
			if wA == wB {
				common++
				break
			}
		}
	}

	// Используем минимальное количество слов для нормализации
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
	history := sess.GetHistory()
	messages := make([]agent.Message, len(history))

	for i, msg := range history {
		messages[i] = agent.Message{
			Role:    string(msg.Role),
			Content: msg.Content,
		}
	}

	return messages
}

// sendToLLM отправляет запрос в LLM и собирает ответ
func (al *agentLoop) sendToLLM(ctx context.Context, messages []agent.Message, sess *session.Session, peerID int64, prompt string) (string, error) {
	// Создаём agent для обработки
	agentConfig := al.buildAgentConfig()
	var a agent.Agent = agent.NewAgent(agentConfig)

	// Устанавливаем callback для thinking сообщений
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

	// Настраиваем инструменты если включены
	if al.config.EnableTools && al.registry != nil {
		al.registerToolsToAgent(a, al.registry)
	}

	// Отправляем запрос с реальным сообщением пользователя
	response, err := a.ProcessMessage(ctx, prompt, peerID)
	if err != nil {
		return "", err
	}

	// Добавляем ответ в сессию
	sess.AddAssistantMessage(response)

	return response, nil
}

// buildAgentConfig строит конфигурацию для agent
func (al *agentLoop) buildAgentConfig() agent.Config {
	return agent.Config{
		LlamaServerURL:                al.config.LlamaServerURL,
		Model:                         al.config.Model,
		MaxTokens:                     al.config.MaxTokens,
		Temperature:                   al.config.Temperature,
		SessionConfig:                 al.config.SessionConfig,
		EnableTools:                   al.config.EnableTools,
		MaxToolCalls:                  al.config.MaxToolCalls,
		EnableContextCompression:      false,
		CompressionTokenThreshold:     al.config.CompressionTokenThreshold,
		CompressionPercentageThreshold: al.config.CompressionPercentageThreshold,
		Debug:                         al.config.Debug,
	}
}

// registerToolsToAgent регистрирует инструменты из registry в agent
func (al *agentLoop) registerToolsToAgent(a agent.Agent, reg ToolRegistry) {
	if reg == nil {
		return
	}

	// Пробуем привести к *agent.agentImpl для прямого добавления инструментов
	type toolInserter interface {
		RegisterTools(registry *tools.Registry)
	}
	if inserter, ok := a.(toolInserter); ok {
		if r, ok := reg.(*tools.Registry); ok {
			inserter.RegisterTools(r)
			return
		}
	}

	// Fallback: передаём схемы через SetTools
	toolSchemas := reg.ToOpenAISchema()
	if len(toolSchemas) > 0 {
		a.SetTools(toolSchemas)
	}

	if al.log != nil {
		al.log.InfoLogf("Registered %d tools from registry", len(toolSchemas))
	}
}

// processToolCalls обрабатывает вызовы инструментов от AI
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

// getStringField извлекает строковое поле из map
func getStringField(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// checkAndCompress проверяет и выполняет сжатие контекста
func (al *agentLoop) checkAndCompress(ctx context.Context, sess *session.Session, peerID int64) {
	// Используем новый компактор если доступен
	if al.compactor != nil {
		al.checkAndCompactNew(ctx, sess, peerID)
		return
	}

	// Legacy: используем старый ContextManager
	if al.contextMgr == nil {
		return
	}

	history := sess.GetHistory()
	var tokenizerMessages []tokenizers.Message
	for _, msg := range history {
		tokenizerMessages = append(tokenizerMessages, tokenizers.Message{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}

	err := al.contextMgr.CheckAndCompress(ctx, peerID, tokenizerMessages, al.config.MaxTokens)
	if err != nil {
		if al.log != nil {
			al.log.WarnLogf("Context compression skipped: %v", err)
		}
	} else if al.log != nil {
		al.log.InfoLogf("Context compressed for peer %d", peerID)
	}
}

// checkAndCompactNew использует новый компактор для сжатия
func (al *agentLoop) checkAndCompactNew(ctx context.Context, sess *session.Session, peerID int64) {
	history := sess.GetHistory()

	// Конвертируем в формат tokenizers
	messages := al.convertHistoryToMessages(history)

	// Оцениваем размер до сжатия
	tokensBefore := compress.EstimateMessagesTokensSimple(messages)

	// Debug: логируем текущее состояние
	if al.log != nil {
		al.log.DebugLogf("[COMPACTION] Peer %d: %d messages, ~%d tokens",
			peerID, len(messages), tokensBefore)
	}

	// Проверяем и сжимаем
	result, err := al.compactor.CheckAndCompact(ctx, messages, al.config.MaxTokens)
	if err != nil {
		if al.log != nil {
			al.log.WarnLogf("[COMPACTION] Failed: %v", err)
		}
		return
	}

	if result == nil {
		// Сжатие не требуется
		if al.log != nil {
			al.log.DebugLogf("[COMPACTION] Peer %d: No compression needed", peerID)
		}
		return
	}

	// Логируем результат
	if al.log != nil {
		al.log.InfoLogf("[COMPACTION] Peer %d: %d -> %d tokens (%.1f%% reduction), level=%v",
			peerID, result.TokensBefore, result.TokensAfter,
			(1-result.CompressionRatio())*100, result.Level)
	}

	// Debug: детали сжатия
	if al.log != nil && result.State != nil {
		al.log.DebugLogf("[COMPACTION] State extracted: goal='%s', decisions=%d, memory=%d, artifacts=%d",
			result.State.Goal, len(result.State.Decisions),
			len(result.State.WorkingMemory), len(result.State.Artifacts))

		if len(result.State.Decisions) > 0 {
			for i, d := range result.State.Decisions {
				al.log.DebugLogf("[COMPACTION]   Decision %d: %s", i+1, d)
			}
		}

		if len(result.State.Artifacts) > 0 {
			for i, a := range result.State.Artifacts {
				al.log.DebugLogf("[COMPACTION]   Artifact %d: %s (%s)", i+1, a.Path, a.Description)
			}
		}
	}

	// Debug: сообщения
	if al.log != nil {
		al.log.DebugLogf("[COMPACTION] Kept %d messages, summarized %d",
			len(result.KeptMessages), result.SummarizedCount)
	}

	// Сохраняем состояние
	if result.State != nil {
		al.saveContextState(peerID, result.State)
	}

	// Обновляем сессию с сохранёнными сообщениями
	al.updateSessionAfterCompaction(sess, result)
}

// convertHistoryToMessages конвертирует историю сессии в сообщения
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

// saveContextState сохраняет состояние контекста для пользователя
func (al *agentLoop) saveContextState(peerID int64, state *compress.ContextState) {
	al.stateMu.Lock()
	defer al.stateMu.Unlock()
	al.contextState[peerID] = state
}

// getContextState возвращает состояние контекста для пользователя
func (al *agentLoop) getContextState(peerID int64) *compress.ContextState {
	al.stateMu.RLock()
	defer al.stateMu.RUnlock()
	return al.contextState[peerID]
}

// updateSessionAfterCompaction обновляет сессию после сжатия
func (al *agentLoop) updateSessionAfterCompaction(sess *session.Session, result *compress.CompactionResult) {
	if len(result.KeptMessages) == 0 {
		return
	}

	// Сбрасываем сессию
	sess.Reset()

	// Восстанавливаем сохранённые сообщения
	for _, msg := range result.KeptMessages {
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

// ============================================================
// Start / Stop
// ============================================================

// Start запускает цикл агента
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

// Stop останавливает цикл агента
func (al *agentLoop) Stop() {
	al.mu.Lock()
	al.isRunning = false
	al.mu.Unlock()

	close(al.stopCh)

	if al.log != nil {
		al.log.InfoLog("AgentLoop stopped")
	}
}

// ============================================================
// Session Management
// ============================================================

// ResetSession сбрасывает сессию пользователя
func (al *agentLoop) ResetSession(peerID int64) {
	if val, ok := al.sessionM.Load(peerID); ok {
		sess := val.(*session.Session)
		sess.Reset()
		if al.log != nil {
			al.log.InfoLogf("Session reset for peer %d", peerID)
		}
	}
}

// GetSession возвращает сессию пользователя
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

// EnsureSession гарантирует существование сессии (загружает из файла если нужно)
func (al *agentLoop) EnsureSession(peerID int64) *session.Session {
	return al.getOrCreateSession(peerID)
}

// SetThinkingCallback устанавливает callback для отправки thinking сообщений
func (al *agentLoop) SetThinkingCallback(cb func(peerID int64, content string) error) {
	al.thinkingCallback = cb
}

// TestLlamaServer тестирует соединение с llama-server
func (al *agentLoop) TestLlamaServer(ctx context.Context) (model string, responseTime time.Duration, tokensPerSec float64, err error) {
	result := TestLlamaServer(ctx, al.config.LlamaServerURL, al.config.Model)
	return result.Model, result.ResponseTime, result.TokensPerSec, result.Error
}

// ============================================================
// Утилиты
// ============================================================

// truncate обрезает строку до максимальной длины
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ============================================================
// Default Logger — простой логгер по умолчанию
// ============================================================

// NewDefaultLogger создаёт логгер по умолчанию
func NewDefaultLogger() Logger {
	return &defaultLogger{}
}

// defaultLogger — простой логгер для дебага
type defaultLogger struct{}

func (l *defaultLogger) DebugLog(msg string, args ...interface{}) {
	fmt.Printf("[DEBUG] "+msg+"\n", args...)
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
