package compress

import (
	"context"
	"fmt"
	"strings"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
)


const (
	DEFAULT_TAIL_TURNS         = 2
	MIN_PRESERVE_RECENT_TOKENS = 2_000
	MAX_PRESERVE_RECENT_TOKENS = 8_000

	TOOL_OUTPUT_MAX_CHARS = 2000
)


func TruncateToolOutput(content string) string {
	if len(content) <= TOOL_OUTPUT_MAX_CHARS {
		return content
	}
	head := content[:TOOL_OUTPUT_MAX_CHARS] + "\n[truncated]"
	
	
	if idx := strings.LastIndex(content, "Full output saved to:"); idx >= 0 {
		return head + "\n" + content[idx:]
	}
	return head
}


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


type Turn struct {
	Start int 
	End   int 
}


type SelectResult struct {
	Head        []tokenizers.Message 
	TailStartID int                  
}


func PreserveRecentBudget(maxTokens int, preserveRecent *int) int {
	return preserveRecentBudget(maxTokens, preserveRecent)
}


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


func estimateMessagesTokens(messages []tokenizers.Message) int {
	return EstimateMessagesTokensSimple(messages)
}


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

	
	
	if keepStart <= 0 {
		return SelectResult{Head: messages, TailStartID: -1}
	}

	
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
		Head:        messages[:tailStartID],
		TailStartID: tailStartID,
	}
}


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


func recentContextToPrompt(msgs []tokenizers.Message) []string {
	ctx := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ctx = append(ctx, fmt.Sprintf("[%s]: %s", m.Role, m.Content))
	}
	return ctx
}


type OpenCodeCompactResult struct {
	Summary      string               
	SummaryMsg   tokenizers.Message   
	KeptTail     []tokenizers.Message 
	TokensBefore int                  
	TokensAfter  int                  
	
	
	TailStartID int
}


func (c *Compactor) CompactWithOpenCode(ctx context.Context, messages []tokenizers.Message, maxTokens int, tailTurns int, preserveRecentTokens *int) (*OpenCodeCompactResult, error) {
	tokensBefore := c.estimator.EstimateMessages(messages)
	budget := preserveRecentBudget(maxTokens, preserveRecentTokens)
	selected := SelectMessages(messages, tailTurns, budget)

	
	
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
	
	SUMMARY_CHUNK_OVERHEAD = 8_192
	
	MIN_SUMMARY_CHUNK_BUDGET = 1_024
)


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


func extractSummary(compResult *CompressionResult) string {
	for _, m := range compResult.CompressedMessages {
		if m.Role == "user" && m.Content != "" {
			return m.Content
		}
	}
	return compResult.Summary
}


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


func truncateToBudget(msg tokenizers.Message, budget int) tokenizers.Message {
	
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
