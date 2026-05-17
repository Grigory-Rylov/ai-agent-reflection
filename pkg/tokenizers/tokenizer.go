package tokenizers

import "fmt"

// ============================================================
// Tokenizer Interface — интерфейс для подсчёта токенов
// ============================================================

// Tokenizer определяет интерфейс для токенайзера
type Tokenizer interface {
	// CountTokens возвращает количество токенов в тексте
	CountTokens(text string) (int, error)

	// Encode кодирует текст в массив токенов
	Encode(text string) ([]int, error)

	// Decode декодирует массив токенов обратно в текст
	Decode(tokens []int) (string, error)

	// MaxContextLength возвращает максимальную длину контекста
	MaxContextLength() int

	// Name возвращает имя токенайзера
	Name() string
}

// ContextSize представляет размер контекста в токенах
type ContextSize struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	MaxContextLength int
	IsWithinLimit    bool
}

// AddCompletion добавляет количество токенов завершения
func (cs *ContextSize) AddCompletion(tokens int) {
	cs.CompletionTokens += tokens
	cs.TotalTokens = cs.PromptTokens + cs.CompletionTokens
	cs.IsWithinLimit = cs.TotalTokens <= cs.MaxContextLength
}

// EstimateWithContext оценивает контекст с максимальной длиной
func EstimateWithContext(promptTokens, completionTokens, maxContext int) *ContextSize {
	return &ContextSize{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
		MaxContextLength: maxContext,
		IsWithinLimit:    promptTokens+completionTokens <= maxContext,
	}
}

// EstimatePromptTokens оценивает количество токенов в промпте
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

// EstimateCompletionTokens оценивает ожидаемое количество токенов ответа
func EstimateCompletionTokens(maxTokens int) int {
	return maxTokens
}

// Message представляет сообщение в чате
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// String возвращает строковое представление сообщения
func (m Message) String() string {
	return fmt.Sprintf("[%s] %s", m.Role, m.Content)
}
