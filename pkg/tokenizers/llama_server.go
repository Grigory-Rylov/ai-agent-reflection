package tokenizers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
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
	debug            bool
}

// NewLlamaServerTokenizer создаёт новый токенайзер через llama-server
func NewLlamaServerTokenizer(serverURL, model string, maxTokens int) *LlamaServerTokenizer {
	return &LlamaServerTokenizer{
		serverURL:   serverURL,
		model:       model,
		maxTokens:   maxTokens,
		debug:       false,
		client: &http.Client{
			Timeout: 60 * time.Second, // Увеличен таймаут для больших контекстов
		},
	}
}

// SetDebug включает/выключает отладочное логирование
func (t *LlamaServerTokenizer) SetDebug(debug bool) {
	t.debug = debug
}

func (t *LlamaServerTokenizer) logf(format string, args ...interface{}) {
	if t.debug {
		log.Printf("[tokenizer] "+format, args...)
	}
}

// CountTokens отправляет запрос к llama-server для подсчёта токенов
func (t *LlamaServerTokenizer) CountTokens(text string) (int, error) {
	if text == "" {
		return 0, nil
	}

	messages := []Message{{Role: "user", Content: text}}
	return t.CountMessagesTokens(messages)
}

// CountMessagesTokens отправляет массив сообщений к llama-server для подсчёта токенов
func (t *LlamaServerTokenizer) CountMessagesTokens(messages []Message) (int, error) {
	if len(messages) == 0 {
		return 0, nil
	}

	// Используем endpoint /tokenize для быстрой токенизации
	var sb strings.Builder
	for _, msg := range messages {
		sb.WriteString(msg.Role)
		sb.WriteString(": ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n")
	}
	text := sb.String()

	reqBody := map[string]interface{}{
		"content": text,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("marshal request: %w", err)
	}

	reqURL := fmt.Sprintf("%s/tokenize", t.serverURL)
	t.logf("Requesting tokenize from %s", reqURL)

	req, err := http.NewRequestWithContext(context.Background(), "POST", reqURL, bytes.NewReader(jsonData))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		t.logf("ERROR: tokenize request failed: %v", err)
		return 0, fmt.Errorf("tokenize request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.logf("ERROR: tokenize returned status %d, body: %s", resp.StatusCode, string(body))
		return 0, fmt.Errorf("tokenize returned status %d: %s", resp.StatusCode, string(body))
	}

	var apiResponse struct {
		Tokens []int `json:"tokens"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		t.logf("ERROR: failed to decode tokenize response: %v", err)
		return 0, fmt.Errorf("decode tokenize response: %w", err)
	}

	t.logf("Tokenize result: %d tokens", len(apiResponse.Tokens))
	return len(apiResponse.Tokens), nil
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
