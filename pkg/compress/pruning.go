package compress

import (
	"github.com/opencode/llama-client/pkg/tokenizers"
)

const (
	PRUNE_MINIMUM        = 20_000
	PRUNE_PROTECT        = 40_000
	PRUNE_PROTECT_TURNS  = 2
	PRUNED_OUTPUT_PLACEHOLDER = "[Old tool result content cleared]"
)

type PruneConfig struct {
	Prune     bool
}

func DefaultPruneConfig() PruneConfig {
	return PruneConfig{Prune: true}
}

func PruneMessages(messages []tokenizers.Message) []tokenizers.Message {
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
