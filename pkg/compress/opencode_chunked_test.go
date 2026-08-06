package compress

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// Chunked compaction: head больше контекста должен суммироваться
// по кускам, каждый из которых укладывается в доступный контекст.
// ============================================================

func TestSummaryChunkBudget(t *testing.T) {
	t.Run("large context keeps room for summary output", func(t *testing.T) {
		got := summaryChunkBudget(200_000)
		usable := Usable(200_000, nil) // 180_000
		if got >= usable {
			t.Errorf("budget %d should leave room below usable %d", got, usable)
		}
		if got <= 0 {
			t.Errorf("budget %d should be positive", got)
		}
	})

	t.Run("tiny context gets positive budget", func(t *testing.T) {
		if got := summaryChunkBudget(4096); got <= 0 {
			t.Errorf("budget %d should be positive for small context", got)
		}
	})
}

func TestTakeOldestFit(t *testing.T) {
	msgs := []tokenizers.Message{
		{Role: "user", Content: createLongOutput(100)}, // ~25 tokens
		{Role: "user", Content: createLongOutput(100)}, // ~25 tokens
		{Role: "user", Content: createLongOutput(100)}, // ~25 tokens
		{Role: "user", Content: createLongOutput(100)}, // ~25 tokens
	}

	t.Run("fits first messages up to budget", func(t *testing.T) {
		taken, rest := takeOldestFit(msgs, 60)
		if len(taken) != 2 {
			t.Errorf("expected 2 taken, got %d", len(taken))
		}
		if len(rest) != 2 {
			t.Errorf("expected 2 rest, got %d", len(rest))
		}
	})

	t.Run("all fit returns no rest", func(t *testing.T) {
		taken, rest := takeOldestFit(msgs, 10_000)
		if len(taken) != len(msgs) {
			t.Errorf("expected all taken, got %d", len(taken))
		}
		if len(rest) != 0 {
			t.Errorf("expected no rest, got %d", len(rest))
		}
	})

	t.Run("empty returns no rest", func(t *testing.T) {
		taken, rest := takeOldestFit(nil, 60)
		if taken != nil || rest != nil {
			t.Errorf("expected nil results, got taken=%v rest=%v", taken, rest)
		}
	})
}

func TestTruncateToBudget(t *testing.T) {
	msg := tokenizers.Message{Role: "user", Content: createLongOutput(100_000)}
	got := truncateToBudget(msg, 10_000)
	if EstimateMessagesTokensSimple([]tokenizers.Message{got}) > 10_000 {
		t.Errorf("truncated message still exceeds budget: %d tokens",
			EstimateMessagesTokensSimple([]tokenizers.Message{got}))
	}
	if !strings.Contains(got.Content, "[truncated") {
		t.Error("expected truncation marker in content")
	}

	t.Run("short content unchanged", func(t *testing.T) {
		m := tokenizers.Message{Role: "user", Content: "hi"}
		if got := truncateToBudget(m, 10_000); got.Content != "hi" {
			t.Errorf("short content changed, got %q", got.Content)
		}
	})
}

func TestCompactWithOpenCode_ChunksLargeHead(t *testing.T) {
	const maxTokens = 200_000
	usable := Usable(maxTokens, nil)

	var calls []*CompressionRequest
	mockLLM := &mockLLMCompressor{
		compressFunc: func(ctx context.Context, req *CompressionRequest) (*CompressionResult, error) {
			calls = append(calls, req)
			summary := fmt.Sprintf("SUMMARY-v%d", len(calls))
			return &CompressionResult{
				Summary: summary,
				CompressedMessages: []tokenizers.Message{
					{Role: "user", Content: summary},
				},
			}, nil
		},
	}
	compactor := NewCompactor(mockLLM)

	// Большой head: 60 ходов по ~50K символов (~12.5K токенов) => ~750K токенов.
	var msgs []tokenizers.Message
	for i := 0; i < 60; i++ {
		msgs = append(msgs, tokenizers.Message{Role: "user", Content: createLongOutput(50_000)})
		msgs = append(msgs, tokenizers.Message{Role: "assistant", Content: "resp"})
	}
	msgs = append(msgs, tokenizers.Message{Role: "user", Content: "recent turn"})
	msgs = append(msgs, tokenizers.Message{Role: "assistant", Content: "recent resp"})

	result, err := compactor.CompactWithOpenCode(nil, msgs, maxTokens, 1, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(calls) < 2 {
		t.Fatalf("expected multiple summarization calls for large head, got %d", len(calls))
	}

	for i, req := range calls {
		promptTokens := EstimateMessagesTokensSimple(req.Messages)
		if promptTokens > usable {
			t.Errorf("call %d prompt tokens %d exceed usable %d", i, promptTokens, usable)
		}
	}

	if result.Summary != fmt.Sprintf("SUMMARY-v%d", len(calls)) {
		t.Errorf("expected summary from last call, got %q", result.Summary)
	}

	// Накопление: следующий вызов должен содержать summary предыдущего.
	for i := 1; i < len(calls); i++ {
		cur := calls[i].Messages[len(calls[i].Messages)-1].Content
		if !strings.Contains(cur, fmt.Sprintf("SUMMARY-v%d", i)) {
			t.Errorf("call %d should include previous summary SUMMARY-v%d", i+1, i)
		}
	}

	if len(result.KeptTail) == 0 {
		t.Error("expected tail to be preserved")
	}
}

func TestCompactWithOpenCode_NoChunkWhenHeadFits(t *testing.T) {
	var calls []*CompressionRequest
	mockLLM := &mockLLMCompressor{
		compressFunc: func(ctx context.Context, req *CompressionRequest) (*CompressionResult, error) {
			calls = append(calls, req)
			return &CompressionResult{
				Summary:            "[SUMMARY] small",
				CompressedMessages: []tokenizers.Message{{Role: "user", Content: "[SUMMARY] small"}},
			}, nil
		},
	}
	compactor := NewCompactor(mockLLM)

	msgs := []tokenizers.Message{
		{Role: "user", Content: "old turn"},
		{Role: "assistant", Content: "old resp"},
		{Role: "user", Content: "recent turn"},
		{Role: "assistant", Content: "recent resp"},
	}

	result, err := compactor.CompactWithOpenCode(nil, msgs, 200_000, 1, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 1 {
		t.Errorf("expected single call for small head, got %d", len(calls))
	}
	if result.Summary == "" {
		t.Error("expected non-empty summary")
	}
}
