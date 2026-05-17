package tokenizers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ============================================================
// LlamaServerTokenizer — токенайзер через llama-server API
// ============================================================

// LlamaServerTokenizer использует llama-server для подсчёта токенов
type LlamaServerTokenizer struct {
	serverURL        string
	model            string
	maxTokens        int
	client           *http.Client
}

// NewLlamaServerTokenizer создаёт новый токенайзер через llama-server
func NewLlamaServerTokenizer(serverURL, model string, maxTokens int) *LlamaServerTokenizer {
	return &LlamaServerTokenizer{
		serverURL:   serverURL,
		model:       model,
		maxTokens:   maxTokens,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// CountTokens отправляет запрос к llama-server для подсчёта токенов
func (t *LlamaServerTokenizer) CountTokens(text string) (int, error) {
	if text == "" {
		return 0, nil
	}

	reqBody := map[string]interface{}{
		"model":      t.model,
		"messages":   []map[string]string{{"role": "user", "content": text}},
		"max_tokens": 1,
		"stream":     false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("marshal request: %w", err)
	}

	reqURL := fmt.Sprintf("%s/v1/chat/completions", t.serverURL)
	req, err := http.NewRequestWithContext(context.Background(), "POST", reqURL, bytes.NewReader(jsonData))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	// Парсим ответ
	var apiResponse struct {
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	return apiResponse.Usage.PromptTokens, nil
}

// Encode — не поддерживается через llama-server
func (t *LlamaServerTokenizer) Encode(text string) ([]int, error) {
	return nil, fmt.Errorf("encode not supported by llama-server tokenizer")
}

// Decode — не поддерживается через llama-server
func (t *LlamaServerTokenizer) Decode(tokens []int) (string, error) {
	return "", fmt.Errorf("decode not supported by llama-server tokenizer")
}

// MaxContextLength возвращает максимальную длину контекста
func (t *LlamaServerTokenizer) MaxContextLength() int {
	return t.maxTokens
}

// Name возвращает имя токенайзера
func (t *LlamaServerTokenizer) Name() string {
	return "llama-server-" + t.model
}
