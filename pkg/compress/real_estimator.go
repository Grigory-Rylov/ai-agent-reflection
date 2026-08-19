package compress

import "github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"


type RealEstimator struct {
	tokenizer tokenizers.Tokenizer
}


func NewRealEstimator(tz tokenizers.Tokenizer) *RealEstimator {
	return &RealEstimator{tokenizer: tz}
}


func (e *RealEstimator) Estimate(text string) int {
	if e.tokenizer == nil {
		return 0
	}
	count, err := e.tokenizer.CountTokens(text)
	if err != nil {
		return 0
	}
	return count
}


func (e *RealEstimator) EstimateMessages(messages []tokenizers.Message) int {
	if e.tokenizer == nil {
		return 0
	}
	count, err := e.tokenizer.CountMessagesTokens(messages)
	if err != nil {
		return 0
	}
	return count
}


