package agent

import (
	"context"

	"github.com/opencode/llama-client/session"
)

// ============================================================
// AI Agent Interface — интерфейс для AI агента
// ============================================================

// Agent определяет интерфейс для AI агента
type Agent interface {
	// ProcessMessage обрабатывает сообщение пользователя и возвращает ответ
	ProcessMessage(ctx context.Context, message string, peerID int64) (string, error)

	// ResetSession сбрасывает сессию пользователя
	ResetSession(peerID int64)

	// GetSession возвращает сессию пользователя
	GetSession(peerID int64) *session.Session

	// SetThinkingCallback устанавливает callback для отправки thinking сообщений
	SetThinkingCallback(cb ThinkingCallback)

	// SetTools регистрирует инструменты из реестра
	SetTools(tools []map[string]interface{})
}

// ============================================================
// AI Agent Configuration
// ============================================================

// Config содержит настройки AI агента
type Config struct {
	// LlamaServerURL — адрес llama-server
	LlamaServerURL string
	// Model — имя модели
	Model string
	// MaxTokens — максимальное количество токенов в ответе
	MaxTokens int
	// Temperature — температура генерации
	Temperature float64
	// SessionConfig — конфигурация сессии
	SessionConfig session.Config
	// SystemPromptFile — путь к файлу системного промпта (если пустой — используется дефолтный)
	SystemPromptFile string
	// EnableLoopAlert — включать alert при обнаружении цикла
	EnableLoopAlert bool
	// EnableTools — использовать инструменты (function calling)
	EnableTools bool
	// MaxToolCalls — максимальное количество вызовов инструментов за один запрос
	MaxToolCalls int
	// EnableContextCompression — включать автоматическое сжатие контекста
	EnableContextCompression bool
	// CompressionStrategy — стратегия сжатия (summarize, truncate, hybrid)
	CompressionStrategy string
	// CompressionTokenThreshold — порог в токенах для сжатия
	CompressionTokenThreshold int
	// CompressionPercentageThreshold — порог в процентах (0.0-1.0)
	CompressionPercentageThreshold float64
}

// DefaultConfig возвращает конфигурацию по умолчанию
func DefaultConfig() Config {
	return Config{
		LlamaServerURL:                "127.0.0.1:8081",
		Model:                         "local-model",
		MaxTokens:                     4096,
		Temperature:                   0.7,
		SessionConfig:                 session.DefaultConfig(),
		EnableLoopAlert:               true,
		EnableTools:                   true,
		MaxToolCalls:                  5,
		EnableContextCompression:      true,
		CompressionStrategy:           "summarize",
		CompressionTokenThreshold:     6000,
		CompressionPercentageThreshold: 0.75,
	}
}
