package compress

import (
	"context"
	"errors"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
)

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

type stubCompressor struct{}

func (s *stubCompressor) Compress(ctx context.Context, req *CompressionRequest) (*CompressionResult, error) {
	return &CompressionResult{Summary: "summary"}, nil
}

func (s *stubCompressor) Complete(ctx context.Context, systemPrompt, userPrompt string, maxTokens int) (string, error) {
	return "summary", nil
}
