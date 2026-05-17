package compress

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// Compressor Interface — интерфейс для сжатия контекста
// ============================================================

// CompressionStrategy определяет стратегию сжатия
type CompressionStrategy string

const (
	// SummarizeStrategy — суммаризовать историю
	SummarizeStrategy CompressionStrategy = "summarize"
	// TruncateStrategy — обрезать старые сообщения
	TruncateStrategy CompressionStrategy = "truncate"
	// HybridStrategy — гибридная (сначала суммаризация, потом обрезка)
	HybridStrategy CompressionStrategy = "hybrid"
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

// CompressionTrigger определяет когда нужно сжать контекст
type CompressionTrigger struct {
	// TokenThreshold — порог в токенах
	TokenThreshold int
	// PercentageThreshold — порог в процентах (0.0-1.0)
	PercentageThreshold float64
}

// Compressor определяет интерфейс для сжатия контекста
type Compressor interface {
	// Compress сжимает контекст
	Compress(ctx context.Context, req *CompressionRequest) (*CompressionResult, error)
	// CheckTrigger проверяет нужно ли сжимать контекст
	CheckTrigger(currentTokens, maxTokens int) bool
	// Name возвращает имя компрессора
	Name() string
}

// ============================================================
// Утилиты
// ============================================================

// CalculateCompressionRatio вычисляет соотношение сжатия
func CalculateCompressionRatio(original, compressed int) float64 {
	if original == 0 {
		return 1.0
	}
	return float64(compressed) / float64(original)
}

// FormatCompressionReport форматирует отчёт о сжатии
func FormatCompressionReport(result *CompressionResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Сжатие контекста: %d → %d токенов",
		result.OriginalTokens, result.CompressedTokens))
	sb.WriteString(fmt.Sprintf(" (соотношение: %.1f%%)", result.CompressionRatio*100))

	if result.Summary != "" {
		sb.WriteString(fmt.Sprintf(" [резюме: %s]", result.Summary))
	}

	return sb.String()
}

// DefaultCompressionTrigger возвращает триггер по умолчанию
func DefaultCompressionTrigger() CompressionTrigger {
	return CompressionTrigger{
		TokenThreshold:        6000,
		PercentageThreshold:   0.75,
	}
}

// ShouldCompress проверяет нужно ли сжимать контекст
func ShouldCompress(currentTokens, maxTokens int, trigger CompressionTrigger) bool {
	if currentTokens >= trigger.TokenThreshold {
		return true
	}
	if maxTokens > 0 && float64(currentTokens)/float64(maxTokens) >= trigger.PercentageThreshold {
		return true
	}
	return false
}
