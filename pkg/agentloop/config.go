package agentloop

import (
	"time"

	"github.com/opencode/llama-client/pkg/compress"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

// ============================================================
// Конфигурация AgentLoop
// ============================================================

// LoopConfig содержит все настройки для AgentLoop
type LoopConfig struct {
	// LLM Configuration
	LlamaServerURL string
	Model          string
	MaxTokens      int
	Temperature    float64

	// Session Management
	SessionConfig  session.Config
	SystemPromptFile string // Путь к файлу системного промпта

	// Loop Detection
	EnableLoopDetection bool
	LoopThreshold       float64 // 0.0-1.0, similarity threshold

	// Tool Processing
	EnableTools  bool
	MaxToolCalls int
	ToolTimeout  time.Duration

	// Thinking Messages
	ThinkingPeerID int64
	EnableThinking bool

	// Logging
	EnableLogging bool
	Logger        Logger

	// Debug
	Debug bool // Сохранять промпт в debug_prompt.txt

	// Context Compression (Legacy - для совместимости)
	EnableCompression         bool
	CompressionStrategy       compress.CompressionStrategy
	CompressionTokenThreshold int
	CompressionPercentageThreshold float64

	// New Compaction Settings
	CompactionConfig compress.CompactionConfig

	// Opencode-style Compaction
	EnableOpenCodeCompaction bool   // использовать opencode-алгоритм
	TailTurns                int    // сколько последних user-оборотов сохранять (default: 2)
	PreserveRecentTokens     *int   // бюджет токенов для хвоста (default: 25% от usable, 2000-8000)
	CompactionReserved       *int   // резерв токенов для ответа компакшена (default: 20000)
	EnablePruning            bool   // pruning старых tool output-ов
	AutoContinueAfterCompact bool   // авто-Continue после компакшена

	// Artifact Store Path - путь для хранения больших результатов
	ArtifactStorePath string
}

// DefaultLoopConfig возвращает конфигурацию по умолчанию
func DefaultLoopConfig() LoopConfig {
	return LoopConfig{
		LlamaServerURL:            "127.0.0.1:8081",
		Model:                     "local-model",
		MaxTokens:                 4096,
		Temperature:               0.7,
		SessionConfig:             session.DefaultConfig(),
		EnableLoopDetection:       true,
		LoopThreshold:             0.85,
		EnableTools:               true,
		MaxToolCalls:              5,
		ToolTimeout:               30 * time.Second,
		ThinkingPeerID:            0,
		EnableThinking:            false,
		EnableLogging:             true,
		EnableCompression:         true,
		CompressionStrategy:       compress.SummarizeStrategy,
		CompressionTokenThreshold: 6000,
		CompressionPercentageThreshold: 0.75,
		CompactionConfig:          compress.DefaultCompactionConfig(),
		EnableOpenCodeCompaction:  true,
		TailTurns:                 2,
		PreserveRecentTokens:      nil,
		CompactionReserved:        nil,
		EnablePruning:             true,
		AutoContinueAfterCompact:  true,
		ArtifactStorePath:         "./artifacts",
	}
}

// ============================================================
// Интерфейсы для зависимостей
// ============================================================

// Logger — интерфейс логгера для AgentLoop
type Logger interface {
	DebugLog(msg string, args ...interface{})
	InfoLog(msg string, args ...interface{})
	WarnLog(msg string, args ...interface{})
	ErrorLog(msg string, args ...interface{})
	// Formatted versions
	DebugLogf(format string, args ...interface{})
	InfoLogf(format string, args ...interface{})
	WarnLogf(format string, args ...interface{})
	ErrorLogf(format string, args ...interface{})
}

// VKClient — интерфейс VK API клиента
type VKClient interface {
	SendMessage(peerID int64, text string) (int64, error)
	SendThinking(peerID int64, content string) (int64, error)
}

// ToolRegistry — интерфейс реестра инструментов
type ToolRegistry interface {
	Get(name string) (tools.Tool, bool)
	ToOpenAISchema() []map[string]interface{}
}

// Compressor — интерфейс компрессора контекста
type Compressor interface {
	Compress(ctx interface{}, req interface{}) (interface{}, error)
	CheckTrigger(currentTokens, maxTokens int) bool
	Name() string
}
