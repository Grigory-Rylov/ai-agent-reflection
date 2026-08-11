package compress

import (
	"context"
	"fmt"
	"strings"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
)

// ============================================================
// Opencode-style Compaction — select(), Summary, LLM-based
// ============================================================

const (
	DEFAULT_TAIL_TURNS         = 2
	MIN_PRESERVE_RECENT_TOKENS = 2_000
	MAX_PRESERVE_RECENT_TOKENS = 8_000

	TOOL_OUTPUT_MAX_CHARS = 2000
)

// TruncateToolOutput truncates tool output to TOOL_OUTPUT_MAX_CHARS (like opencode)
func TruncateToolOutput(content string) string {
	if len(content) <= TOOL_OUTPUT_MAX_CHARS {
		return content
	}
	head := content[:TOOL_OUTPUT_MAX_CHARS] + "\n[truncated]"
	// Сохраняем хвостовую подсказку с путём к полному выводу, чтобы LLM
	// мог перечитать файл порциями после компакции.
	if idx := strings.LastIndex(content, "Full output saved to:"); idx >= 0 {
		return head + "\n" + content[idx:]
	}
	return head
}

// SUMMARY_TEMPLATE — шаблон для суммаризации контекста в формате opencode.
const SUMMARY_TEMPLATE = `Output exactly the Markdown structure shown inside <template> and keep the section order unchanged. Do not include the <template> tags in your response.
<template>
## Goal
- [single-sentence task summary]

## Constraints & Preferences
- [user constraints, preferences, specs, or "(none)"]

## Progress
### Done
- [completed work or "(none)"]

### In Progress
- [current work or "(none)"]

### Blocked
- [blockers or "(none)"]

## Key Decisions
- [decision and why, or "(none)"]

## Next Steps
- [ordered next actions or "(none)"]

## Critical Context
- [important technical facts, errors, open questions, or "(none)"]

## Relevant Files
- [file or directory path: why it matters, or "(none)"]
</template>

Rules:
- Keep every section, even when empty.
- Use terse bullets, not prose paragraphs.
- Preserve exact file paths, commands, error strings, and identifiers when known.
- Do not mention the summary process or that context was compacted.`

// Turn представляет user-оборот (user message + все последующие assistant/tool сообщениях до следующего user).
type Turn struct {
	Start int // индекс первого сообщения оборота
	End   int // индекс после последнего сообщения оборота
}

// SelectResult — результат выбора сообщений для компакшена.
// TailStartID — индекс (стабильный ID) первого сообщения хвоста в исходном
// slice сообщений; -1 означает «хвост не сохраняется» (head = все сообщения).
type SelectResult struct {
	Head        []tokenizers.Message // сообщения для сжатия
	TailStartID int                  // индекс первого сообщения хвоста
}

// PreserveRecentBudget вычисляет бюджет для сохранения последних сообщений.
func PreserveRecentBudget(maxTokens int, preserveRecent *int) int {
	return preserveRecentBudget(maxTokens, preserveRecent)
}

// preserveRecentBudget вычисляет бюджет для сохранения последних сообщений.
func preserveRecentBudget(maxTokens int, preserveRecent *int) int {
	if preserveRecent != nil && *preserveRecent > 0 {
		return *preserveRecent
	}
	usable := Usable(maxTokens, nil)
	budget := int(float64(usable) * 0.25)
	if budget < MIN_PRESERVE_RECENT_TOKENS {
		budget = MIN_PRESERVE_RECENT_TOKENS
	}
	if budget > MAX_PRESERVE_RECENT_TOKENS {
		budget = MAX_PRESERVE_RECENT_TOKENS
	}
	return budget
}

// userTurns разбивает сообщения на user-обороты.
// Как в opencode compaction.ts `turns()`: compaction user-сообщения (маркеры
// компактизации) НЕ считаются границей оборота.
func userTurns(messages []tokenizers.Message) []Turn {
	var result []Turn
	for i, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		if msg.Content == tokenizers.CompactionUserMessage {
			continue
		}
		result = append(result, Turn{
			Start: i,
			End:   len(messages),
		})
	}
	for i := 0; i < len(result)-1; i++ {
		result[i].End = result[i+1].Start
	}
	return result
}

// estimateMessagesTokens оценивает токены в сообщениях.
func estimateMessagesTokens(messages []tokenizers.Message) int {
	return EstimateMessagesTokensSimple(messages)
}

// splitTurn пытается разделить оборот, сохраняя только часть сообщений.
func splitTurn(messages []tokenizers.Message, turn Turn, budget int) (int, bool) {
	if budget <= 0 {
		return 0, false
	}
	if turn.End-turn.Start <= 1 {
		return 0, false
	}
	for start := turn.Start + 1; start < turn.End; start++ {
		size := estimateMessagesTokens(messages[start:turn.End])
		if size <= budget {
			return start, true
		}
	}
	return 0, false
}

// SelectMessages разделяет сообщения на head (для сжатия) и tail (сохранить).
// Сохраняет последние tailTurns user-оборотов, вписываясь в budget.
// Повторяет семантику opencode compaction.ts `select()`:
//   - keep.start === 0 (всё помещается в бюджет) → head = все сообщения,
//     tail_start_id = undefined (ничего не сохраняется, компактится всё);
//   - newest оборот не влезает и не делится → head = все сообщения.
func SelectMessages(messages []tokenizers.Message, tailTurns int, budget int) SelectResult {
	if tailTurns <= 0 {
		return SelectResult{Head: messages, TailStartID: -1}
	}
	all := userTurns(messages)
	if len(all) == 0 {
		return SelectResult{Head: messages, TailStartID: -1}
	}
	if tailTurns > len(all) {
		tailTurns = len(all)
	}
	recent := all[len(all)-tailTurns:]

	var total int
	var keepStart int = -1

	// Идём от newest к oldest, пытаемся вписаться в budget
	for i := len(recent) - 1; i >= 0; i-- {
		turn := recent[i]
		size := estimateMessagesTokens(messages[turn.Start:turn.End])
		if total+size <= budget {
			total += size
			keepStart = turn.Start
			continue
		}
		remaining := budget - total
		if remaining > 0 {
			if splitStart, ok := splitTurn(messages, turn, remaining); ok {
				keepStart = splitStart
			}
		}
		break
	}

	// Как в opencode: если хвост не выбран или начинается с индекса 0 —
	// head = все сообщения, tail_start_id = undefined.
	if keepStart <= 0 {
		return SelectResult{Head: messages, TailStartID: -1}
	}

	// Находим ID первого user сообщения в tail
	tailStartID := -1
	for i := keepStart; i < len(messages); i++ {
		if messages[i].Role == "user" {
			tailStartID = i
			break
		}
	}
	if tailStartID == -1 {
		tailStartID = keepStart
	}

	// Head доходит до границы хвоста, а не до split-точки: при splitTurn
	// (keepStart на не-user сообщении) остаток оборота messages[keepStart:tailStartID]
	// обязан попасть в head, иначе он выпадет и из head, и из tail.
	return SelectResult{
		Head:        messages[:tailStartID],
		TailStartID: tailStartID,
	}
}

// BuildSummaryPrompt строит промпт для суммаризации как opencode buildPrompt():
// [инструкция, SUMMARY_TEMPLATE, ...context]. Head передаётся отдельными
// сообщениями (см. summarizeChunk), а не дампится в текст промпта.
// context — сериализованные сообщения (например, recent предыдущей
// компактизации).
func BuildSummaryPrompt(previousSummary string, context []string) string {
	parts := make([]string, 0, len(context)+2)
	if previousSummary != "" {
		parts = append(parts, fmt.Sprintf("Update the anchored summary below using the conversation history above.\nPreserve still-true details, remove stale details, and merge in the new facts.\n<previous-summary>\n%s\n</previous-summary>", previousSummary))
	} else {
		parts = append(parts, "Create a new anchored summary from the conversation history above.")
	}
	parts = append(parts, SUMMARY_TEMPLATE)
	parts = append(parts, context...)
	return strings.Join(parts, "\n\n")
}

// recentContextToPrompt сериализует сообщения recent (хвост предыдущей
// компактизации) в строки контекста промпта суммаризации.
func recentContextToPrompt(msgs []tokenizers.Message) []string {
	ctx := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ctx = append(ctx, fmt.Sprintf("[%s]: %s", m.Role, m.Content))
	}
	return ctx
}

// OpenCodeCompactResult — результат opencode-компакшена.
type OpenCodeCompactResult struct {
	Summary      string               // текст summary
	SummaryMsg   tokenizers.Message   // сообщение с summary
	KeptTail     []tokenizers.Message // сохранённый хвост
	TokensBefore int                  // токенов до
	TokensAfter  int                  // токенов после
	// TailStartID — индекс первого сообщения сохранённого хвоста в исходной
	// истории (tail_start_id в opencode). -1 — хвост не сохраняется.
	TailStartID int
}

// CompactWithOpenCode выполняет сжатие в стиле opencode.
func (c *Compactor) CompactWithOpenCode(ctx context.Context, messages []tokenizers.Message, maxTokens int, tailTurns int, preserveRecentTokens *int) (*OpenCodeCompactResult, error) {
	tokensBefore := c.estimator.EstimateMessages(messages)
	budget := preserveRecentBudget(maxTokens, preserveRecentTokens)
	selected := SelectMessages(messages, tailTurns, budget)

	// Как в opencode processCompaction: head исключает пары «маркер+summary»
	// предыдущих компактизаций (hidden set), previousSummary передаётся отдельно.
	head := withoutCompactionPairs(selected.Head)
	tail := copyTail(messages, selected.TailStartID)

	if c.llm == nil {
		return nil, fmt.Errorf("LLMCompressorInterface not configured for compaction")
	}

	previousSummary, previousRecent := findPreviousCompaction(messages)
	if len(head) == 0 && previousSummary == "" {
		return nil, fmt.Errorf("no messages to compact and no previous summary")
	}

	summaryContent, err := c.summarizeHead(ctx, head, previousSummary, previousRecent, maxTokens)
	if err != nil {
		return nil, fmt.Errorf("LLM compaction failed: %w", err)
	}

	summaryMsg := tokenizers.Message{
		Role:    "assistant",
		Content: summaryContent,
		Summary: true,
	}

	result := &OpenCodeCompactResult{
		Summary:      summaryContent,
		SummaryMsg:   summaryMsg,
		KeptTail:     tail,
		TokensBefore: tokensBefore,
		TailStartID:  selected.TailStartID,
	}

	tailTokens := c.estimator.EstimateMessages(tail)
	summaryTokens := c.estimator.Estimate(summaryContent)
	result.TokensAfter = tailTokens + summaryTokens + 100

	return result, nil
}

const (
	// SUMMARY_CHUNK_OVERHEAD — резерв токенов под system-промпт и выход summary.
	SUMMARY_CHUNK_OVERHEAD = 8_192
	// MIN_SUMMARY_CHUNK_BUDGET — минимальный бюджет одного вызова суммаризации.
	MIN_SUMMARY_CHUNK_BUDGET = 1_024
)

// summaryChunkBudget вычисляет бюджет токенов одного вызова суммаризации.
func summaryChunkBudget(maxTokens int) int {
	usable := Usable(maxTokens, nil)
	budget := usable - SUMMARY_CHUNK_OVERHEAD
	half := usable / 2
	if budget < half {
		budget = half
	}
	if budget < MIN_SUMMARY_CHUNK_BUDGET {
		budget = MIN_SUMMARY_CHUNK_BUDGET
	}
	return budget
}

// summarizeHead суммирует head по кускам, каждый из которых укладывается в контекст.
// Summary накапливается: каждый следующий вызов обновляет summary предыдущего.
// previousRecent (recent предыдущей компактизации) попадает в контекст только
// первого вызова — как previousSummary.recent в opencode core compactAfterOverflow.
func (c *Compactor) summarizeHead(ctx context.Context, head []tokenizers.Message, previousSummary string, previousRecent []tokenizers.Message, maxTokens int) (string, error) {
	summary := previousSummary
	remaining := head
	first := true
	for len(remaining) > 0 {
		currentBudget := summaryChunkBudget(maxTokens) - c.estimator.Estimate(summary)
		if currentBudget < MIN_SUMMARY_CHUNK_BUDGET {
			currentBudget = MIN_SUMMARY_CHUNK_BUDGET
		}
		chunk, rest := takeOldestFit(remaining, currentBudget)
		if len(chunk) == 0 {
			chunk = []tokenizers.Message{truncateToBudget(remaining[0], currentBudget)}
			rest = remaining[1:]
		}
		next, err := c.summarizeChunk(ctx, summary, chunk, previousRecent, first)
		if err != nil {
			return "", err
		}
		summary = next
		remaining = rest
		first = false
	}
	return summary, nil
}

// summarizeChunk вызывает LLM для суммаризации одного куска head.
// Head передаётся отдельными сообщениями, промпт — только инструкция+шаблон
// (+ recent предыдущей компактизации в context).
func (c *Compactor) summarizeChunk(ctx context.Context, previousSummary string, chunk []tokenizers.Message, previousRecent []tokenizers.Message, includeRecent bool) (string, error) {
	var context []string
	if includeRecent && previousSummary != "" && len(previousRecent) > 0 {
		context = recentContextToPrompt(previousRecent)
	}
	prompt := BuildSummaryPrompt(previousSummary, context)
	systemPrompt := "You are a context compaction assistant. Extract and summarize the key information from the conversation into a structured Markdown summary."
	msgs := make([]tokenizers.Message, 0, len(chunk)+2)
	msgs = append(msgs, tokenizers.Message{Role: "system", Content: systemPrompt})
	msgs = append(msgs, chunk...)
	msgs = append(msgs, tokenizers.Message{Role: "user", Content: prompt})
	req := &CompressionRequest{
		Messages:     msgs,
		Strategy:     SummarizeStrategy,
		TargetTokens: 2000,
	}
	compResult, err := c.llm.Compress(ctx, req)
	if err != nil {
		return "", err
	}
	return extractSummary(compResult), nil
}

// extractSummary извлекает текст summary из результата сжатия.
func extractSummary(compResult *CompressionResult) string {
	for _, m := range compResult.CompressedMessages {
		if m.Role == "user" && m.Content != "" {
			return m.Content
		}
	}
	return compResult.Summary
}

// takeOldestFit возвращает самый длинный префикс messages, укладывающийся в budget.
// Если первое сообщение не влезает, возвращает пустой chunk — вызывающий обязан его обрезать.
func takeOldestFit(messages []tokenizers.Message, budget int) ([]tokenizers.Message, []tokenizers.Message) {
	if budget <= 0 || len(messages) == 0 {
		return nil, messages
	}
	total := 0
	for i := range messages {
		size := estimateMessagesTokens(messages[i : i+1])
		if total+size > budget {
			if i == 0 {
				return nil, messages
			}
			return messages[:i], messages[i:]
		}
		total += size
	}
	return messages, nil
}

// truncateToBudget обрезает содержимое сообщения до укладывания в budget токенов.
func truncateToBudget(msg tokenizers.Message, budget int) tokenizers.Message {
	// Резерв под роль, разметку и маркер обрезки.
	maxChars := (budget - 64) * 4
	if maxChars < 0 {
		maxChars = 0
	}
	if len(msg.Content) <= maxChars {
		return msg
	}
	msg.Content = msg.Content[:maxChars] + "\n[truncated for summarization]"
	return msg
}

// copyTail копирует хвост сообщений, начиная с tailStartID.
func copyTail(messages []tokenizers.Message, tailStartID int) []tokenizers.Message {
	if tailStartID > 0 {
		tail := make([]tokenizers.Message, len(messages)-tailStartID)
		copy(tail, messages[tailStartID:])
		return tail
	}
	if tailStartID == 0 {
		tail := make([]tokenizers.Message, len(messages))
		copy(tail, messages)
		return tail
	}
	return nil
}

// withoutCompactionPairs удаляет пары «маркер компактизации (user) + summary
// (assistant)» из сообщений. Как в opencode processCompaction: старые пары
// исключаются из контекста суммаризации (hidden set), а previousSummary
// передаётся отдельно.
func withoutCompactionPairs(messages []tokenizers.Message) []tokenizers.Message {
	result := make([]tokenizers.Message, 0, len(messages))
	skipNext := false
	for _, msg := range messages {
		if skipNext {
			skipNext = false
			continue
		}
		if msg.Role == "user" && msg.Content == tokenizers.CompactionUserMessage {
			skipNext = true
			continue
		}
		result = append(result, msg)
	}
	return result
}

// findPreviousCompaction ищет последний summary в сообщениях (как opencode
// completedCompactions().at(-1)) — обновляется последний, самый свежий.
// Возвращает текст summary и recent — хвост, сохранённый предыдущей
// компактизацией (messages[tailStartID:markerIdx]), который передаётся в
// контекст при повторной суммаризации.
func findPreviousCompaction(messages []tokenizers.Message) (summary string, recent []tokenizers.Message) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "assistant" || !messages[i].Summary {
			continue
		}
		markerIdx := -1
		for j := i - 1; j >= 0; j-- {
			if messages[j].Role == "user" {
				markerIdx = j
				break
			}
		}
		tailStart := messages[i].TailStartID
		if tailStart > 0 && markerIdx > tailStart && markerIdx < i {
			return messages[i].Content, messages[tailStart:markerIdx]
		}
		return messages[i].Content, nil
	}
	return "", nil
}
