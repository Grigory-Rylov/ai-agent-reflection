package compress

import "github.com/opencode/llama-client/pkg/tokenizers"

// ============================================================
// RealEstimator — TokenEstimator backed by a real tokenizer
// ============================================================

// RealEstimator implements TokenEstimator using a real tokenizer.
// Falls back to 0 if the tokenizer call fails.
type RealEstimator struct {
	tokenizer tokenizers.Tokenizer
}

// NewRealEstimator creates a RealEstimator backed by the given tokenizer.
func NewRealEstimator(tz tokenizers.Tokenizer) *RealEstimator {
	return &RealEstimator{tokenizer: tz}
}

// Estimate counts tokens using the real tokenizer.
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

// EstimateMessages counts tokens in messages using the real tokenizer.
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


