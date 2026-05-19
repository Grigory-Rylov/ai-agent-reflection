package compress

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// LLMCompressor — компрессор через llama-server API
// ============================================================

// LLMCompressor использует модель для сжатия контекста
type LLMCompressor struct {
	serverURL   string
	model       string
	client      *http.Client
	temperature float64
}

// NewLLMCompressor создаёт новый компрессор через llama-server
func NewLLMCompressor(serverURL, model string, temperature float64) *LLMCompressor {
	return &LLMCompressor{
		serverURL:   serverURL,
		model:       model,
		temperature: temperature,
		client: &http.Client{
			Timeout: 2 * time.Minute,
		},
	}
}

// Compress сжимает контекст путём отправки запроса модели
func (c *LLMCompressor) Compress(ctx context.Context, req *CompressionRequest) (*CompressionResult, error) {
	if req.TargetTokens <= 0 {
		req.TargetTokens = 2000
	}

	// Считаем токены до сжатия
	originalTokens := c.simpleCountTokens(req.Messages)

	// Формируем промпт для сжатия
	systemPrompt := c.buildCompressionSystemPrompt(req)
	userPrompt := c.buildCompressionUserPrompt(req.Messages, req.Strategy, req.TargetTokens)

	// Отправляем запрос на сжатие
	compressedText, summary, err := c.sendCompressionRequest(ctx, systemPrompt, userPrompt, req.TargetTokens)
	if err != nil {
		return nil, fmt.Errorf("compression request failed: %w", err)
	}

	// Считаем токены после сжатия
	compressedTokens, _ := c.countTextTokens(compressedText)

	// Создаём сжатые сообщения
	var compressedMessages []tokenizers.Message
	if req.Strategy == SummarizeStrategy {
		// Если суммаризация — добавляем резюме как системное сообщение
		compressedMessages = []tokenizers.Message{
			{
				Role:    "system",
				Content: fmt.Sprintf("[SUMMARY] %s", summary),
			},
			{
				Role:    "user",
				Content: compressedText,
			},
		}
	} else {
		// Если обрезка — оставляем только последние сообщения
		compressedMessages = c.truncateMessages(req.Messages, req.TargetTokens)
	}

	ratio := CalculateCompressionRatio(originalTokens, compressedTokens)

	return &CompressionResult{
		OriginalTokens:     originalTokens,
		CompressedTokens:   compressedTokens,
		CompressionRatio:   ratio,
		CompressedMessages: compressedMessages,
		Summary:            summary,
		CompressedAt:       time.Now(),
	}, nil
}

// buildCompressionSystemPrompt формирует системный промпт для сжатия
func (c *LLMCompressor) buildCompressionSystemPrompt(req *CompressionRequest) string {
	strategyHint := ""
	switch req.Strategy {
	case SummarizeStrategy:
		strategyHint = "Please provide a concise summary of the conversation."
	case TruncateStrategy:
		strategyHint = "Keep only the most important parts of the conversation."
	case HybridStrategy:
		strategyHint = "Summarize the key points and keep only essential information."
	}

	return fmt.Sprintf(`You are a context compression assistant. Your task is to compress conversation history while preserving essential information.

%s

Important:
- Preserve key facts, decisions, and context
- Remove redundant or repetitive information
- Maintain the flow and meaning of the conversation
- Keep the response concise and focused`, strategyHint)
}

// buildCompressionUserPrompt формирует пользовательский промпт для сжатия
func (c *LLMCompressor) buildCompressionUserPrompt(messages []tokenizers.Message, strategy CompressionStrategy, targetTokens int) string {
	var sb strings.Builder
	sb.WriteString("Please compress the following conversation:\n\n")

	for _, msg := range messages {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", msg.Role, msg.Content))
	}

	if strategy == SummarizeStrategy {
		sb.WriteString("Provide a concise summary that captures the essence of this conversation.\n")
	} else {
		sb.WriteString("Keep only the most essential parts of this conversation.\n")
	}

	sb.WriteString(fmt.Sprintf("Target: approximately %d tokens maximum.\n", targetTokens))

	return sb.String()
}

// sendCompressionRequest отправляет запрос на сжатие к модели
func (c *LLMCompressor) sendCompressionRequest(ctx context.Context, systemPrompt, userPrompt string, targetTokens int) (string, string, error) {
	reqBody := map[string]interface{}{
		"model":      c.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"max_tokens":  targetTokens,
		"temperature": c.temperature,
		"stream":      false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", fmt.Errorf("marshal request: %w", err)
	}

	reqURL := fmt.Sprintf("%s/v1/chat/completions", c.serverURL)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(jsonData))
	if err != nil {
		return "", "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	// Парсим ответ
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return "", "", fmt.Errorf("decode response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return "", "", fmt.Errorf("no response from model")
	}

	compressedText := apiResponse.Choices[0].Message.Content
	summary := fmt.Sprintf("Summary: %d → %d tokens", 0, apiResponse.Usage.CompletionTokens)

	return compressedText, summary, nil
}

// countRequestTokens подсчитывает токены в запросе
func (c *LLMCompressor) countRequestTokens(ctx context.Context, messages []tokenizers.Message) (int, error) {
	if len(messages) == 0 {
		return 0, nil
	}
	tokenizer := tokenizers.NewLlamaServerTokenizer(c.serverURL, c.model, 8192)
	return tokenizer.CountMessagesTokens(messages)
}

// simpleCountTokens — простой подсчёт без API
func (c *LLMCompressor) simpleCountTokens(messages []tokenizers.Message) int {
	total := 0
	for _, msg := range messages {
		tokens := len(strings.Fields(msg.Content))
		total += tokens + 2 // +2 для роли
	}
	return total
}

// countTextTokens подсчитывает токены в тексте
func (c *LLMCompressor) countTextTokens(text string) (int, error) {
	tokenizer := tokenizers.NewLlamaServerTokenizer(c.serverURL, c.model, 8192)
	return tokenizer.CountTokens(text)
}

// truncateMessages обрезает сообщения до целевого размера
func (c *LLMCompressor) truncateMessages(messages []tokenizers.Message, targetTokens int) []tokenizers.Message {
	if len(messages) <= 2 {
		return messages
	}

	// Сохраняем первые 2 и последние N сообщений
	keepLast := 4
	if len(messages) > keepLast+2 {
		return append(messages[:2], messages[len(messages)-keepLast:]...)
	}

	return messages
}

// CheckTrigger проверяет нужно ли сжимать контекст
func (c *LLMCompressor) CheckTrigger(currentTokens, maxTokens int) bool {
	return ShouldCompress(currentTokens, maxTokens, DefaultCompressionTrigger())
}

// Name возвращает имя компрессора
func (c *LLMCompressor) Name() string {
	return "llm-compressor"
}
