package compress

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// Opencode-style Compaction — select(), Summary, LLM-based
// ============================================================

const (
	DEFAULT_TAIL_TURNS         = 2
	MIN_PRESERVE_RECENT_TOKENS = 2_000
	MAX_PRESERVE_RECENT_TOKENS = 8_000
)

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
type SelectResult struct {
	Head        []tokenizers.Message // сообщения для сжатия
	TailStartID int                   // индекс первого сообщения хвоста
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
func userTurns(messages []tokenizers.Message) []Turn {
	var result []Turn
	for i, msg := range messages {
		if msg.Role != "user" {
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

	if keepStart < 0 {
		return SelectResult{Head: messages, TailStartID: -1}
	}

	if keepStart == 0 {
		return SelectResult{
			Head:        nil,
			TailStartID: 0,
		}
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

	return SelectResult{
		Head:        messages[:keepStart],
		TailStartID: tailStartID,
	}
}

// BuildSummaryPrompt строит промпт для суммаризации.
func BuildSummaryPrompt(previousSummary string, headMessages []tokenizers.Message) string {
	var sb strings.Builder
	for _, msg := range headMessages {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", msg.Role, msg.Content))
	}
	if previousSummary != "" {
		sb.WriteString(fmt.Sprintf("Update the anchored summary below using the conversation history above.\nPreserve still-true details, remove stale details, and merge in the new facts.\n<previous-summary>\n%s\n</previous-summary>\n\n", previousSummary))
	} else {
		sb.WriteString("Create a new anchored summary from the conversation history above.\n\n")
	}
	sb.WriteString(SUMMARY_TEMPLATE)
	return sb.String()
}

// OpenCodeCompactResult — результат opencode-компакшена.
type OpenCodeCompactResult struct {
	Summary      string                  // текст summary
	SummaryMsg   tokenizers.Message      // сообщение с summary
	KeptTail     []tokenizers.Message    // сохранённый хвост
	TokensBefore int                     // токенов до
	TokensAfter  int                     // токенов после
}

// CompactWithOpenCode выполняет сжатие в стиле opencode.
func (c *Compactor) CompactWithOpenCode(ctx context.Context, messages []tokenizers.Message, maxTokens int, tailTurns int, preserveRecentTokens *int) (*OpenCodeCompactResult, error) {
	tokensBefore := c.estimator.EstimateMessages(messages)
	budget := preserveRecentBudget(maxTokens, preserveRecentTokens)
	selected := SelectMessages(messages, tailTurns, budget)

	head := selected.Head
	var tail []tokenizers.Message
	if selected.TailStartID > 0 {
		tail = make([]tokenizers.Message, len(messages)-selected.TailStartID)
		copy(tail, messages[selected.TailStartID:])
	} else if selected.TailStartID == 0 {
		tail = make([]tokenizers.Message, len(messages))
		copy(tail, messages)
	}

	if c.llm == nil {
		return nil, fmt.Errorf("LLMCompressorInterface not configured for compaction")
	}

	// Ищем предыдущий summary
	previousSummary := ""
	for _, msg := range messages {
		if msg.Role == "assistant" && msg.Summary {
			previousSummary = msg.Content
		}
	}

	if len(head) == 0 && previousSummary == "" {
		return nil, fmt.Errorf("no messages to compact and no previous summary")
	}

	prompt := BuildSummaryPrompt(previousSummary, head)
	systemPrompt := "You are a context compaction assistant. Extract and summarize the key information from the conversation into a structured Markdown summary."

	req := &CompressionRequest{
		Messages: []tokenizers.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: prompt},
		},
		Strategy:     SummarizeStrategy,
		TargetTokens: 2000,
	}

	compResult, err := c.llm.Compress(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM compaction failed: %w", err)
	}

	// Извлекаем summary из результата (raw LLM ответ в user-сообщении)
	summaryContent := compResult.Summary
	for _, m := range compResult.CompressedMessages {
		if m.Role == "user" && m.Content != "" {
			summaryContent = m.Content
			break
		}
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
	}

	tailTokens := c.estimator.EstimateMessages(tail)
	summaryTokens := c.estimator.Estimate(summaryContent)
	result.TokensAfter = tailTokens + summaryTokens + 100

	return result, nil
}
