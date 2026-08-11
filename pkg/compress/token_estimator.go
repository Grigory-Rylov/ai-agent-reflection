package compress

import (
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
)

// ============================================================
// TokenEstimator — интерфейс для оценки токенов
// ============================================================

// TokenEstimator — интерфейс для оценки токенов.
type TokenEstimator interface {
	Estimate(text string) int
	EstimateMessages(messages []tokenizers.Message) int
}

// ============================================================
// SimpleEstimator — простая реализация TokenEstimator
// ============================================================

// SimpleEstimator — простая реализация TokenEstimator.
type SimpleEstimator struct{}

// EstimateTokensSimple оценивает количество токенов по тексту.
// Единая стратегия (как opencode Token.estimate): 1 токен ≈ 4 символа,
// без codeFactor/структурных бонусов — чтобы выбор head/tail, pruning и
// overflow-проверка использовали одну и ту же оценку.
func EstimateTokensSimple(text string) int {
	if len(text) == 0 {
		return 0
	}
	// Math.ceil(len/4) в opencode
	return (len(text) + 3) / 4
}

// EstimateMessagesTokensSimple оценивает токены в сообщениях.
func EstimateMessagesTokensSimple(messages []tokenizers.Message) int {
	total := 0
	for _, msg := range messages {
		// +4 токена на role и форматирование
		total += EstimateTokensSimple(msg.Content) + 4
	}
	return total
}

// Estimate оценивает количество токенов по тексту.
func (e *SimpleEstimator) Estimate(text string) int {
	return EstimateTokensSimple(text)
}

// EstimateMessages оценивает токены в сообщениях.
func (e *SimpleEstimator) EstimateMessages(messages []tokenizers.Message) int {
	return EstimateMessagesTokensSimple(messages)
}

// ============================================================
// Message alias for compatibility
// ============================================================

// Message — alias for tokenizers.Message to avoid import cycle.
type Message = tokenizers.Message
