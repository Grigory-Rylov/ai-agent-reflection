package compress

import (
	"context"
	"errors"
	"testing"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// MockTokenizer — test stub for tokenizers.Tokenizer
// ============================================================

type mockTokenizer struct {
	countTokens    func(text string) (int, error)
	countMsgTokens func(messages []tokenizers.Message) (int, error)
	maxContext     int
	name           string
}

func (m *mockTokenizer) CountTokens(text string) (int, error) {
	if m.countTokens != nil {
		return m.countTokens(text)
	}
	return 0, nil
}

func (m *mockTokenizer) CountMessagesTokens(messages []tokenizers.Message) (int, error) {
	if m.countMsgTokens != nil {
		return m.countMsgTokens(messages)
	}
	return 0, nil
}

func (m *mockTokenizer) Encode(text string) ([]int, error) {
	return nil, nil
}

func (m *mockTokenizer) Decode(tokens []int) (string, error) {
	return "", nil
}

func (m *mockTokenizer) MaxContextLength() int {
	return m.maxContext
}

func (m *mockTokenizer) Name() string {
	return m.name
}

// ============================================================
// Test: RealEstimator uses tokenizer for EstimateMessages
// ============================================================

func TestRealEstimator_EstimateMessages(t *testing.T) {
	msgs := []tokenizers.Message{
		{Role: "user", Content: "Hello world"},
		{Role: "assistant", Content: "Hi there!"},
	}

	mock := &mockTokenizer{
		countTokens: func(text string) (int, error) {
			return len(text) / 2, nil
		},
		countMsgTokens: func(messages []tokenizers.Message) (int, error) {
			return 37, nil
		},
	}

	est := NewRealEstimator(mock)

	t.Run("EstimateMessages uses CountMessagesTokens", func(t *testing.T) {
		got := est.EstimateMessages(msgs)
		if got != 37 {
			t.Errorf("EstimateMessages() = %d, want 37", got)
		}
	})

	t.Run("Estimate uses CountTokens", func(t *testing.T) {
		got := est.Estimate("Hello")
		want := len("Hello") / 2
		if got != want {
			t.Errorf("Estimate() = %d, want %d", got, want)
		}
	})
}

// ============================================================
// Test: RealEstimator returns 0 on error
// ============================================================

func TestRealEstimator_ReturnsZeroOnError(t *testing.T) {
	mock := &mockTokenizer{
		countTokens: func(text string) (int, error) {
			return 0, errors.New("fail")
		},
		countMsgTokens: func(messages []tokenizers.Message) (int, error) {
			return 0, errors.New("fail")
		},
	}

	est := NewRealEstimator(mock)

	t.Run("Estimate returns 0 on error", func(t *testing.T) {
		got := est.Estimate("test")
		if got != 0 {
			t.Errorf("Estimate() = %d, want 0", got)
		}
	})

	t.Run("EstimateMessages returns 0 on error", func(t *testing.T) {
		got := est.EstimateMessages([]tokenizers.Message{{Role: "user", Content: "test"}})
		if got != 0 {
			t.Errorf("EstimateMessages() = %d, want 0", got)
		}
	})
}

// ============================================================
// Test: Compare usable/isOverflow with real tokenizer vs heuristic
// ============================================================

func TestOverflow_RealTokenizerVsHeuristic(t *testing.T) {
	tests := []struct {
		name           string
		messages       []tokenizers.Message
		tokenizerCount int // what mock tokenizer returns
		contextLimit   int
		inputLimit     int
		reserved       int
	}{
		{
			name: "real tokenizer shows no overflow, heuristic does",
			messages: []tokenizers.Message{
				{Role: "user", Content: string(make([]byte, 50000))}, // large content
			},
			tokenizerCount: 5000, // real count is much less than heuristic
			contextLimit:   128000,
			inputLimit:     0,
			reserved:       0,
		},
		{
			name: "both agree on overflow",
			messages: []tokenizers.Message{
				{Role: "user", Content: string(make([]byte, 200000))},
			},
			tokenizerCount: 100000,
			contextLimit:   32000,
			inputLimit:     0,
			reserved:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockTokenizer{
				countMsgTokens: func(messages []tokenizers.Message) (int, error) {
					return tt.tokenizerCount, nil
				},
			}

			heuristicTokens := EstimateMessagesTokensSimple(tt.messages)
			realTokens := NewRealEstimator(mock).EstimateMessages(tt.messages)

			reservedPtr := tt.reserved

			overflowHeuristic := IsOverflowWithLimits(heuristicTokens, tt.contextLimit, tt.inputLimit, &reservedPtr)
			overflowReal := IsOverflowWithLimits(realTokens, tt.contextLimit, tt.inputLimit, &reservedPtr)

			t.Logf("heuristic=%d, real=%d, overflow(heuristic)=%v, overflow(real)=%v",
				heuristicTokens, realTokens, overflowHeuristic, overflowReal)

			usable := UsableWithLimits(tt.contextLimit, tt.inputLimit, &reservedPtr)
			if realTokens >= usable {
				if !overflowReal {
					t.Errorf("expected overflow with real tokens %d >= usable %d", realTokens, usable)
				}
			} else {
				if overflowReal {
					t.Errorf("expected no overflow with real tokens %d < usable %d", realTokens, usable)
				}
			}
		})
	}
}

// ============================================================
// Test: Provider-reported token count influences compaction decision
// ============================================================

func TestIsOverflowWithProviderTokens_InfluencesDecision(t *testing.T) {
	tests := []struct {
		name         string
		tokens       ProviderTokens
		contextLimit int
		inputLimit   int
		reserved     int
		wantOverflow bool
	}{
		{
			name:         "total tokens just below usable — no overflow",
			tokens:       ProviderTokens{Total: 95999},
			contextLimit: 128000,
			inputLimit:   0,
			reserved:     0,
			wantOverflow: false,
		},
		{
			name:         "total tokens at usable — overflow",
			tokens:       ProviderTokens{Total: 96000},
			contextLimit: 128000,
			inputLimit:   0,
			reserved:     0,
			wantOverflow: true,
		},
		{
			name:         "input+output+cache exceeds usable — overflow",
			tokens:       ProviderTokens{Input: 50000, Output: 50000, CacheRead: 10000},
			contextLimit: 128000,
			inputLimit:   0,
			reserved:     0,
			wantOverflow: true,
		},
		{
			name:         "inputLimit with provider tokens — overflow",
			tokens:       ProviderTokens{Total: 110000},
			contextLimit: 128000,
			inputLimit:   120000,
			reserved:     0,
			wantOverflow: true,
		},
		{
			name:         "inputLimit with provider tokens — no overflow",
			tokens:       ProviderTokens{Total: 99999},
			contextLimit: 128000,
			inputLimit:   120000,
			reserved:     0,
			wantOverflow: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reservedPtr := tt.reserved
			got := IsOverflowWithProviderTokens(tt.tokens, tt.contextLimit, tt.inputLimit, &reservedPtr)
			if got != tt.wantOverflow {
				t.Errorf("IsOverflowWithProviderTokens() = %v, want %v (tokens.Count()=%d)",
					got, tt.wantOverflow, tt.tokens.Count())
			}
		})
	}
}

// ============================================================
// Test: Compactor uses tokenizer when available
// ============================================================

func TestCompactor_WithTokenizer(t *testing.T) {
	mock := &mockTokenizer{
		countTokens: func(text string) (int, error) {
			return len(text) / 2, nil
		},
		countMsgTokens: func(messages []tokenizers.Message) (int, error) {
			return 100, nil
		},
	}

	llm := &stubCompressor{}
	c := NewCompactorWithEstimator(llm, NewRealEstimator(mock))

	// Verify the compactor's estimator uses the real tokenizer
	msgs := []tokenizers.Message{
		{Role: "user", Content: "test"},
	}
	tokens := c.estimator.EstimateMessages(msgs)
	if tokens != 100 {
		t.Errorf("Compactor estimator returned %d, want 100", tokens)
	}
}

func TestCompactor_WithoutTokenizer(t *testing.T) {
	llm := &stubCompressor{}
	c := NewCompactor(llm)

	msgs := []tokenizers.Message{
		{Role: "user", Content: "Hello world"},
	}
	tokens := c.estimator.EstimateMessages(msgs)
	want := EstimateMessagesTokensSimple(msgs)
	if tokens != want {
		t.Errorf("Compactor estimator returned %d, want heuristic %d", tokens, want)
	}
}

// ============================================================
// stubCompressor — minimal LLMCompressorInterface for tests
// ============================================================

type stubCompressor struct{}

func (s *stubCompressor) Compress(ctx context.Context, req *CompressionRequest) (*CompressionResult, error) {
	return &CompressionResult{Summary: "summary"}, nil
}
