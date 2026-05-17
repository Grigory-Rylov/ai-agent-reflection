package tokenizers

import (
	"fmt"
	"strings"
)

// ============================================================
// ContextCounter — счётчик контекста для агента
// ============================================================

// MessageTokenInfo информация о токенах сообщения
type MessageTokenInfo struct {
	Role       string `json:"role"`
	ContentLen int    `json:"content_length"`
	Tokens     int    `json:"tokens"`
}

// ContextStats полная статистика контекста
type ContextStats struct {
	SystemTokens   int              `json:"system_tokens"`
	Messages       []MessageTokenInfo `json:"messages"`
	TotalTokens    int              `json:"total_tokens"`
	MaxTokens      int              `json:"max_tokens"`
	Remaining      int              `json:"remaining"`
	IsFull         bool             `json:"is_full"`
	Warning        bool             `json:"warning"`
}

// ContextCounter подсчитывает и управляет контекстом
type ContextCounter struct {
	tokenizer Tokenizer
	maxTokens int
	systemMsg string
}

// NewContextCounter создаёт новый счётчик контекста
func NewContextCounter(tokenizer Tokenizer, maxTokens int) *ContextCounter {
	return &ContextCounter{
		tokenizer: tokenizer,
		maxTokens: maxTokens,
	}
}

// SetSystemMessage устанавливает системное сообщение
func (c *ContextCounter) SetSystemMessage(msg string) {
	c.systemMsg = msg
}

// CountMessageTokens подсчитывает токены для сообщения
func (c *ContextCounter) CountMessageTokens(role, content string) (int, error) {
	// Добавляем служебные токены для роли
	tokens := 2 // [system/user/assistant]

	// Подсчитываем контент
	contentTokens, err := c.tokenizer.CountTokens(content)
	if err != nil {
		return 0, err
	}

	return tokens + contentTokens, nil
}

// CountFullContext подсчитывает полный контекст
func (c *ContextCounter) CountFullContext(messages []Message) (*ContextStats, error) {
	stats := &ContextStats{
		Messages:  make([]MessageTokenInfo, 0, len(messages)),
		MaxTokens: c.maxTokens,
	}

	// Системное сообщение
	if c.systemMsg != "" {
		systemTokens, err := c.tokenizer.CountTokens(c.systemMsg)
		if err != nil {
			return nil, err
		}
		stats.SystemTokens = systemTokens
		stats.TotalTokens += systemTokens
	}

	// Добавляем служебные токены
	stats.TotalTokens += 4 // Системные служебные токены

	// Подсчитываем каждое сообщение
	for _, msg := range messages {
		tokens, err := c.CountMessageTokens(msg.Role, msg.Content)
		if err != nil {
			return nil, err
		}

		stats.Messages = append(stats.Messages, MessageTokenInfo{
			Role:       msg.Role,
			ContentLen: len(msg.Content),
			Tokens:     tokens,
		})
		stats.TotalTokens += tokens
	}

	// Обновляем статус
	stats.Remaining = c.maxTokens - stats.TotalTokens
	stats.IsFull = stats.Remaining <= 0
	stats.Warning = stats.Remaining < 500

	return stats, nil
}

// ShouldTruncate определяет нужно ли обрезать контекст
func (c *ContextCounter) ShouldTruncate(stats *ContextStats) bool {
	return stats != nil && (stats.IsFull || stats.Warning)
}

// FormatStats форматирует статистику контекста для отображения
func FormatStats(stats *ContextStats) string {
	if stats == nil {
		return "Нет данных о контексте"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Контекст: %d/%d токенов", stats.TotalTokens, stats.MaxTokens))

	if stats.SystemTokens > 0 {
		sb.WriteString(fmt.Sprintf(" (система: %d)", stats.SystemTokens))
	}

	if stats.Warning {
		sb.WriteString(" ⚠️ ВНИМАНИЕ: Мало места!")
	}

	if stats.IsFull {
		sb.WriteString(" ❌ КОНТЕКСТ ПОЛНЫЙ!")
	}

	return sb.String()
}
