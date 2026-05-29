package compress

import "github.com/opencode/llama-client/pkg/tokenizers"

type CompactionMarker struct {
	Index       int
	TailStartID int
	Summary     string
}

func FindCompactionMarkers(messages []tokenizers.Message) []CompactionMarker {
	var markers []CompactionMarker
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && messages[i].Summary {
			parentID := -1
			for j := i - 1; j >= 0; j-- {
				if messages[j].Role == "user" {
					parentID = j
					break
				}
			}
			if parentID >= 0 {
				markers = append(markers, CompactionMarker{
					Index:   i,
					Summary: messages[i].Content,
				})
			}
		}
	}
	return markers
}

func FilterCompacted(messages []tokenizers.Message) []tokenizers.Message {
	markers := FindCompactionMarkers(messages)
	if len(markers) == 0 {
		return messages
	}

	latest := markers[0]
	compactionUserIdx := -1
	for j := latest.Index - 1; j >= 0; j-- {
		if messages[j].Role == "user" {
			compactionUserIdx = j
			break
		}
	}
	if compactionUserIdx < 0 {
		return messages
	}

	tailStartID := compactionUserIdx + 1
	result := make([]tokenizers.Message, 0, len(messages))

	result = append(result, messages[compactionUserIdx])
	result = append(result, messages[latest.Index])

	for i := tailStartID; i < len(messages); i++ {
		if i == latest.Index {
			continue
		}
		result = append(result, messages[i])
	}

	return result
}
