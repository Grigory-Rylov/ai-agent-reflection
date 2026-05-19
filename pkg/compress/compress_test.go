package compress

import (
	"context"
	"testing"
	"time"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// Тесты CompressionResult
// ============================================================

func TestCalculateCompressionRatio(t *testing.T) {
	t.Run("normal ratio", func(t *testing.T) {
		ratio := CalculateCompressionRatio(1000, 500)
		if ratio != 0.5 {
			t.Errorf("expected 0.5, got %f", ratio)
		}
	})

	t.Run("original is 0", func(t *testing.T) {
		ratio := CalculateCompressionRatio(0, 100)
		if ratio != 1.0 {
			t.Errorf("expected 1.0, got %f", ratio)
		}
	})
}

func TestFormatCompressionReport(t *testing.T) {
	result := &CompressionResult{
		OriginalTokens:     1000,
		CompressedTokens:   500,
		CompressionRatio:   0.5,
		Summary:            "conversation summary",
		CompressedAt:       time.Now(),
	}

	report := FormatCompressionReport(result)
	// Проверяем что есть основные поля
	if len(report) == 0 {
		t.Error("report should not be empty")
	}
	// Проверяем что есть информация о токенах
	expected := "Сжатие контекста: 1000 → 500 токенов (соотношение: 50.0%) [резюме: conversation summary]"
	if report != expected {
		t.Errorf("expected '%s', got '%s'", expected, report)
	}
}

func TestDefaultCompressionTrigger(t *testing.T) {
	trigger := DefaultCompressionTrigger()
	if trigger.TokenThreshold != 6000 {
		t.Errorf("expected TokenThreshold 6000, got %d", trigger.TokenThreshold)
	}
	if trigger.PercentageThreshold != 0.75 {
		t.Errorf("expected PercentageThreshold 0.75, got %f", trigger.PercentageThreshold)
	}
}

func TestShouldCompress(t *testing.T) {
	trigger := DefaultCompressionTrigger()

	t.Run("should compress when tokens exceed threshold", func(t *testing.T) {
		if !ShouldCompress(7000, 8192, trigger) {
			t.Error("expected should compress")
		}
	})

	t.Run("should compress when percentage exceeds threshold", func(t *testing.T) {
		if !ShouldCompress(6500, 8192, trigger) {
			t.Error("expected should compress")
		}
	})

	t.Run("should not compress when below threshold", func(t *testing.T) {
		if ShouldCompress(5000, 8192, trigger) {
			t.Error("expected should not compress")
		}
	})
}

// ============================================================
// Тесты ContextManager
// ============================================================

func TestNewContextManager(t *testing.T) {
	compressor := &mockCompressor{}
	tokenizer := &mockTokenizer{}
	trigger := DefaultCompressionTrigger()

	manager := NewContextManager(compressor, tokenizer, trigger)
	if manager == nil {
		t.Fatal("manager should not be nil")
	}
	if manager.peerContexts == nil {
		t.Error("peerContexts should not be nil")
	}
}

func TestContextManagerGetPeerContext(t *testing.T) {
	compressor := &mockCompressor{}
	tokenizer := &mockTokenizer{}
	manager := NewContextManager(compressor, tokenizer, DefaultCompressionTrigger())

	// Первый вызов создаёт новый контекст
	ctx1 := manager.getPeerContext(123)
	if ctx1 == nil {
		t.Fatal("peer context should not be nil")
	}

	// Второй вызов возвращает тот же контекст
	ctx2 := manager.getPeerContext(123)
	if ctx2 != ctx1 {
		t.Error("expected same peer context")
	}
}

func TestContextManagerUpdateTokens(t *testing.T) {
	compressor := &mockCompressor{}
	tokenizer := &mockTokenizer{}
	manager := NewContextManager(compressor, tokenizer, DefaultCompressionTrigger())

	manager.UpdateTokens(123, 1000)
	tokens := manager.GetTokens(123)
	if tokens != 1000 {
		t.Errorf("expected 1000 tokens, got %d", tokens)
	}
}

func TestContextManagerClearPeerContext(t *testing.T) {
	compressor := &mockCompressor{}
	tokenizer := &mockTokenizer{}
	manager := NewContextManager(compressor, tokenizer, DefaultCompressionTrigger())

	manager.UpdateTokens(123, 1000)
	manager.ClearPeerContext(123)

	tokens := manager.GetTokens(123)
	if tokens != 0 {
		t.Errorf("expected 0 tokens after clear, got %d", tokens)
	}
}

// ============================================================
// Mock для тестов
// ============================================================

type mockCompressor struct {
	checkTriggerFunc func(int, int) bool
}

func (m *mockCompressor) Compress(ctx context.Context, req *CompressionRequest) (*CompressionResult, error) {
	return &CompressionResult{
		OriginalTokens:     1000,
		CompressedTokens:   500,
		CompressionRatio:   0.5,
		CompressedMessages: nil,
		Summary:            "mock summary",
		CompressedAt:       time.Now(),
	}, nil
}

func (m *mockCompressor) CheckTrigger(currentTokens, maxTokens int) bool {
	if m.checkTriggerFunc != nil {
		return m.checkTriggerFunc(currentTokens, maxTokens)
	}
	return currentTokens > 6000
}

func (m *mockCompressor) Name() string {
	return "mock"
}

type mockTokenizer struct {
	countFunc func(string) (int, error)
}

func (m *mockTokenizer) CountTokens(text string) (int, error) {
	if m.countFunc != nil {
		return m.countFunc(text)
	}
	return len(text), nil
}

func (m *mockTokenizer) CountMessagesTokens(messages []tokenizers.Message) (int, error) {
	total := 0
	for _, msg := range messages {
		count, err := m.CountTokens(msg.Content)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func (m *mockTokenizer) Encode(text string) ([]int, error) {
	return nil, nil
}

func (m *mockTokenizer) Decode(tokens []int) (string, error) {
	return "", nil
}

func (m *mockTokenizer) MaxContextLength() int {
	return 8192
}

func (m *mockTokenizer) Name() string {
	return "mock"
}
