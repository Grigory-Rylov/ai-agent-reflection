package compress

import (
	"github.com/opencode/llama-client/pkg/tokenizers"
)

const (
	PRUNE_MINIMUM             = 20_000
	PRUNE_PROTECT             = 40_000
	PRUNE_PROTECT_TURNS       = 2
	PRUNED_OUTPUT_PLACEHOLDER = "[Old tool result content cleared]"
)

// PRUNE_PROTECTED_TOOLS — имена инструментов, чьи выводы не обрезаются
// (как opencode PRUNE_PROTECTED_TOOLS = ["skill"]).
var PRUNE_PROTECTED_TOOLS = []string{"skill"}

type PruneConfig struct {
	Prune bool
}

func DefaultPruneConfig() PruneConfig {
	return PruneConfig{Prune: true}
}

func PruneMessages(messages []tokenizers.Message, protectedTools ...string) []tokenizers.Message {
	var total int
	var pruned int
	toPrune := make([]int, 0)
	turns := 0
	msgCount := 0

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == "user" {
			turns++
		}
		if turns < PRUNE_PROTECT_TURNS {
			continue
		}
		if msg.Role == "assistant" && msg.Summary {
			break
		}

		if msg.Role != "tool" {
			continue
		}
		// Защищённые инструменты пропускаются ДО проверки compacted (как opencode).
		protected := false
		for _, name := range protectedTools {
			if msg.Name == name {
				protected = true
				break
			}
		}
		if protected {
			continue
		}
		if msg.Compacted {
			break
		}

		estimate := EstimateTokensSimple(msg.Content)
		total += estimate
		if total <= PRUNE_PROTECT {
			continue
		}
		pruned += estimate
		toPrune = append(toPrune, i)
		msgCount++
	}

	if pruned <= PRUNE_MINIMUM {
		return messages
	}

	result := make([]tokenizers.Message, len(messages))
	copy(result, messages)
	for _, idx := range toPrune {
		result[idx] = tokenizers.Message{
			Role:      "tool",
			Content:   PRUNED_OUTPUT_PLACEHOLDER,
			Compacted: true,
		}
	}
	return result
}
