package parser

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// EventType определяет тип SSE-события
type EventType string

const (
	EventDelta     EventType = "delta"     // Новый токен ответа
	EventReasoning EventType = "reasoning" // Токен размышления
	EventStop      EventType = "stop"      // Завершение генерации
	EventDone      EventType = "done"      // Маркер окончания потока [DONE]
)

// DeltaChunk — вложенная структура delta из ответа
type DeltaChunk struct {
	Role           string `json:"role"`
	Content        string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
}

// SSEChunk — распарсенный чанк из SSE-потока
type SSEChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`

	// Выбор из ответа
	ChoiceIndex    int         `json:"choice_index"`
	FinishReason   *string     `json:"finish_reason"`
	Delta          DeltaChunk  `json:"delta"`
	Content        string      `json:"-"`
	ReasoningContent string   `json:"-"`

	// Тайминги (из финального события)
	Timings *Timings `json:"timings"`
}

// Timings содержит статистику генерации
type Timings struct {
	PromptN            int64   `json:"prompt_n"`
	PromptMS           float64 `json:"prompt_ms"`
	PromptPerTokenMS   float64 `json:"prompt_per_token_ms"`
	PromptPerSecond    float64 `json:"prompt_per_second"`
	PredictedN         int64   `json:"predicted_n"`
	PredictedMS        float64 `json:"predicted_ms"`
	PredictedPerTokenMS float64 `json:"predicted_per_token_ms"`
	PredictedPerSecond float64 `json:"predicted_per_second"`
}

// UnmarshalJSON кастомный парсинг для извлечения content из delta
func (c *SSEChunk) UnmarshalJSON(data []byte) error {
	// Парсим в промежуточную структуру
	var raw struct {
		ID           string     `json:"id"`
		Object       string     `json:"object"`
		Created      int64      `json:"created"`
		Model        string     `json:"model"`
		Choices      []struct {
			Index        int         `json:"index"`
			FinishReason *string     `json:"finish_reason"`
			Delta        DeltaChunk  `json:"delta"`
		} `json:"choices"`
		Timings *Timings `json:"timings"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Копируем поля
	c.ID = raw.ID
	c.Object = raw.Object
	c.Created = raw.Created
	c.Model = raw.Model
	c.ChoiceIndex = 0
	c.FinishReason = raw.Choices[0].FinishReason
	c.Timings = raw.Timings

	if len(raw.Choices) > 0 {
		c.ChoiceIndex = raw.Choices[0].Index
		c.Delta = raw.Choices[0].Delta
		c.Content = raw.Choices[0].Delta.Content
		c.ReasoningContent = raw.Choices[0].Delta.ReasoningContent
	}

	return nil
}

// EventType возвращает тип события на основе содержимого чанка
func (c SSEChunk) EventType() EventType {
	if c.FinishReason != nil {
		return EventStop
	}
	if c.ReasoningContent != "" {
		return EventReasoning
	}
	if c.Content != "" {
		return EventDelta
	}
	return ""
}

// IsCompletion возвращает true если генерация завершена
func (c SSEChunk) IsCompletion() bool {
	return c.FinishReason != nil
}

// IsStopReason возвращает true если есть причина остановки
func (c SSEChunk) IsStopReason() bool {
	if c.FinishReason == nil {
		return false
	}
	return *c.FinishReason == "stop" || *c.FinishReason == "length" || *c.FinishReason == "eoi" || *c.FinishReason == "eos"
}

// ============================================================
// SSE-парсер
// ============================================================

// Parser парсит SSE-поток из llama-server
type Parser struct {
	reader *bufio.Reader
}

// NewParser создаёт новый парсер
func NewParser(r io.Reader) *Parser {
	return &Parser{
		reader: bufio.NewReader(r),
	}
}

// ParseChunk читает одну строку и возвращает тип события + данные
func (p *Parser) ParseChunk() (EventType, string, error) {
	line, err := p.reader.ReadSlice('\n')
	if err != nil {
		if err == io.EOF {
			return "", "", io.EOF
		}
		return "", "", fmt.Errorf("failed to read line: %w", err)
	}

	// Убираем перевод строки
	lineStr := strings.TrimSpace(string(line))

	// Пустая строка — пропуск
	if lineStr == "" {
		return p.ParseChunk() // Рекурсивно читаем следующую строку
	}

	// Маркер [DONE]
	if strings.Contains(lineStr, "[DONE]") {
		return EventDone, "", nil
	}

	// Проверяем формат "data: {json}"
	if !strings.HasPrefix(lineStr, "data: ") {
		return "", "", fmt.Errorf("invalid SSE format: %s", lineStr)
	}

	// Извлекаем JSON
	jsonData := strings.TrimPrefix(lineStr, "data: ")
	return "", jsonData, nil
}

// ParseStream читает весь SSE-поток и возвращает чанки
func (p *Parser) ParseStream() ([]SSEChunk, EventType, error) {
	var chunks []SSEChunk
	var lastEventType EventType

	for {
		eventType, jsonData, err := p.ParseChunk()
		if err != nil {
			return chunks, lastEventType, err
		}

		// Обработка [DONE]
		if eventType == EventDone {
			return chunks, EventDone, nil
		}

		// Парсим JSON
		var chunk SSEChunk
		if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
			continue // Пропускаем невалидные JSON
		}

		chunks = append(chunks, chunk)
		lastEventType = eventType
	}
}

// ============================================================
// Утилиты для тестов
// ============================================================

// ParseSSELine парсит одну строку SSE в SSEChunk (для тестов)
func ParseSSELine(line string) (SSEChunk, error) {
	if !strings.HasPrefix(line, "data: ") {
		return SSEChunk{}, fmt.Errorf("invalid SSE format: %s", line)
	}

	jsonData := strings.TrimPrefix(line, "data: ")
	var chunk SSEChunk
	if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
		return SSEChunk{}, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return chunk, nil
}

// CountChunksByType считает количество чанков по типам
func CountChunksByType(chunks []SSEChunk) (delta, reasoning, stop int) {
	for _, c := range chunks {
		switch c.EventType() {
		case EventDelta:
			delta++
		case EventReasoning:
			reasoning++
		case EventStop:
			stop++
		}
	}
	return
}

// ExtractContent собирает полный контент ответа
func ExtractContent(chunks []SSEChunk) string {
	var result strings.Builder
	for _, c := range chunks {
		if c.Content != "" {
			result.WriteString(c.Content)
		}
	}
	return result.String()
}

// ExtractReasoning собирает полное содержимое размышлений
func ExtractReasoning(chunks []SSEChunk) string {
	var result strings.Builder
	for _, c := range chunks {
		if c.ReasoningContent != "" {
			result.WriteString(c.ReasoningContent)
		}
	}
	return result.String()
}
