package compress

import (
	"context"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
)


type LLMCompressorInterface interface {
	Compress(ctx context.Context, req *CompressionRequest) (*CompressionResult, error)
}


type Compactor struct {
	estimator TokenEstimator
	llm       LLMCompressorInterface
}


func NewCompactor(llm LLMCompressorInterface) *Compactor {
	return &Compactor{
		estimator: &SimpleEstimator{},
		llm:       llm,
	}
}


func NewCompactorWithEstimator(llm LLMCompressorInterface, estimator TokenEstimator) *Compactor {
	return &Compactor{
		estimator: estimator,
		llm:       llm,
	}
}


func (c *Compactor) LLM() LLMCompressorInterface {
	return c.llm
}


type CompactorInterface interface {
	CompactWithOpenCode(ctx context.Context, messages []tokenizers.Message, maxTokens int, tailTurns int, preserveRecentTokens *int) (*OpenCodeCompactResult, error)
}
