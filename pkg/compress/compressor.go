package compress

import (
	"time"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// Compression types — LLM-сжатие для opencode-компакшена
// ============================================================

// CompressionStrategy определяет стратегию сжатия
type CompressionStrategy string

const (
	// SummarizeStrategy — суммаризовать историю
	SummarizeStrategy CompressionStrategy = "summarize"
)

// CompressionRequest запрос на сжатие
type CompressionRequest struct {
	// Messages — текущие сообщения для сжатия
	Messages []tokenizers.Message
	// Strategy — стратегия сжатия
	Strategy CompressionStrategy
	// TargetTokens — целевое количество токенов после сжатия
	TargetTokens int
	// MaxCompressionRatio — максимальное соотношение сжатия (0.0-1.0)
	MaxCompressionRatio float64
}

// CompressionResult результат сжатия
type CompressionResult struct {
	// OriginalTokens — количество токенов до сжатия
	OriginalTokens int
	// CompressedTokens — количество токенов после сжатия
	CompressedTokens int
	// CompressionRatio — соотношение сжатия
	CompressionRatio float64
	// CompressedMessages — сжатые сообщения
	CompressedMessages []tokenizers.Message
	// Summary — текстовое резюме (если использовалась суммаризация)
	Summary string
	// CompressedAt — время сжатия
	CompressedAt time.Time
}

// CalculateCompressionRatio вычисляет соотношение сжатия
func CalculateCompressionRatio(original, compressed int) float64 {
	if original == 0 {
		return 1.0
	}
	return float64(compressed) / float64(original)
}
