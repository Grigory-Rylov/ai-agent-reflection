package compress

import (
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
)


type CompressionStrategy string

const (
	
	SummarizeStrategy CompressionStrategy = "summarize"
)


type CompressionRequest struct {
	
	Messages []tokenizers.Message
	
	Strategy CompressionStrategy
	
	TargetTokens int
	
	MaxCompressionRatio float64
}


type CompressionResult struct {
	
	OriginalTokens int
	
	CompressedTokens int
	
	CompressionRatio float64
	
	CompressedMessages []tokenizers.Message
	
	Summary string
	
	CompressedAt time.Time
}


func CalculateCompressionRatio(original, compressed int) float64 {
	if original == 0 {
		return 1.0
	}
	return float64(compressed) / float64(original)
}
