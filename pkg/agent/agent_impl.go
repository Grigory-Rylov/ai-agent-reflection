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

	"github.com/opencode/llama-client/pkg/compress"
	"github.com/opencode/llama-client/pkg/tokenizers"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

// Aliases for tools from tools package
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

// ============================================================
// AI Agent Implementation — реализация агента с подключением к llama-server
// ============================================================

// ThinkingCallback callback для отправки thinking сообщений
type ThinkingCallback func(peerID int64, content string) error

// agentImpl реализует интерфейс AI агента с подключением к llama-server
type agentImpl struct {
	config          Config
	sessions        map[int64]*session.Session
	toolsRegistry   *tools.Registry
	mu              sync.RWMutex
	client          *http.Client
	contextManager  *compress.ContextManager
	tokenizer       tokenizers.Tokenizer
	systemPrompt    string            // системный промпт из файла или дефолтный
	thinkingCallback ThinkingCallback  // callback для отправки thinking сообщений
	toolSchemas     []map[string]interface{} // схемы инструментов, переданные извне
	toolExecutor    ToolExecutor       // кастомный executor (для тестов через StubToolExecutor)
}

// ============================================================
// Инициализация
// ============================================================

// NewAgent создаёт новый AI Agent
func NewAgent(config Config) *agentImpl {
	agent := &agentImpl{
		config:       config,
		sessions:     make(map[int64]*session.Session),
		toolsRegistry: tools.NewRegistry(),
		client: &http.Client{
			Timeout: 5 * time.Minute,
			Transport: &http.Transport{
				DisableKeepAlives: true,
			},
		},
	}

	// Загружаем системный промпт из файла или используем дефолтный
	agent.loadSystemPrompt()

	// Регистрируем инструменты по умолчанию если включены
	if config.EnableTools {
		agent.registerDefaultTools()
	}

	// Инициализируем ContextManager если включено сжатие
	if config.EnableContextCompression {
		agent.initContextManager()
	}

	return agent
}

// loadSystemPrompt загружает системный промпт из файла или использует дефолтный
func (a *agentImpl) loadSystemPrompt() {
	// Дефолтный системный промпт
	defaultPrompt := "You are a helpful assistant."

	if a.config.SystemPromptFile == "" {
		// Если путь не указан — используем дефолтный
		a.systemPrompt = defaultPrompt
		return
	}

	// Пытаемся прочитать файл
	data, err := os.ReadFile(a.config.SystemPromptFile)
	if err != nil {
		fmt.Printf("[WARN] Could not read system prompt file '%s': %v. Using default.\n", a.config.SystemPromptFile, err)
		a.systemPrompt = defaultPrompt
		return
	}

	// Проверяем что файл не пустой
	content := strings.TrimSpace(string(data))
	if content == "" {
		fmt.Printf("[WARN] System prompt file '%s' is empty. Using default.\n", a.config.SystemPromptFile)
		a.systemPrompt = defaultPrompt
		return
	}

	// Используем прочитанный промпт
	a.systemPrompt = content
	fmt.Printf("[INFO] Loaded system prompt from '%s' (%d bytes)\n", a.config.SystemPromptFile, len(content))
}

// GetSystemPrompt возвращает системный промпт
func (a *agentImpl) GetSystemPrompt() string {
	return a.systemPrompt
}

// initContextManager инициализирует менеджер контекста
func (a *agentImpl) initContextManager() {
	// Создаём токенайзер
	a.tokenizer = tokenizers.NewLlamaServerTokenizer(a.config.LlamaServerURL, a.config.Model, a.config.MaxTokens)

	// Создаём компрессор
	compressor := compress.NewLLMCompressor(a.config.LlamaServerURL, a.config.Model, a.config.Temperature)

	// Создаём триггер
	trigger := compress.CompressionTrigger{
		TokenThreshold:        a.config.CompressionTokenThreshold,
		PercentageThreshold:   a.config.CompressionPercentageThreshold,
	}

	// Создаём менеджер контекста
	a.contextManager = compress.NewContextManager(compressor, a.tokenizer, trigger)
}

// registerDefaultTools регистрирует инструменты по умолчанию
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
}

// RegisterTools регистрирует инструменты из внешнего реестра
func (a *agentImpl) RegisterTools(registry *tools.Registry) {
	if registry == nil {
		return
	}
	for _, tool := range registry.GetAll() {
		if !a.toolsRegistry.IsRegistered(tool.Name()) {
			a.toolsRegistry.Register(tool)
		}
	}
	// Также передаём схемы для OpenAI function calling
	schema := registry.ToOpenAISchema()
	if len(schema) > 0 {
		a.toolSchemas = schema
	}
}

// ============================================================
// Методы Agent Interface
// ============================================================

// ProcessMessage обрабатывает сообщение пользователя и возвращает ответ
func (a *agentImpl) ProcessMessage(ctx context.Context, message string, peerID int64) (string, error) {
	fmt.Printf("[PROCESS] ProcessMessage called: peerID=%d, message=%q, tools=%d\n", peerID, message, len(a.toolsRegistry.GetAll()))
	// Получаем или создаём сессию
	s := a.getSession(peerID)

	// Проверяем, не зациклилась ли AI
	if s.IsLoopDetected() {
		alert := s.GetLoopAlertMessage()
		if alert != "" {
			message = "[LOOP DETECTED] " + alert + "\n\n" + message
		}
	}

	// Добавляем сообщение в сессию, если его там ещё нет.
	// В обычном потоке (через agentloop) сообщение уже добавлено в сессию
	// и сохранено в файл. При прямом вызове (через Orchestrator) добавляем здесь.
	history := s.GetHistory()
	if len(history) == 0 || history[len(history)-1].Role != session.UserRole || history[len(history)-1].Content != message {
		s.AddUserMessage(message)
		history = s.GetHistory()
	}

	// Проверяем и при необходимости сжимаем контекст
	if a.contextManager != nil {
		// Конвертируем историю в формат tokenizers.Message
		var tokenizerMessages []tokenizers.Message
		for _, msg := range history {
			tokenizerMessages = append(tokenizerMessages, tokenizers.Message{
				Role:    string(msg.Role),
				Content: msg.Content,
			})
		}
		err := a.contextManager.CheckAndCompress(ctx, peerID, tokenizerMessages, a.config.MaxTokens)
		if err != nil {
			// Если сжатие не удалось — продолжаем без него
			fmt.Printf("[CONTEXT] Compression skipped: %v\n", err)
		}
	}

	// Формируем сообщения для API
	apiMessages := a.convertHistoryToAPIMessages(history)

	// Проверяем, нужно ли использовать инструменты
	if a.config.EnableTools {
		// Используем function calling с инструментами
		result, err := a.processWithTools(ctx, apiMessages, s, a.config.MaxToolCalls)
		if err != nil {
			return "", fmt.Errorf("process with tools: %w", err)
		}
		return result.Response, nil
	}

	// Обычный streaming запрос без инструментов
	return a.processStreaming(ctx, apiMessages, s)
}

// ResetSession сбрасывает сессию пользователя
func (a *agentImpl) ResetSession(peerID int64) {
	s := a.getSession(peerID)
	s.Reset()
}

// GetSession возвращает сессию пользователя
func (a *agentImpl) GetSession(peerID int64) *session.Session {
	return a.getSession(peerID)
}

// SetThinkingCallback устанавливает callback для отправки thinking сообщений
func (a *agentImpl) SetThinkingCallback(cb ThinkingCallback) {
	a.thinkingCallback = cb
}

// SetTools регистрирует инструменты, переданные из agentloop
func (a *agentImpl) SetTools(toolSchemas []map[string]interface{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolSchemas = toolSchemas
}

// SetToolExecutor устанавливает кастомный executor для инструментов
func (a *agentImpl) SetToolExecutor(executor ToolExecutor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolExecutor = executor
}

// ============================================================
// Управление сессиями
// ============================================================

// getSession возвращает или создаёт сессию для пользователя
func (a *agentImpl) getSession(peerID int64) *session.Session {
	a.mu.RLock()
	s, exists := a.sessions[peerID]
	a.mu.RUnlock()

	if !exists {
		a.mu.Lock()
		// Double-check после получения write-lock
		s, exists = a.sessions[peerID]
		if !exists {
			config := a.config.SessionConfig
			config.PeerID = peerID
			config.SystemPrompt = a.systemPrompt
			s = session.NewSession(config)
			a.sessions[peerID] = s
		}
		a.mu.Unlock()
	}

	return s
}

// ============================================================
// Streaming без инструментов
// ============================================================

// processStreaming обрабатывает streaming запрос без инструментов
func (a *agentImpl) processStreaming(ctx context.Context, messages []Message, session *session.Session) (string, error) {
	streamConfig := StreamingConfig{
		Model:       a.config.Model,
		MaxTokens:   a.config.MaxTokens,
		Temperature: a.config.Temperature,
		Stream:      true,
	}

	chunkChan, err := a.streamingRequest(ctx, streamConfig, messages)
	if err != nil {
		return "", fmt.Errorf("streaming request: %w", err)
	}

	// Собираем ответ с reasoning
	responseText, reasoningText, err := a.collectStreamResponse(chunkChan)
	if err != nil {
		return "", err
	}

	// Проверяем на XML tool calls в reasoning
	if reasoningText != "" {
		parsed := ParseXMLToolCalls(reasoningText)
		if len(parsed.ToolCalls) > 0 {
			// Есть XML tool calls - нужно переключиться на processWithTools
			result, err := a.processWithTools(ctx, messages, session, 5)
			if err != nil {
				return "", err
			}
			return result.Response, nil
		}
	}

	// Отправляем очищенный reasoning в thinkingPeerID (без XML тегов)
	if reasoningText != "" && a.thinkingCallback != nil {
		cleanedReasoning := reasoningText
		parsed := ParseXMLToolCalls(reasoningText)
		if len(parsed.ToolCalls) > 0 {
			cleanedReasoning = parsed.Content
		}
		if cleanedReasoning != "" {
			if err := a.thinkingCallback(session.GetPeerID(), cleanedReasoning); err != nil {
				fmt.Printf("[WARN] Failed to send thinking message: %v\n", err)
			}
		}
	}

	// Если reasoning есть но response пустой — возвращаем reasoning
	if responseText == "" && reasoningText != "" {
		session.AddAssistantMessage(reasoningText)
		return reasoningText, nil
	}

	session.AddAssistantMessage(responseText)
	return responseText, nil
}

// ============================================================
// Утилиты для конвертации
// ============================================================

// convertHistoryToAPIMessages конвертирует историю сессии в формат API
func (a *agentImpl) convertHistoryToAPIMessages(history []session.Message) []Message {
	apiMessages := make([]Message, len(history))
	for i, msg := range history {
		apiMsg := Message{
			Role:       string(msg.Role),
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
			Name:       msg.Name,
		}
		// Конвертируем tool_calls если есть
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
