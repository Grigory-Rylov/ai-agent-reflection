package agentloop

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/opencode/llama-client/pkg/agent"
	"github.com/opencode/llama-client/pkg/compress"
	"github.com/opencode/llama-client/pkg/tokenizers"
	"github.com/opencode/llama-client/session"
)

// ============================================================
// Интерфейс AgentLoop
// ============================================================

// AgentLoop определяет основной интерфейс цикла агента
type AgentLoop interface {
	// ProcessPrompt обрабатывает промпт пользователя и возвращает ответ AI
	ProcessPrompt(ctx context.Context, prompt string, peerID int64) (string, error)

	// Start запускает цикл агента (для долгосрочных сценариев)
	Start(ctx context.Context)

	// Stop gracefully завершает цикл
	Stop()

	// ResetSession сбрасывает сессию пользователя
	ResetSession(peerID int64)

	// GetSession возвращает сессию пользователя
	GetSession(peerID int64) *session.Session
}

// ============================================================
// Реализация AgentLoop
// ============================================================

// agentLoop — основная реализация цикла агента
type agentLoop struct {
	config     LoopConfig
	sessionM   sync.Map // peerID -> *session.Session
	vk         VKClient
	registry   ToolRegistry
	contextMgr *compress.ContextManager
	dispatcher *EventDispatcher
	stopCh     chan struct{}
	isRunning  bool
	mu         sync.Mutex
	log        Logger
	aiHistory  []string // История ответов AI для loop detection
	historyMu  sync.Mutex
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

	// Инициализируем ContextManager если включено сжатие
	var contextMgr *compress.ContextManager
	if config.EnableCompression {
		contextMgr = initContextManager(config.LlamaServerURL, config.Model, config.MaxTokens, config.Temperature, config.CompressionTokenThreshold)
	}

	return &agentLoop{
		config:     config,
		vk:         vk,
		registry:   registry,
		contextMgr: contextMgr,
		stopCh:     make(chan struct{}),
		dispatcher: NewEventDispatcher(),
		log:        l,
	}, nil
}

// initContextManager инициализирует менеджер контекста для сжатия
func initContextManager(serverURL, model string, maxTokens int, temperature float64, threshold int) *compress.ContextManager {
	compressor := compress.NewLLMCompressor(serverURL, model, temperature)
	tokenizer := tokenizers.NewLlamaServerTokenizer(serverURL, model, maxTokens)
	trigger := compress.CompressionTrigger{
		TokenThreshold:      threshold,
		PercentageThreshold: 0.75,
	}
	return compress.NewContextManager(compressor, tokenizer, trigger)
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
	response, err := al.sendToLLM(ctx, messages, sess, peerID)
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
	if !al.config.EnableThinking || al.config.ThinkingPeerID <= 0 || al.vk == nil {
		return
	}

	// Отправляем thinking сообщение в thinking_peer_id
	_, err := al.vk.SendThinking(al.config.ThinkingPeerID, content)
	if err != nil {
		if al.log != nil {
			al.log.ErrorLogf("Failed to send thinking message: %v", err)
		}
		return
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

	sess := session.NewSession(config)
	al.sessionM.Store(peerID, sess)

	if al.log != nil {
		al.log.InfoLogf("Created new session for peer %d", peerID)
	}

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
func (al *agentLoop) sendToLLM(ctx context.Context, messages []agent.Message, sess *session.Session, peerID int64) (string, error) {
	// Создаём agent для обработки
	agentConfig := al.buildAgentConfig()
	var a agent.Agent = agent.NewAgent(agentConfig)

	// Настраиваем инструменты если включены
	if al.config.EnableTools && al.registry != nil {
		al.registerToolsToAgent(a, al.registry)
	}

	// Отправляем запрос
	response, err := a.ProcessMessage(ctx, "", peerID)
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
		LlamaServerURL: al.config.LlamaServerURL,
		Model:          al.config.Model,
		MaxTokens:      al.config.MaxTokens,
		Temperature:    al.config.Temperature,
		SessionConfig:  al.config.SessionConfig,
		EnableTools:    al.config.EnableTools,
		MaxToolCalls:   al.config.MaxToolCalls,
	}
}

// registerToolsToAgent регистрирует инструменты в agent
func (al *agentLoop) registerToolsToAgent(a agent.Agent, reg ToolRegistry) {
	// TODO: Реализовать регистрацию инструментов
	// Пока заглушка
	_ = a
	_ = reg
}

// processToolCalls обрабатывает вызовы инструментов от AI
func (al *agentLoop) processToolCalls(ctx context.Context, toolCalls []map[string]interface{}, sess *session.Session, peerID int64) ([]map[string]interface{}, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}

	if al.log != nil {
		al.log.InfoLogf("Processing %d tool calls for peer %d", len(toolCalls), peerID)
	}

	// Эмитим событие tool call
	al.dispatcher.Emit(NewEvent(EventToolCall, peerID))

	results := make([]map[string]interface{}, len(toolCalls))

	for i, tc := range toolCalls {
		toolName := getStringField(tc, "name")
		_ = tc

		if al.log != nil {
			al.log.InfoLogf("Executing tool: %s", toolName)
		}

		// Отправляем thinking сообщение если включено
		al.sendThinking(peerID, "Executing tool: "+toolName)

		// Получаем инструмент из реестра
		var result string
		var err error
		if al.registry != nil {
			// TODO: Реализовать получение и выполнение инструмента
			result = fmt.Sprintf(`{"success": false, "error": "tool %s not implemented yet"}`, toolName)
			err = nil
		} else {
			result = fmt.Sprintf(`{"success": false, "error": "no tool registry"}`)
			err = fmt.Errorf("no tool registry")
		}

		results[i] = map[string]interface{}{
			"tool_name": toolName,
			"result":    result,
			"error":     err,
		}

		// Эмитим событие tool result
		al.dispatcher.Emit(NewEvent(EventToolResult, peerID))

		if al.log != nil {
			if err != nil {
				al.log.ErrorLogf("Tool %s failed: %v", toolName, err)
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
	if al.contextMgr == nil {
		return
	}

	// Конвертируем историю сессии в формат tokenizers
	history := sess.GetHistory()
	var tokenizerMessages []tokenizers.Message
	for _, msg := range history {
		tokenizerMessages = append(tokenizerMessages, tokenizers.Message{
			Role:    string(msg.Role),
			Content: msg.Content,
		})
	}

	// Выполняем проверку и сжатие
	err := al.contextMgr.CheckAndCompress(ctx, peerID, tokenizerMessages, al.config.MaxTokens)
	if err != nil {
		// Если сжатие не удалось — продолжаем без него
		if al.log != nil {
			al.log.WarnLogf("Context compression skipped: %v", err)
		}
	} else if al.log != nil {
		al.log.InfoLogf("Context compressed for peer %d", peerID)
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
		return val.(*session.Session)
	}
	return nil
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
