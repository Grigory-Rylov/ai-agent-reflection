package tokenizers

import "fmt"


type Tokenizer interface {
	
	CountTokens(text string) (int, error)

	
	CountMessagesTokens(messages []Message) (int, error)

	
	Encode(text string) ([]int, error)

	
	Decode(tokens []int) (string, error)

	
	MaxContextLength() int

	
	Name() string
}


type ContextSize struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	MaxContextLength int
	IsWithinLimit    bool
}


func (cs *ContextSize) AddCompletion(tokens int) {
	cs.CompletionTokens += tokens
	cs.TotalTokens = cs.PromptTokens + cs.CompletionTokens
	cs.IsWithinLimit = cs.TotalTokens <= cs.MaxContextLength
}


func EstimateWithContext(promptTokens, completionTokens, maxContext int) *ContextSize {
	return &ContextSize{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
		MaxContextLength: maxContext,
		IsWithinLimit:    promptTokens+completionTokens <= maxContext,
	}
}


func EstimatePromptTokens(texts []string, tokenizer Tokenizer) (int, error) {
	total := 0
	for _, text := range texts {
		count, err := tokenizer.CountTokens(text)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}


func EstimateCompletionTokens(maxTokens int) int {
	return maxTokens
}


const CompactionUserMessage = "What did we do so far? Respond in the same language as the conversation."


const CompactionAutoContinueText = "Continue if you have next steps, or stop and ask for clarification if you are unsure how to proceed. Respond in the same language as the conversation."


const CompactionOverflowContinueText = "The previous request exceeded the provider's size limit due to large context. Continue if you have next steps, or stop and ask for clarification if you are unsure how to proceed. Respond in the same language as the conversation."


type Message struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"` 
	Name       string `json:"name,omitempty"`         
	Compacted  bool   `json:"compacted,omitempty"`    
	Summary    bool   `json:"summary,omitempty"`      
	
	
	
	TailStartID int `json:"tail_start_id,omitempty"`
}


func (m Message) String() string {
	return fmt.Sprintf("[%s] %s", m.Role, m.Content)
}
