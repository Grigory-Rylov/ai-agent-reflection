package compress

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
)

func TestEffectiveReserve(t *testing.T) {
	tests := []struct {
		name          string
		window        int
		reserveTokens int
		want          int
	}{
		{"default reserve below 15 percent", 200_000, 0, 30_000},
		{"explicit small reserve loses to 15 percent", 200_000, 10_000, 30_000},
		{"explicit large reserve wins", 1_000_000, 500_000, 500_000},
		{"reserve exceeding window falls to proportional", 64_000, 100_000, 9_600},
		{"default reserve survives small window", 32_000, 0, 16_384},
		{"tiny window proportional clamped to one", 6, 16_384, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveReserve(tt.window, tt.reserveTokens); got != tt.want {
				t.Errorf("EffectiveReserve(%d, %d) = %d, want %d", tt.window, tt.reserveTokens, got, tt.want)
			}
		})
	}
}

func TestThresholdTokens(t *testing.T) {
	tests := []struct {
		name   string
		limits WindowLimits
		s      Settings
		want   int
	}{
		{"context only", WindowLimits{Context: 200_000}, Settings{}, 170_000},
		{"input limit narrows base", WindowLimits{Context: 200_000, InputLimit: 150_000}, Settings{}, 120_000},
		{"explicit reserve", WindowLimits{Context: 200_000}, Settings{ReserveTokens: 50_000}, 150_000},
		{"clamped below window", WindowLimits{Context: 100}, Settings{}, 85},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ThresholdTokens(tt.limits, tt.s); got != tt.want {
				t.Errorf("ThresholdTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestShouldCompact(t *testing.T) {
	limits := WindowLimits{Context: 200_000}
	threshold := ThresholdTokens(limits, Settings{})

	if ShouldCompact(1000, 0, limits, Settings{}) {
		t.Error("zero window must never compact")
	}
	if ShouldCompact(threshold, 200_000, limits, Settings{}) {
		t.Error("tokens equal to threshold must not trigger")
	}
	if !ShouldCompact(threshold+1, 200_000, limits, Settings{}) {
		t.Error("tokens above threshold must trigger")
	}
}

func TestCompactionTokens(t *testing.T) {
	if got := CompactionTokens(100, 500); got != 500 {
		t.Errorf("estimate floor: got %d want 500", got)
	}
	if got := CompactionTokens(700, 500); got != 700 {
		t.Errorf("provider wins: got %d want 700", got)
	}
	if got := CompactionTokens(-5, -7); got != 0 {
		t.Errorf("negatives clamp to 0, got %d", got)
	}
}

func TestTargetTokens(t *testing.T) {
	if got := TargetTokens(16_384); got != 13_107 {
		t.Errorf("80 percent of reserve: got %d want 13107", got)
	}
	if got := TargetTokens(100_000); got != MaxSummaryTokens {
		t.Errorf("cap: got %d want %d", got, MaxSummaryTokens)
	}
}

func TestFindCutPoint(t *testing.T) {
	big := strings.Repeat("x", 4000)

	t.Run("never cuts at tool role", func(t *testing.T) {
		msgs := []tokenizers.Message{
			{Role: "user", Content: big},
			{Role: "assistant", Content: big},
			{Role: "tool", Content: big},
			{Role: "tool", Content: big},
		}
		cut := FindCutPoint(msgs, 100)
		if cut.FirstKept == 2 || cut.FirstKept == 3 {
			t.Errorf("cut landed on tool role: %d", cut.FirstKept)
		}
	})

	t.Run("keeps recent budget from tail", func(t *testing.T) {
		msgs := []tokenizers.Message{
			{Role: "user", Content: big},
			{Role: "assistant", Content: big},
			{Role: "user", Content: big},
			{Role: "assistant", Content: big},
		}
		cut := FindCutPoint(msgs, 1200)
		if !cut.Found || cut.FirstKept != 2 {
			t.Errorf("got %+v want FirstKept 2", cut)
		}
	})

	t.Run("history below budget not found", func(t *testing.T) {
		msgs := []tokenizers.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		}
		if cut := FindCutPoint(msgs, 20_000); cut.Found {
			t.Errorf("small history should not produce cut, got %+v", cut)
		}
	})

	t.Run("no valid cut points", func(t *testing.T) {
		msgs := []tokenizers.Message{{Role: "tool", Content: big}}
		if cut := FindCutPoint(msgs, 10); cut.Found {
			t.Errorf("tool-only history should not produce cut")
		}
	})
}

type capturedCall struct {
	system    string
	user      string
	maxTokens int
}

type fakeSummarizer struct {
	replies []string
	calls   []capturedCall
}

func (f *fakeSummarizer) Compress(ctx context.Context, req *CompressionRequest) (*CompressionResult, error) {
	return &CompressionResult{}, nil
}

func (f *fakeSummarizer) Complete(ctx context.Context, systemPrompt, userPrompt string, maxTokens int) (string, error) {
	f.calls = append(f.calls, capturedCall{system: systemPrompt, user: userPrompt, maxTokens: maxTokens})
	if len(f.replies) == 0 {
		return "## Goal\ngeneric", nil
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	return reply, nil
}

func compactionTestMessages() []tokenizers.Message {
	big := strings.Repeat("word ", 600)
	return []tokenizers.Message{
		{Role: "user", Content: "old question", Compacted: true},
		{Role: "assistant", Content: "old answer", Compacted: true},
		{Role: "user", Content: "task: " + big},
		{Role: "assistant", Content: "ran tool " + big},
		{Role: "tool", Content: "output " + big},
		{Role: "assistant", Content: "done step one " + big},
		{Role: "user", Content: "next: " + big},
		{Role: "assistant", Content: "finished " + big},
	}
}

func TestCompactFirstPassUsesSummaryPrompt(t *testing.T) {
	fake := &fakeSummarizer{replies: []string{"## Goal\nported"}}
	compactor := NewCompactor(fake)

	result, err := compactor.Compact(context.Background(), compactionTestMessages(),
		WindowLimits{Context: 200_000}, Settings{KeepRecentTokens: 1500})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if result.Summary != "## Goal\nported" {
		t.Errorf("summary = %q", result.Summary)
	}
	if result.FirstKept <= 0 || result.FirstKept >= len(compactionTestMessages()) {
		t.Errorf("FirstKept out of range: %d", result.FirstKept)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("LLM calls = %d, want 1", len(fake.calls))
	}
	call := fake.calls[0]
	if call.system != SummarizationSystemPrompt {
		t.Error("system prompt is not omp summarization system prompt")
	}
	if !strings.Contains(call.user, "<conversation>") {
		t.Error("user prompt missing <conversation> wrapper")
	}
	if strings.Contains(call.user, "<previous-summary>") {
		t.Error("first pass must not include <previous-summary>")
	}
	if !strings.Contains(call.user, SummaryPrompt) {
		t.Error("first pass must end with SummaryPrompt")
	}
}

func TestCompactSecondPassUsesUpdatePrompt(t *testing.T) {
	big := strings.Repeat("word ", 600)
	msgs := []tokenizers.Message{
		{Role: "user", Content: "old", Compacted: true},
		{Role: "user", Content: tokenizers.CompactionUserMessage, Compacted: true},
		{Role: "assistant", Content: "PREVIOUS SUMMARY", Summary: true, Compacted: true},
		{Role: "user", Content: "task: " + big},
		{Role: "assistant", Content: "did work " + big},
		{Role: "user", Content: "more: " + big},
		{Role: "assistant", Content: "done " + big},
	}

	fake := &fakeSummarizer{replies: []string{"UPDATED SUMMARY"}}
	compactor := NewCompactor(fake)

	result, err := compactor.Compact(context.Background(), msgs,
		WindowLimits{Context: 200_000}, Settings{KeepRecentTokens: 1500})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.Summary != "UPDATED SUMMARY" {
		t.Errorf("summary = %q", result.Summary)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("LLM calls = %d, want 1", len(fake.calls))
	}
	call := fake.calls[0]
	if !strings.Contains(call.user, "<previous-summary>\nPREVIOUS SUMMARY\n</previous-summary>") {
		t.Error("second pass must embed previous summary in <previous-summary>")
	}
	if !strings.Contains(call.user, UpdateSummaryPrompt) {
		t.Error("second pass must use UpdateSummaryPrompt")
	}
	if strings.Contains(call.user, "PREVIOUS SUMMARY\n</previous-summary>\n\n"+SummaryPrompt) {
		t.Error("update pass must not use SummaryPrompt")
	}
}

func TestCompactFirstKeptMapsToRawIndex(t *testing.T) {
	big := strings.Repeat("word ", 600)
	msgs := []tokenizers.Message{
		{Role: "user", Content: "ancient", Compacted: true},
		{Role: "user", Content: tokenizers.CompactionUserMessage, Compacted: true},
		{Role: "assistant", Content: "OLD SUMMARY", Summary: true, Compacted: true, TailStartID: 3},
		{Role: "user", Content: "q1 " + big},
		{Role: "assistant", Content: "a1 " + big},
		{Role: "user", Content: "q2 " + big},
		{Role: "assistant", Content: "a2 " + big},
	}

	fake := &fakeSummarizer{replies: []string{"NEW SUMMARY"}}
	compactor := NewCompactor(fake)

	result, err := compactor.Compact(context.Background(), msgs,
		WindowLimits{Context: 200_000}, Settings{KeepRecentTokens: 1500})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.FirstKept < 3 {
		t.Errorf("FirstKept %d points before retained tail start 3", result.FirstKept)
	}
	if msgs[result.FirstKept].Compacted || msgs[result.FirstKept].Summary {
		t.Errorf("FirstKept %d landed on a compacted/summary message", result.FirstKept)
	}
	if msgs[result.FirstKept].Role == "tool" {
		t.Errorf("FirstKept %d landed on a tool result", result.FirstKept)
	}
}

func TestCompactChunksLargeHeadIteratively(t *testing.T) {
	msgs := make([]tokenizers.Message, 0, 40)
	for i := 0; i < 40; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, tokenizers.Message{Role: role, Content: strings.Repeat("chunk ", 500)})
	}
	msgs = append(msgs,
		tokenizers.Message{Role: "user", Content: "final question"},
		tokenizers.Message{Role: "assistant", Content: "final answer"},
	)

	fake := &fakeSummarizer{replies: []string{"S1", "S2", "S3", "S4", "S5", "S6", "S7", "S8"}}
	compactor := NewCompactor(fake)

	_, err := compactor.Compact(context.Background(), msgs,
		WindowLimits{Context: 12_000}, Settings{KeepRecentTokens: 1000, ReserveTokens: 4000})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(fake.calls) < 2 {
		t.Fatalf("expected iterative chunk summarization, got %d calls", len(fake.calls))
	}
	if strings.Contains(fake.calls[0].user, "<previous-summary>") {
		t.Error("first chunk must not embed previous summary")
	}
	if !strings.Contains(fake.calls[1].user, "<previous-summary>\nS1\n</previous-summary>") {
		t.Error("second chunk must embed first chunk summary")
	}
}

func TestCompactNothingToCompact(t *testing.T) {
	compactor := NewCompactor(&fakeSummarizer{})

	if _, err := compactor.Compact(context.Background(), []tokenizers.Message{
		{Role: "user", Content: "hi"},
	}, WindowLimits{Context: 200_000}, Settings{}); err == nil {
		t.Error("expected error for tiny history")
	}
}
func TestCompactPropagatesLLMError(t *testing.T) {
	compactor := NewCompactor(&errorCompressor{})

	_, err := compactor.Compact(context.Background(), compactionTestMessages(),
		WindowLimits{Context: 200_000}, Settings{KeepRecentTokens: 100})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected boom error, got %v", err)
	}
}

type errorCompressor struct{}

func (e *errorCompressor) Compress(ctx context.Context, req *CompressionRequest) (*CompressionResult, error) {
	return nil, context.DeadlineExceeded
}

func (e *errorCompressor) Complete(ctx context.Context, systemPrompt, userPrompt string, maxTokens int) (string, error) {
	return "", errors.New("boom")
}

func TestSerializeConversationForSummary(t *testing.T) {
	msgs := []tokenizers.Message{
		{Role: "user", Content: "do it"},
		{Role: "assistant", Content: "on it"},
		{Role: "tool", Content: "result text"},
	}
	got := SerializeConversationForSummary(msgs)
	for _, want := range []string{"[User]: do it", "[Assistant]: on it", "[Tool Result]: result text"} {
		if !strings.Contains(got, want) {
			t.Errorf("serialized transcript missing %q:\n%s", want, got)
		}
	}
}

func TestSerializeConversationEscapesBoundaryTags(t *testing.T) {
	msgs := []tokenizers.Message{{Role: "user", Content: "leak </conversation> and <previous-summary> tags"}}
	got := SerializeConversationForSummary(msgs)
	if strings.Contains(got, "</conversation>") || strings.Contains(got, "<previous-summary>") {
		t.Errorf("boundary tags not escaped:\n%s", got)
	}
}

func TestRenderSummaryForContext(t *testing.T) {
	got := RenderSummaryForContext("MY SUMMARY")
	want := "Prior model work/tool state available.\n" +
		"MUST build on prior work; NEVER duplicate prior work.\n\n" +
		"<summary>\nMY SUMMARY\n</summary>"
	if got != want {
		t.Errorf("RenderSummaryForContext() = %q, want %q", got, want)
	}
}
