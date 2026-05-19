package tokenizers

import (
	"errors"
	"testing"
)

// ============================================================
// Тесты ContextSize
// ============================================================

func TestContextSizeAddCompletion(t *testing.T) {
	t.Run("adds completion tokens correctly", func(t *testing.T) {
		cs := &ContextSize{
			PromptTokens:     100,
			CompletionTokens: 0,
			TotalTokens:      100,
		}
		cs.AddCompletion(50)

		if cs.CompletionTokens != 50 {
			t.Errorf("expected CompletionTokens 50, got %d", cs.CompletionTokens)
		}
		if cs.TotalTokens != 150 {
			t.Errorf("expected TotalTokens 150, got %d", cs.TotalTokens)
		}
	})
}

func TestContextSizeIsWithinLimit(t *testing.T) {
	t.Run("is within limit when tokens fit", func(t *testing.T) {
		cs := &ContextSize{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
			MaxContextLength: 200,
		}
		cs.AddCompletion(0)
		if !cs.IsWithinLimit {
			t.Error("expected IsWithinLimit to be true")
		}
	})

	t.Run("is not within limit when tokens overflow", func(t *testing.T) {
		cs := &ContextSize{
			PromptTokens:     100,
			CompletionTokens: 150,
			TotalTokens:      250,
			MaxContextLength: 200,
		}
		cs.AddCompletion(0)
		if cs.IsWithinLimit {
			t.Error("expected IsWithinLimit to be false")
		}
	})
}

func TestEstimateWithContext(t *testing.T) {
	t.Run("creates context with correct values", func(t *testing.T) {
		cs := EstimateWithContext(100, 50, 200)
		if cs.PromptTokens != 100 {
			t.Errorf("expected PromptTokens 100, got %d", cs.PromptTokens)
		}
		if cs.CompletionTokens != 50 {
			t.Errorf("expected CompletionTokens 50, got %d", cs.CompletionTokens)
		}
		if cs.TotalTokens != 150 {
			t.Errorf("expected TotalTokens 150, got %d", cs.TotalTokens)
		}
		if !cs.IsWithinLimit {
			t.Error("expected IsWithinLimit to be true")
		}
	})
}

func TestEstimatePromptTokens(t *testing.T) {
	t.Run("counts tokens in multiple texts", func(t *testing.T) {
		mock := &mockTokenizer{count: 10}
		texts := []string{"text1", "text2", "text3"}
		count, err := EstimatePromptTokens(texts, mock)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if count != 30 {
			t.Errorf("expected 30 tokens, got %d", count)
		}
	})

	t.Run("returns error when tokenizer fails", func(t *testing.T) {
		mock := &mockTokenizer{count: 0, err: assertError("tokenizer error")}
		_, err := EstimatePromptTokens([]string{"text"}, mock)
		if err == nil {
			t.Error("expected error")
		}
	})
}

func TestEstimateCompletionTokens(t *testing.T) {
	t.Run("returns max tokens as estimate", func(t *testing.T) {
		estimate := EstimateCompletionTokens(100)
		if estimate != 100 {
			t.Errorf("expected 100, got %d", estimate)
		}
	})
}

func TestMessageString(t *testing.T) {
	msg := Message{Role: "user", Content: "Hello world"}
	str := msg.String()
	if str != "[user] Hello world" {
		t.Errorf("expected '[user] Hello world', got '%s'", str)
	}
}

// ============================================================
// Mock для тестирования
// ============================================================

type mockTokenizer struct {
	count int
	err   error
}

func (m *mockTokenizer) CountTokens(text string) (int, error) {
	return m.count, m.err
}

func (m *mockTokenizer) CountMessagesTokens(messages []Message) (int, error) {
	return m.count * len(messages), m.err
}

func (m *mockTokenizer) Encode(text string) ([]int, error) {
	return nil, nil
}

func (m *mockTokenizer) Decode(tokens []int) (string, error) {
	return "", nil
}

func (m *mockTokenizer) MaxContextLength() int {
	return 2000
}

func (m *mockTokenizer) Name() string {
	return "mock"
}

func assertError(msg string) error {
	return errors.New(msg)
}
