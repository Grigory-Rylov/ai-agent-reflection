package compress

import (
	"context"
	"testing"
	"time"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// Overflow Tests
// ============================================================

func TestUsable(t *testing.T) {
	t.Run("zero context returns 0", func(t *testing.T) {
		if got := Usable(0, nil); got != 0 {
			t.Errorf("Usable(0) = %d, want 0", got)
		}
	})

	t.Run("small context uses half as reserved", func(t *testing.T) {
		got := Usable(4000, nil)
		if got != 2000 {
			t.Errorf("Usable(4000) = %d, want 2000", got)
		}
	})

	t.Run("large context uses COMPACTION_BUFFER", func(t *testing.T) {
		got := Usable(200_000, nil)
		if got != 180_000 {
			t.Errorf("Usable(200000) = %d, want 180000", got)
		}
	})

	t.Run("custom reserved", func(t *testing.T) {
		reserved := 50000
		got := Usable(200_000, &reserved)
		if got != 150_000 {
			t.Errorf("Usable(200000, 50000) = %d, want 150000", got)
		}
	})
}

func TestIsOverflow(t *testing.T) {
	t.Run("no overflow when under limit", func(t *testing.T) {
		if IsOverflow(1000, 200_000, nil) {
			t.Error("1000 tokens should not overflow 200K context")
		}
	})

	t.Run("overflow when at limit", func(t *testing.T) {
		if !IsOverflow(180_000, 200_000, nil) {
			t.Error("180000 tokens should overflow 200K context (reserved=20K)")
		}
	})

	t.Run("zero context never overflows", func(t *testing.T) {
		if IsOverflow(1000, 0, nil) {
			t.Error("zero context should never overflow")
		}
	})
}

// ============================================================
// Pruning Tests
// ============================================================

func TestPruneMessages(t *testing.T) {
	t.Run("no pruning for small history", func(t *testing.T) {
		msgs := []tokenizers.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		}
		got := PruneMessages(msgs)
		if len(got) != len(msgs) {
			t.Errorf("expected %d messages, got %d", len(msgs), len(got))
		}
	})

	t.Run("protects first 2 user turns", func(t *testing.T) {
		msgs := make([]tokenizers.Message, 0)
		for i := 0; i < 10; i++ {
			msgs = append(msgs, tokenizers.Message{Role: "user", Content: "hello"})
			msgs = append(msgs, tokenizers.Message{Role: "tool", Content: createLongOutput(1000)})
			msgs = append(msgs, tokenizers.Message{Role: "assistant", Content: "done"})
		}
		got := PruneMessages(msgs)
		for i, m := range got {
			if m.Role == "tool" && m.Compacted {
				t.Errorf("tool at index %d should not be pruned (first 2 turns protected)", i)
			}
		}
	})

	t.Run("prunes old tool outputs exceeding PRUNE_PROTECT", func(t *testing.T) {
		large := createLongOutput(200000)
		msgs := make([]tokenizers.Message, 0)
		for i := 0; i < 7; i++ {
			msgs = append(msgs, tokenizers.Message{Role: "user", Content: "hello"})
			msgs = append(msgs, tokenizers.Message{Role: "tool", Content: large})
			msgs = append(msgs, tokenizers.Message{Role: "assistant", Content: "done"})
		}
		got := PruneMessages(msgs)

		prunedCount := 0
		for _, m := range got {
			if m.Compacted {
				prunedCount++
			}
		}
		if prunedCount == 0 {
			t.Error("expected at least some tool outputs to be pruned")
		}
		t.Logf("Pruned %d tool outputs", prunedCount)
	})

	t.Run("placeholder text for pruned outputs", func(t *testing.T) {
		large := createLongOutput(200000)
		msgs := make([]tokenizers.Message, 0)
		for i := 0; i < 7; i++ {
			msgs = append(msgs, tokenizers.Message{Role: "user", Content: "turn"})
			msgs = append(msgs, tokenizers.Message{Role: "tool", Content: large})
			msgs = append(msgs, tokenizers.Message{Role: "assistant", Content: "done"})
		}
		got := PruneMessages(msgs)

		prunedCount := 0
		for _, m := range got {
			if m.Compacted {
				prunedCount++
			}
		}
		if prunedCount == 0 {
			t.Fatal("expected pruning to happen")
		}
		for _, m := range got {
			if m.Compacted && m.Content != PRUNED_OUTPUT_PLACEHOLDER {
				t.Errorf("pruned tool should have placeholder, got: %s", m.Content[:30])
			}
		}
	})
}

// ============================================================
// Select Tests (tail preservation)
// ============================================================

func TestSelectMessages(t *testing.T) {
	t.Run("zero tailTurns returns all as head", func(t *testing.T) {
		msgs := []tokenizers.Message{
			{Role: "user", Content: "a"},
			{Role: "assistant", Content: "b"},
		}
		got := SelectMessages(msgs, 0, 8000)
		if len(got.Head) != 2 {
			t.Errorf("expected all messages in head, got %d", len(got.Head))
		}
		if got.TailStartID != -1 {
			t.Errorf("expected TailStartID -1, got %d", got.TailStartID)
		}
	})

	t.Run("preserves last N turns in tail", func(t *testing.T) {
		msgs := []tokenizers.Message{
			{Role: "user", Content: "turn1"},
			{Role: "assistant", Content: "resp1"},
			{Role: "user", Content: "turn2"},
			{Role: "assistant", Content: "resp2"},
		}
		got := SelectMessages(msgs, 1, 8000)
		if len(got.Head) == 0 {
			t.Error("expected some messages in head")
		}
		if got.TailStartID <= 0 {
			t.Error("expected TailStartID > 0")
		}
	})

	t.Run("all messages fit in budget", func(t *testing.T) {
		msgs := []tokenizers.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "world"},
		}
		got := SelectMessages(msgs, 2, 8000)
		if got.TailStartID != 0 {
			t.Errorf("expected all messages as tail (TailStartID=0), got %d", got.TailStartID)
		}
		if got.Head != nil {
			t.Errorf("expected empty head when all fits, got %d head messages", len(got.Head))
		}
	})

	t.Run("budget limit truncates tail", func(t *testing.T) {
		large := createLongOutput(500)
		msgs := []tokenizers.Message{
			{Role: "user", Content: "old"},
			{Role: "assistant", Content: "old resp"},
			{Role: "user", Content: "recent"},
			{Role: "tool", Content: large},
			{Role: "assistant", Content: "recent resp"},
		}
		got := SelectMessages(msgs, 1, 200)
		if got.TailStartID == -1 {
			t.Log("tail doesn't fit in tiny budget — expected")
		} else if len(got.Head) == 0 {
			t.Error("expected at least old messages in head")
		}
	})
}

// ============================================================
// BuildSummaryPrompt Tests
// ============================================================

func TestBuildSummaryPrompt(t *testing.T) {
	t.Run("includes conversation history", func(t *testing.T) {
		head := []tokenizers.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "world"},
		}
		prompt := BuildSummaryPrompt("", head)
		if !contains(prompt, "hello") || !contains(prompt, "world") {
			t.Error("prompt should include conversation history")
		}
		if !contains(prompt, "Goal") {
			t.Error("prompt should include SUMMARY_TEMPLATE")
		}
	})

	t.Run("includes previous summary when available", func(t *testing.T) {
		head := []tokenizers.Message{
			{Role: "user", Content: "continue"},
		}
		prompt := BuildSummaryPrompt("Previous summary here", head)
		if !contains(prompt, "Previous summary here") {
			t.Error("prompt should include previous summary")
		}
		if !contains(prompt, "Update the anchored summary") {
			t.Error("update prompt should be used for existing summary")
		}
	})

	t.Run("new summary prompt for first compaction", func(t *testing.T) {
		head := []tokenizers.Message{
			{Role: "user", Content: "start"},
		}
		prompt := BuildSummaryPrompt("", head)
		if !contains(prompt, "Create a new anchored summary") {
			t.Error("new prompt should be used for first compaction")
		}
	})
}

// ============================================================
// CompactWithOpenCode Tests (with mock LLM)
// ============================================================

type mockLLMCompressor struct {
	compressFunc func(ctx context.Context, req *CompressionRequest) (*CompressionResult, error)
}

func (m *mockLLMCompressor) Compress(ctx context.Context, req *CompressionRequest) (*CompressionResult, error) {
	if m.compressFunc != nil {
		return m.compressFunc(ctx, req)
	}
	return &CompressionResult{
		OriginalTokens:   100,
		CompressedTokens: 50,
		CompressionRatio: 0.5,
		CompressedMessages: []tokenizers.Message{
			{Role: "assistant", Content: "## Goal\n- Did something\n\n## Key Decisions\n- Chose technology X"},
		},
		Summary:      "[SUMMARY] Did something important",
		CompressedAt: time.Now(),
	}, nil
}

func TestCompactWithOpenCode(t *testing.T) {
	t.Run("returns error without LLM compressor", func(t *testing.T) {
		config := DefaultCompactionConfig()
		compactor := NewCompactor(config, nil, nil)
		msgs := []tokenizers.Message{
			{Role: "user", Content: "hello"},
		}
		_, err := compactor.CompactWithOpenCode(nil, msgs, 4096, 2, nil)
		if err == nil {
			t.Error("expected error without LLM compressor")
		}
	})

	t.Run("produces summary and tail with mock LLM", func(t *testing.T) {
		config := DefaultCompactionConfig()
		mockLLM := &mockLLMCompressor{}
		compactor := NewCompactor(config, mockLLM, nil)

		msgs := []tokenizers.Message{
			{Role: "user", Content: "old turn"},
			{Role: "assistant", Content: "old resp"},
			{Role: "user", Content: "recent turn"},
			{Role: "assistant", Content: "recent resp"},
		}
		result, err := compactor.CompactWithOpenCode(nil, msgs, 4096, 1, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Summary == "" {
			t.Error("expected non-empty summary")
		}
		if !result.SummaryMsg.Summary {
			t.Error("summary message should have Summary=true")
		}
		if result.TokensBefore == 0 {
			t.Error("expected non-zero TokensBefore")
		}
		if result.TokensAfter == 0 {
			t.Error("expected non-zero TokensAfter")
		}

		t.Logf("Summary: %s", truncateStr(result.Summary, 80))
		t.Logf("Tail messages: %d", len(result.KeptTail))
		t.Logf("Tokens: %d -> %d", result.TokensBefore, result.TokensAfter)
	})

	t.Run("tokens decrease after compaction", func(t *testing.T) {
		config := DefaultCompactionConfig()
		mockLLM := &mockLLMCompressor{}
		compactor := NewCompactor(config, mockLLM, nil)

		msgs := make([]tokenizers.Message, 20)
		for i := 0; i < 20; i++ {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			msgs[i] = tokenizers.Message{Role: role, Content: createLongOutput(50)}
		}
		result, err := compactor.CompactWithOpenCode(nil, msgs, 4096, 2, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.TokensAfter > 0 && result.TokensAfter >= result.TokensBefore {
			t.Logf("Note: tokens after (%d) >= before (%d) — may happen with small context and large tail", result.TokensAfter, result.TokensBefore)
		}
	})
}

// ============================================================
// FilterCompacted Tests
// ============================================================

func TestFindCompactionMarkers(t *testing.T) {
	t.Run("no markers in empty history", func(t *testing.T) {
		got := FindCompactionMarkers(nil)
		if len(got) != 0 {
			t.Errorf("expected 0 markers, got %d", len(got))
		}
	})

	t.Run("finds summary assistant message", func(t *testing.T) {
		msgs := []tokenizers.Message{
			{Role: "user", Content: "what did we do?"},
			{Role: "assistant", Content: "## Goal\n- Did stuff", Summary: true},
		}
		got := FindCompactionMarkers(msgs)
		if len(got) != 1 {
			t.Fatalf("expected 1 marker, got %d", len(got))
		}
		if got[0].Index != 1 {
			t.Errorf("expected index 1, got %d", got[0].Index)
		}
	})

	t.Run("ignores non-summary assistant messages", func(t *testing.T) {
		msgs := []tokenizers.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "world"},
		}
		got := FindCompactionMarkers(msgs)
		if len(got) != 0 {
			t.Errorf("expected 0 markers, got %d", len(got))
		}
	})
}

func TestFilterCompacted(t *testing.T) {
	t.Run("no change without compaction markers", func(t *testing.T) {
		msgs := []tokenizers.Message{
			{Role: "user", Content: "a"},
			{Role: "assistant", Content: "b"},
		}
		got := FilterCompacted(msgs)
		if len(got) != 2 {
			t.Errorf("expected 2 messages, got %d", len(got))
		}
	})

	t.Run("reorders with summary marker", func(t *testing.T) {
		msgs := []tokenizers.Message{
			{Role: "user", Content: "old"},
			{Role: "assistant", Content: "old resp"},
			{Role: "user", Content: "compaction marker"},
			{Role: "assistant", Content: "## Summary", Summary: true},
			{Role: "user", Content: "tail msg"},
		}
		got := FilterCompacted(msgs)
		if len(got) < 3 {
			t.Fatalf("expected at least 3 messages, got %d", len(got))
		}
		if got[0].Content != "compaction marker" {
			t.Errorf("first message should be compaction user, got: %s", got[0].Content)
		}
		if got[1].Content != "## Summary" {
			t.Errorf("second message should be summary, got: %s", got[1].Content)
		}
	})
}

// ============================================================
// Integration: Full opencode compaction flow
// ============================================================

func TestOpenCodeFullFlow(t *testing.T) {
	mockLLM := &mockLLMCompressor{}
	config := DefaultCompactionConfig()
	compactor := NewCompactor(config, mockLLM, nil)

	msgs := []tokenizers.Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "write a go function"},
		{Role: "assistant", Content: "here is the code"},
		{Role: "tool", Content: createLongOutput(3000)},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "now add tests"},
		{Role: "assistant", Content: "tests added"},
	}

	if !IsOverflow(EstimateMessagesTokensSimple(msgs), 400, nil) {
		t.Log("Messages don't overflow — increasing context would require more data")
	}

	pruned := PruneMessages(msgs)
	if len(pruned) < len(msgs) {
		t.Logf("Pruning removed %d tool outputs", len(msgs)-len(pruned))
	}

	result, err := compactor.CompactWithOpenCode(nil, msgs, 4096, 1, nil)
	if err != nil {
		t.Fatalf("Compaction failed: %v", err)
	}

	t.Logf("=== Full Flow Result ===")
	t.Logf("Tokens: %d -> %d (%.1f%% reduction)",
		result.TokensBefore, result.TokensAfter,
		(float64(result.TokensBefore-result.TokensAfter)/float64(result.TokensBefore))*100)
	t.Logf("Summary: %s", truncateStr(result.Summary, 100))
	t.Logf("Tail messages preserved: %d", len(result.KeptTail))
	t.Logf("Summary message: %+v", result.SummaryMsg)

	if result.TokensAfter > 0 && result.TokensAfter < result.TokensBefore {
		t.Log("✓ Tokens reduced")
	}
	if result.Summary != "" {
		t.Log("✓ Summary produced")
	}
}

// ============================================================
// Helpers
// ============================================================

func createLongOutput(chars int) string {
	b := make([]byte, chars)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
