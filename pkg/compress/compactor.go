package compress

import (
	"context"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// Compactor — контекстная компакция в стиле opencode
// ============================================================

// LLMCompressorInterface — интерфейс для LLM-сжатия.
type LLMCompressorInterface interface {
	Compress(ctx context.Context, req *CompressionRequest) (*CompressionResult, error)
}

// Compactor выполняет контекстную компакцию в стиле opencode:
// при переполнении старые сообщения суммируются через LLM,
// а последние tailTurns ходов сохраняются дословно (tail).
type Compactor struct {
	estimator TokenEstimator
	llm       LLMCompressorInterface
}

// NewCompactor создаёт новый компрессор.
func NewCompactor(llm LLMCompressorInterface) *Compactor {
	return &Compactor{
		estimator: &SimpleEstimator{},
		llm:       llm,
	}
}

// NewCompactorWithEstimator creates a compactor with a custom token estimator.
func NewCompactorWithEstimator(llm LLMCompressorInterface, estimator TokenEstimator) *Compactor {
	return &Compactor{
		estimator: estimator,
		llm:       llm,
	}
}

// LLM возвращает внутренний LLM-компрессор (для диагностики и тестов).
func (c *Compactor) LLM() LLMCompressorInterface {
	return c.llm
}

// CompactorInterface — интерфейс компакции, используемый в agentloop.
type CompactorInterface interface {
	CompactWithOpenCode(ctx context.Context, messages []tokenizers.Message, maxTokens int, tailTurns int, preserveRecentTokens *int) (*OpenCodeCompactResult, error)
}
