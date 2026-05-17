package tokenizers

import (
	"testing"
)

// ============================================================
// Тесты ContextCounter
// ============================================================

func TestNewContextCounter(t *testing.T) {
	counter := NewContextCounter(&mockTokenizer{}, 8192)
	if counter == nil {
		t.Fatal("counter should not be nil")
	}
	if counter.maxTokens != 8192 {
		t.Errorf("expected maxTokens 8192, got %d", counter.maxTokens)
	}
}

func TestContextCounterSetSystemMessage(t *testing.T) {
	counter := NewContextCounter(&mockTokenizer{}, 8192)
	counter.SetSystemMessage("You are a helpful assistant")
	if counter.systemMsg != "You are a helpful assistant" {
		t.Errorf("expected system message, got '%s'", counter.systemMsg)
	}
}

func TestContextCounterCountMessageTokens(t *testing.T) {
	counter := NewContextCounter(&mockTokenizer{count: 5}, 8192)

	tokens, err := counter.CountMessageTokens("user", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokens <= 2 {
		t.Error("expected more than 2 tokens (role + content)")
	}
}

func TestContextCounterCountFullContext(t *testing.T) {
	counter := NewContextCounter(&mockTokenizer{count: 5}, 8192)
	counter.SetSystemMessage("System prompt")

	messages := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
	}

	stats, err := counter.CountFullContext(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats == nil {
		t.Fatal("stats should not be nil")
	}
	if len(stats.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(stats.Messages))
	}
	if stats.MaxTokens != 8192 {
		t.Errorf("expected MaxTokens 8192, got %d", stats.MaxTokens)
	}
}

func TestContextCounterShouldTruncate(t *testing.T) {
	t.Run("returns false for healthy context", func(t *testing.T) {
		counter := NewContextCounter(&mockTokenizer{count: 1}, 8192)
		counter.SetSystemMessage("System")

		messages := []Message{{Role: "user", Content: "Hello"}}
		stats, _ := counter.CountFullContext(messages)

		if counter.ShouldTruncate(stats) {
			t.Error("should not truncate healthy context")
		}
	})

	t.Run("returns true when context is full", func(t *testing.T) {
		counter := NewContextCounter(&mockTokenizer{count: 4000}, 8192)
		counter.SetSystemMessage("System")

		messages := []Message{
			{Role: "user", Content: "Long text"},
			{Role: "assistant", Content: "Long response"},
		}
		stats, _ := counter.CountFullContext(messages)

		if !counter.ShouldTruncate(stats) {
			t.Error("should truncate when context is full")
		}
	})
}

func TestFormatStats(t *testing.T) {
	t.Run("formats healthy context", func(t *testing.T) {
		stats := &ContextStats{
			TotalTokens: 100,
			MaxTokens:   8192,
		}
		formatted := FormatStats(stats)
		if formatted != "Контекст: 100/8192 токенов" {
			t.Errorf("unexpected format: %s", formatted)
		}
	})

	t.Run("includes system tokens", func(t *testing.T) {
		stats := &ContextStats{
			TotalTokens:  200,
			MaxTokens:    8192,
			SystemTokens: 100,
		}
		formatted := FormatStats(stats)
		if formatted != "Контекст: 200/8192 токенов (система: 100)" {
			t.Errorf("unexpected format: %s", formatted)
		}
	})

	t.Run("returns default for nil stats", func(t *testing.T) {
		formatted := FormatStats(nil)
		if formatted != "Нет данных о контексте" {
			t.Errorf("unexpected format: %s", formatted)
		}
	})
}
