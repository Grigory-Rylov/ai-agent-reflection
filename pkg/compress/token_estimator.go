package compress

import (
	"strings"

	"github.com/opencode/llama-client/pkg/tokenizers"
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
// Использует эвристику: 1 токен ≈ 4 символа для английского, 2-3 для кода.
func EstimateTokensSimple(text string) int {
	if len(text) == 0 {
		return 0
	}

	// Базовая оценка: 4 символа на токен
	charCount := len(text)

	// Корректировка для разных типов контента
	newlines := strings.Count(text, "\n")
	spaces := strings.Count(text, " ")

	// Код и структурированный текст имеют больше токенов
	codeFactor := 1.0
	if strings.Contains(text, "{") || strings.Contains(text, "func ") {
		codeFactor = 1.3
	}

	// Эвристика: (символы / 4) + поправка на структуру
	baseTokens := float64(charCount) / 4.0
	structureBonus := float64(newlines+spaces) / 20.0

	return int((baseTokens + structureBonus) * codeFactor)
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
