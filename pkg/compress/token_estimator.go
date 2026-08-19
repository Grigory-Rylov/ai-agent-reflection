package compress

import (
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
)


type TokenEstimator interface {
	Estimate(text string) int
	EstimateMessages(messages []tokenizers.Message) int
}


type SimpleEstimator struct{}


func EstimateTokensSimple(text string) int {
	if len(text) == 0 {
		return 0
	}
	
	return (len(text) + 3) / 4
}


func EstimateMessagesTokensSimple(messages []tokenizers.Message) int {
	total := 0
	for _, msg := range messages {
		
		total += EstimateTokensSimple(msg.Content) + 4
	}
	return total
}


func (e *SimpleEstimator) Estimate(text string) int {
	return EstimateTokensSimple(text)
}


func (e *SimpleEstimator) EstimateMessages(messages []tokenizers.Message) int {
	return EstimateMessagesTokensSimple(messages)
}


type Message = tokenizers.Message
