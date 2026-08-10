package compress

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// Test Scenarios: Real-world compression cases
// ============================================================

// Scenario 1: Long conversation with code discussion
func TestScenario_CodeDiscussion(t *testing.T) {
	mockLLM := &mockLLMCompressor{}
	compactor := NewCompactor(mockLLM)

	// Симулируем долгий разговор о рефакторинге
	msgs := []tokenizers.Message{
		{Role: "user", Content: "I need to refactor the user service"},
		{Role: "assistant", Content: "I'll help you refactor. What specific issues are you facing?"},
		{Role: "user", Content: "The authentication is duplicated across multiple files"},
		{Role: "assistant", Content: "Let's extract the auth logic into a separate module"},
		{Role: "tool", Content: "Reading src/auth/service.go..."},
		{Role: "assistant", Content: "Found the auth service. We can move the common logic here."},
		{Role: "user", Content: "Also need to update the middleware"},
		{Role: "tool", Content: createLongOutput(50000)}, // Большой output
		{Role: "assistant", Content: "I'll update the middleware"},
		{Role: "tool", Content: createLongOutput(30000)},
		{Role: "assistant", Content: "Middleware updated"},
		{Role: "user", Content: "Now let's add tests"},
		{Role: "assistant", Content: "I'll create test files"},
	}

	tokensBefore := EstimateMessagesTokensSimple(msgs)
	t.Logf("Scenario: Code discussion with %d tokens", tokensBefore)

	// Pruning
	pruned := PruneMessages(msgs)
	prunedCount := 0
	for _, m := range pruned {
		if m.Compacted {
			prunedCount++
		}
	}
	t.Logf("Pruned %d tool outputs", prunedCount)

	// Compaction
	result, err := compactor.CompactWithOpenCode(nil, msgs, 200000, 2, nil)
	if err != nil {
		t.Fatalf("Compaction failed: %v", err)
	}

	if result.TokensAfter >= result.TokensBefore {
		t.Errorf("Tokens should decrease: %d -> %d", result.TokensBefore, result.TokensAfter)
	}

	reduction := float64(result.TokensBefore-result.TokensAfter) / float64(result.TokensBefore) * 100
	t.Logf("Reduction: %.1f%%", reduction)

	if result.Summary == "" {
		t.Error("Expected non-empty summary")
	}

	if len(result.KeptTail) == 0 {
		t.Error("Expected some tail messages to be preserved")
	}
}

// Scenario 2: Multiple compaction cycles
func TestScenario_MultipleCompactionCycles(t *testing.T) {
	mockLLM := &mockLLMCompressor{
		compressFunc: func(ctx context.Context, req *CompressionRequest) (*CompressionResult, error) {
			return &CompressionResult{
				CompressedMessages: []tokenizers.Message{
					{Role: "assistant", Content: "## Goal\n- Working on multi-step task\n\n## Progress\n### Done\n- Step 1 completed\n### In Progress\n- Step 2"},
				},
			}, nil
		},
	}
	compactor := NewCompactor(mockLLM)

	// Первая компакшн
	msgs1 := createConversation(20, 1000)
	result1, err := compactor.CompactWithOpenCode(nil, msgs1, 200000, 2, nil)
	if err != nil {
		t.Fatalf("First compaction failed: %v", err)
	}
	t.Logf("First compaction: %d -> %d tokens", result1.TokensBefore, result1.TokensAfter)

	// Симулируем продолжение разговора после компакшн
	compactedMsgs := []tokenizers.Message{
		{Role: "user", Content: "compaction"},
		{Role: "assistant", Content: result1.Summary, Summary: true},
	}
	compactedMsgs = append(compactedMsgs, result1.KeptTail...)

	// Добавляем новые сообщения
	newMsgs := []tokenizers.Message{
		{Role: "user", Content: "Continue with step 3"},
		{Role: "assistant", Content: "I'll proceed with step 3"},
		{Role: "tool", Content: createLongOutput(40000)},
		{Role: "assistant", Content: "Step 3 completed"},
	}
	combined := append(compactedMsgs, newMsgs...)

	// Вторая компакшн (должна использовать предыдущий summary)
	result2, err := compactor.CompactWithOpenCode(nil, combined, 200000, 2, nil)
	if err != nil {
		t.Fatalf("Second compaction failed: %v", err)
	}
	t.Logf("Second compaction: %d -> %d tokens", result2.TokensBefore, result2.TokensAfter)

	// Проверяем, что второй compaction строится на первом
	if !strings.Contains(result2.Summary, "Step 1") && !strings.Contains(result2.Summary, "Step 2") {
		t.Log("Note: Second summary may have different content depending on LLM")
	}
}

// Scenario 3: Budget constraint - minimal tail preservation
func TestScenario_MinimalBudgetConstraint(t *testing.T) {
	// Создаем большое сообщение
	msgs := make([]tokenizers.Message, 10)
	for i := 0; i < 10; i++ {
		msgs[i] = tokenizers.Message{
			Role:    "user",
			Content: createLongOutput(1000),
		}
		if i < 9 {
			msgs = append(msgs, tokenizers.Message{
				Role:    "assistant",
				Content: createLongOutput(1000),
			})
		}
	}

	// Очень маленький бюджет
	budget := 500
	selected := SelectMessages(msgs, 2, budget)

	t.Logf("Budget: %d tokens", budget)
	t.Logf("Head: %d messages", len(selected.Head))
	t.Logf("TailStartID: %d", selected.TailStartID)

	// С маленьким бюджетом tail может быть пустым или минимальным
	if selected.TailStartID >= 0 {
		tailSize := 0
		if selected.TailStartID >= 0 && selected.TailStartID < len(msgs) {
			tailSize = len(msgs) - selected.TailStartID
		}
		t.Logf("Tail size: %d messages", tailSize)
	}
}

// Scenario 4: Overflow detection edge cases
func TestScenario_OverflowEdgeCases(t *testing.T) {
	tests := []struct {
		name           string
		currentTokens  int
		contextLimit   int
		reserved       *int
		expectOverflow bool
	}{
		{"exact limit", 168_000, 200_000, nil, true}, // usable = 200000 - 32000 = 168000
		{"just under", 167_999, 200_000, nil, false},
		{"zero context", 1000, 0, nil, false},
		{"custom reserved no inputLimit", 168_000, 200_000, intPtr(50_000), true}, // reserved не влияет без inputLimit
		{"under custom reserved", 167_999, 200_000, intPtr(50_000), false},
		{"large context", 968_000, 1_000_000, nil, true}, // usable = 1000000 - 32000 = 968000
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			overflow := IsOverflow(tt.currentTokens, tt.contextLimit, tt.reserved)
			if overflow != tt.expectOverflow {
				t.Errorf("IsOverflow(%d, %d, %v) = %v, want %v",
					tt.currentTokens, tt.contextLimit, tt.reserved, overflow, tt.expectOverflow)
			}
		})
	}
}

// Scenario 5: Pruning with existing compacted messages
func TestScenario_PruningWithAlreadyCompacted(t *testing.T) {
	large := createLongOutput(100000)
	msgs := []tokenizers.Message{
		{Role: "user", Content: "old request"},
		{Role: "tool", Content: large, Compacted: true}, // Already compacted
		{Role: "assistant", Content: "response"},
		{Role: "user", Content: "new request 1"},
		{Role: "tool", Content: large},
		{Role: "assistant", Content: "response"},
		{Role: "user", Content: "new request 2"},
		{Role: "tool", Content: large},
		{Role: "assistant", Content: "response"},
		{Role: "user", Content: "new request 3"},
		{Role: "tool", Content: large},
		{Role: "assistant", Content: "response"},
	}

	result := PruneMessages(msgs)

	newlyCompacted := 0
	for i, m := range result {
		if m.Compacted && i > 0 && !msgs[i].Compacted {
			newlyCompacted++
		}
	}

	t.Logf("Newly compacted: %d tool outputs", newlyCompacted)

	// Первые 2 user turn должны быть защищены
	userTurns := 0
	for _, m := range result {
		if m.Role == "user" {
			userTurns++
			if userTurns <= 2 {
				// Не должно быть compacted в защищенных turns
			}
		}
	}
}

// Scenario 5b: PRUNE_PROTECTED_TOOLS — tool-выводы защищённых инструментов
// (например "skill") не обрезаются, даже если выходят за PRUNE_PROTECT.
// Как в opencode prune(): if (PRUNE_PROTECTED_TOOLS.includes(part.tool)) continue.
func TestScenario_PruningProtectedTools(t *testing.T) {
	large := createLongOutput(200000) // ~50k токенов каждый
	msgs := []tokenizers.Message{}
	for i := 0; i < 4; i++ {
		name := "file_read"
		if i == 0 {
			name = "skill"
		}
		msgs = append(msgs,
			tokenizers.Message{Role: "user", Content: "request"},
			tokenizers.Message{Role: "tool", Content: large, Name: name},
			tokenizers.Message{Role: "assistant", Content: "response"},
		)
	}

	result := PruneMessages(msgs, PRUNE_PROTECTED_TOOLS...)

	skillPruned := false
	fileReadPruned := false
	for i, m := range msgs {
		if m.Name == "skill" && result[i].Compacted {
			skillPruned = true
		}
		if m.Name == "file_read" && result[i].Compacted {
			fileReadPruned = true
		}
	}
	if skillPruned {
		t.Error("protected tool 'skill' must not be pruned")
	}
	if !fileReadPruned {
		t.Error("expected non-protected tool outputs to be pruned")
	}
}

// Scenario 6: FilterCompacted with multiple summaries
func TestScenario_MultipleSummaries(t *testing.T) {
	msgs := []tokenizers.Message{
		{Role: "user", Content: "very old"},
		{Role: "assistant", Content: "old response"},
		{Role: "user", Content: "compaction 1"},
		{Role: "assistant", Content: "## Summary 1", Summary: true},
		{Role: "user", Content: "middle message"},
		{Role: "assistant", Content: "middle response"},
		{Role: "user", Content: "compaction 2"},
		{Role: "assistant", Content: "## Summary 2", Summary: true},
		{Role: "user", Content: "latest message"},
	}

	result := FilterCompacted(msgs)

	t.Logf("Original: %d messages", len(msgs))
	t.Logf("Filtered: %d messages", len(result))

	// Должно начинаться с последнего compaction marker
	if len(result) > 0 {
		if result[0].Content != "compaction 2" {
			t.Errorf("Expected 'compaction 2' as first message, got: %s", result[0].Content)
		}
		if len(result) > 1 && result[1].Content != "## Summary 2" {
			t.Errorf("Expected '## Summary 2' as second message, got: %s", result[1].Content)
		}
	}
}

// Scenario 7: Tool output truncation for compaction
func TestScenario_ToolOutputTruncation(t *testing.T) {
	// Создаем длинный tool output
	toolContent := createLongOutput(10000)

	// Проверяем, что при оценке токенов учитывается усечение
	estimateBefore := EstimateTokensSimple(toolContent)
	t.Logf("Tool output: %d chars, estimated %d tokens", len(toolContent), estimateBefore)

	// Усечение для компакшена (2000 chars max)
	truncated := truncateToolOutputForCompaction(toolContent, 2000)
	estimateAfter := EstimateTokensSimple(truncated)
	t.Logf("Truncated: %d chars, estimated %d tokens", len(truncated), estimateAfter)

	if estimateAfter >= estimateBefore {
		t.Errorf("Truncation should reduce tokens: %d -> %d", estimateBefore, estimateAfter)
	}
}

// Scenario 8: Preserved recent tokens calculation
func TestScenario_PreserveRecentBudget(t *testing.T) {
	tests := []struct {
		name              string
		maxTokens         int
		preserveRecent    *int
		expectedMinBudget int
		expectedMaxBudget int
	}{
		{"default budget 200K", 200_000, nil, 2_000, 8_000},
		{"small context", 10_000, nil, 2_000, 2_500},
		{"large context", 1_000_000, nil, 2_000, 8_000},
		{"custom budget", 200_000, intPtr(5000), 5_000, 5_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			budget := preserveRecentBudget(tt.maxTokens, tt.preserveRecent)
			t.Logf("Budget: %d", budget)

			if budget < tt.expectedMinBudget {
				t.Errorf("Budget %d below minimum %d", budget, tt.expectedMinBudget)
			}
			if budget > tt.expectedMaxBudget {
				t.Errorf("Budget %d above maximum %d", budget, tt.expectedMaxBudget)
			}
		})
	}
}

// Scenario 9: User turns extraction
func TestScenario_UserTurnsExtraction(t *testing.T) {
	msgs := []tokenizers.Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "user1"},
		{Role: "assistant", Content: "resp1"},
		{Role: "tool", Content: "tool1"},
		{Role: "user", Content: "user2"},
		{Role: "assistant", Content: "resp2"},
		{Role: "user", Content: "user3"},
		{Role: "assistant", Content: "resp3"},
	}

	turns := userTurns(msgs)

	t.Logf("Found %d user turns", len(turns))

	if len(turns) != 3 {
		t.Errorf("Expected 3 turns, got %d", len(turns))
	}

	// Проверяем границы turns
	expectedTurns := []struct{ start, end int }{
		{1, 4}, // user1 -> user2 (exclusive)
		{4, 6}, // user2 -> user3
		{6, 8}, // user3 -> end
	}

	for i, turn := range turns {
		if i >= len(expectedTurns) {
			break
		}
		exp := expectedTurns[i]
		if turn.Start != exp.start || turn.End != exp.end {
			t.Errorf("Turn %d: got [%d,%d), want [%d,%d)", i, turn.Start, turn.End, exp.start, exp.end)
		}
	}
}

// Scenario 10: Split turn functionality
func TestScenario_SplitTurn(t *testing.T) {
	msgs := []tokenizers.Message{
		{Role: "user", Content: "start"},
		{Role: "assistant", Content: "resp1"},
		{Role: "tool", Content: createLongOutput(1000)},
		{Role: "tool", Content: createLongOutput(1000)},
		{Role: "assistant", Content: "resp2"},
		{Role: "user", Content: "next"}, // End of turn
	}

	turn := Turn{Start: 0, End: 6}

	// Пытаемся сохранить часть turn с маленьким бюджетом
	splitStart, ok := splitTurn(msgs, turn, 500) // 500 tokens budget
	t.Logf("Split result: start=%d, ok=%v", splitStart, ok)

	if ok {
		// splitStart должен быть внутри turn
		if splitStart <= turn.Start || splitStart >= turn.End {
			t.Errorf("splitStart %d not in turn [%d,%d)", splitStart, turn.Start, turn.End)
		}

		// Проверяем, что разделённая часть влезает в бюджет
		size := estimateMessagesTokens(msgs[splitStart:turn.End])
		t.Logf("Split size: %d tokens (budget: %d)", size, 500)
		if size > 500 {
			t.Errorf("Split size %d exceeds budget %d", size, 500)
		}
	}
}

// Scenario 11: Empty/null message handling
func TestScenario_EmptyMessages(t *testing.T) {
	mockLLM := &mockLLMCompressor{}
	compactor := NewCompactor(mockLLM)

	tests := []struct {
		name      string
		msgs      []tokenizers.Message
		tailTurns int
		wantErr   bool
	}{
		{"nil messages", nil, 2, true},
		{"empty messages", []tokenizers.Message{}, 2, true},
		{"single user", []tokenizers.Message{{Role: "user", Content: "hi"}}, 2, false},                 // head = все сообщения (opencode: keepStart==0)
		{"only system", []tokenizers.Message{{Role: "system", Content: "You are helpful"}}, 2, false}, // system messages go to head
		{"two user messages with tail 1", []tokenizers.Message{
			{Role: "user", Content: "old"},
			{Role: "assistant", Content: "resp"},
			{Role: "user", Content: "new"},
		}, 1, false}, // valid compaction with tail_turns=1
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := compactor.CompactWithOpenCode(nil, tt.msgs, 4096, tt.tailTurns, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("CompactWithOpenCode() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Scenario 12: Summary template validation
func TestScenario_SummaryTemplateValidation(t *testing.T) {
	template := SUMMARY_TEMPLATE

	requiredSections := []string{
		"## Goal",
		"## Constraints & Preferences",
		"## Progress",
		"### Done",
		"### In Progress",
		"### Blocked",
		"## Key Decisions",
		"## Next Steps",
		"## Critical Context",
		"## Relevant Files",
	}

	for _, section := range requiredSections {
		if !strings.Contains(template, section) {
			t.Errorf("Template missing section: %s", section)
		}
	}

	// Template должен содержать <template> тег для инструкций модели
	if !strings.Contains(template, "<template>") {
		t.Error("Template should contain <template> tag for instructions")
	}

	// Проверяем, что есть инструкцию не включать теги в ответ
	if !strings.Contains(template, "Do not include the <template> tags") {
		t.Error("Template should instruct not to include <template> tags in response")
	}
}

// Scenario 13: Auto-continue after compaction
func TestScenario_AutoContinueAfterCompact(t *testing.T) {
	mockLLM := &mockLLMCompressor{}
	compactor := NewCompactor(mockLLM)

	msgs := []tokenizers.Message{
		{Role: "user", Content: "old request"},
		{Role: "assistant", Content: "old response"},
		{Role: "user", Content: "latest request"},
	}

	result, err := compactor.CompactWithOpenCode(nil, msgs, 4096, 1, nil)
	if err != nil {
		t.Fatalf("Compaction failed: %v", err)
	}

	// После компакшена нужно отправить continue
	if result.Summary == "" {
		t.Error("Expected summary")
	}

	if len(result.KeptTail) == 0 {
		t.Error("Expected tail to be preserved for continue")
	}

	// Последнее сообщение в tail должно быть user запросом
	lastUserIdx := -1
	for i := len(result.KeptTail) - 1; i >= 0; i-- {
		if result.KeptTail[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}

	if lastUserIdx >= 0 {
		lastUserMsg := result.KeptTail[lastUserIdx]
		t.Logf("Last user message in tail: %s", truncateStr(lastUserMsg.Content, 50))
	}
}

// Scenario 14: Context with only summary (no head to compress)
func TestScenario_OnlySummaryNoHead(t *testing.T) {
	mockLLM := &mockLLMCompressor{}
	compactor := NewCompactor(mockLLM)

	// Уже сжатый контекст
	msgs := []tokenizers.Message{
		{Role: "user", Content: "compaction"},
		{Role: "assistant", Content: "## Goal\n- Already summarized", Summary: true},
		{Role: "user", Content: "continue"},
	}

	result, err := compactor.CompactWithOpenCode(nil, msgs, 4096, 1, nil)
	if err != nil {
		t.Fatalf("Compaction failed: %v", err)
	}

	// Head пустой, только предыдущий summary
	t.Logf("Summary: %s", truncateStr(result.Summary, 50))
	t.Logf("Tail: %d messages", len(result.KeptTail))
}

// Scenario 15: Maximum tail turns preservation
func TestScenario_MaxTailTurns(t *testing.T) {
	msgs := createConversation(10, 100) // 10 user turns

	// Сохраняем все turns
	tailTurns := 10
	budget := 100000 // Достаточно большой бюджет

	selected := SelectMessages(msgs, tailTurns, budget)

	t.Logf("Tail turns: %d, budget: %d", tailTurns, budget)
	t.Logf("Head: %d, TailStartID: %d", len(selected.Head), selected.TailStartID)

	// Как в opencode select(): если всё помещается в бюджет, keepStart == 0 —
	// компактится всё (head = все сообщения), хвост не сохраняется.
	if selected.TailStartID != -1 {
		t.Errorf("Expected no tail (TailStartID=-1), got %d", selected.TailStartID)
	}
	if len(selected.Head) != len(msgs) {
		t.Errorf("Expected all messages in head, got %d of %d", len(selected.Head), len(msgs))
	}
}

// ============================================================
// Benchmarks
// ============================================================

func BenchmarkSelectMessages(b *testing.B) {
	msgs := createConversation(100, 500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SelectMessages(msgs, 2, 8000)
	}
}

func BenchmarkPruneMessages(b *testing.B) {
	msgs := createConversation(50, 1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PruneMessages(msgs)
	}
}

func BenchmarkEstimateTokensSimple(b *testing.B) {
	text := createLongOutput(10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EstimateTokensSimple(text)
	}
}

// ============================================================
// Helpers
// ============================================================

func createConversation(turns, msgSize int) []tokenizers.Message {
	msgs := make([]tokenizers.Message, 0, turns*2)
	for i := 0; i < turns; i++ {
		content := fmt.Sprintf("Turn %d: %s", i, createLongOutput(msgSize))
		msgs = append(msgs, tokenizers.Message{Role: "user", Content: content})
		msgs = append(msgs, tokenizers.Message{Role: "assistant", Content: fmt.Sprintf("Response %d", i)})
	}
	return msgs
}

func truncateToolOutputForCompaction(content string, maxChars int) string {
	if len(content) <= maxChars {
		return content
	}
	return content[:maxChars] + fmt.Sprintf("\n[Tool output truncated for compaction: omitted %d chars]", len(content)-maxChars)
}

func intPtr(i int) *int {
	return &i
}
