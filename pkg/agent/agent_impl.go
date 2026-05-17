package agent

import (
	"context"
	"fmt"
	"net/http"
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

// ============================================================
// AI Agent Implementation — реализация агента с подключением к llama-server
// ============================================================

// agentImpl реализует интерфейс AI агента с подключением к llama-server
type agentImpl struct {
	config          Config
	sessions        map[int64]*session.Session
	toolsRegistry   *tools.Registry
	mu              sync.RWMutex
	client          *http.Client
	contextManager  *compress.ContextManager
	tokenizer       tokenizers.Tokenizer
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
		},
	}

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
	// File read tool
	a.toolsRegistry.Register(&FileReadTool{})

	// File write tool
	a.toolsRegistry.Register(&FileWriteTool{})

	// Time get tool
	a.toolsRegistry.Register(&TimeGetTool{})

	// Dir list tool
	a.toolsRegistry.Register(&DirListTool{})
}

// ============================================================
// Методы Agent Interface
// ============================================================

// ProcessMessage обрабатывает сообщение пользователя и возвращает ответ
func (a *agentImpl) ProcessMessage(ctx context.Context, message string, peerID int64) (string, error) {
	// Получаем или создаём сессию
	s := a.getSession(peerID)

	// Проверяем, не зациклилась ли AI
	if s.IsLoopDetected() {
		alert := s.GetLoopAlertMessage()
		if alert != "" {
			message = "[LOOP DETECTED] " + alert + "\n\n" + message
		}
	}

	// Добавляем сообщение пользователя в сессию
	s.AddUserMessage(message)

	// Получаем историю для отправки в API
	history := s.GetHistory()

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
			config.SystemPrompt = "You are a helpful assistant."
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

	// Собираем ответ
	var fullResponse strings.Builder
	for event := range chunkChan {
		if event.IsDone {
			break
		}
		if event.Content != "" {
			fullResponse.WriteString(event.Content)
		}
	}

	responseText := fullResponse.String()
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
		apiMessages[i] = Message{
			Role:    string(msg.Role),
			Content: msg.Content,
		}
	}
	return apiMessages
}
