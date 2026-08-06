package agent

import (
	"context"
	"time"

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

	// SetToolExecutor устанавливает кастомный executor для инструментов
	SetToolExecutor(executor ToolExecutor)
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
	// EnableCompression — включать opencode-style компакцию контекста
	EnableCompression bool
	// TailTurns — сколько последних user-оборотов сохранять при компакции
	TailTurns int
	// PreserveRecentTokens — бюджет для сохранения последних сообщений
	PreserveRecentTokens *int
	// CompactionReserved — резерв токенов для компакции
	CompactionReserved *int
	// ModelLimitInput — model.limit.input из конфигурации модели (0 = не задано)
	ModelLimitInput int
	// EnablePruning — вычищать большие tool-выводы
	EnablePruning bool
	// ToolOutputMaxLines — лимит строк вывода инструмента (0 = дефолт opencode 2000)
	ToolOutputMaxLines int
	// ToolOutputMaxBytes — лимит байт вывода инструмента (0 = дефолт opencode 50KB)
	ToolOutputMaxBytes int
	// Debug — режим отладки (сохранять промпт в debug_prompt.txt)
	Debug bool
	// AgentName — имя агента для логов (coordinator, worker, qa и т.д.)
	AgentName string
	// PromptsDir — директория с шаблонами промптов (если пустая — используется SystemPromptFile)
	PromptsDir string
	// Mode — режим работы агента ("normal", "plan")
	Mode string
	// ToolsList — список имён доступных инструментов (для подстановки в шаблон)
	ToolsList []string
	// SkipShellPermissionForPathless — не спрашивать разрешение для shell-команд
	// без явных файловых операций (нет путей в аргументах и редиректов).
	SkipShellPermissionForPathless bool
	// RetryDelay — пауза между бесконечными ретраями LLM-запроса
	// при серверных ошибках (недоступность, HTTP 5xx, оборванный стрим).
	// Если <= 0 — используется значение по умолчанию (5 сек).
	RetryDelay time.Duration
}

// DefaultConfig возвращает конфигурацию по умолчанию
func DefaultConfig() Config {
	return Config{
		LlamaServerURL:    "127.0.0.1:8081",
		Model:             "local-model",
		MaxTokens:         4096,
		Temperature:       0.7,
		SessionConfig:     session.DefaultConfig(),
		EnableLoopAlert:   true,
		EnableTools:       true,
		MaxToolCalls:      5,
		EnableCompression: true,
		TailTurns:         2,
		EnablePruning:     true,
		RetryDelay:        5 * time.Second,
	}
}
