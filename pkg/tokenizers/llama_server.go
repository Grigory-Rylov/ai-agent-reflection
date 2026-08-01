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
	serverURL      string
	model          string
	maxTokens      int
	client         *http.Client
	debug          bool
	infoClient     *ServerInfoClient
	actualCtxLimit int // Реальный контекст от сервера
}

// NewLlamaServerTokenizer создаёт новый токенайзер через llama-server
func NewLlamaServerTokenizer(serverURL, model string, maxTokens int) *LlamaServerTokenizer {
	infoClient := NewServerInfoClient(serverURL)
	return &LlamaServerTokenizer{
		serverURL:      serverURL,
		model:          model,
		maxTokens:      maxTokens,
		debug:          false,
		infoClient:     infoClient,
		actualCtxLimit: -1, // Ещё не запрашивали
		client: &http.Client{
			Timeout: 60 * time.Second, // Увеличен таймаут для больших контекстов
		},
	}
}

// SetDebug включает/выключает отладочное логирование
func (t *LlamaServerTokenizer) SetDebug(debug bool) {
	t.debug = debug
	t.infoClient.SetDebug(debug)
}

// GetActualContextLimit возвращает реальный лимит контекста от llama-server
// Если не удалось получить - возвращает -1
func (t *LlamaServerTokenizer) GetActualContextLimit() int {
	if t.actualCtxLimit > 0 {
		return t.actualCtxLimit
	}
	t.actualCtxLimit = t.infoClient.GetModelContextLength(t.model)
	return t.actualCtxLimit
}

// InitializeContextLimit запрашивает реальный контекст у llama-server
func (t *LlamaServerTokenizer) InitializeContextLimit() error {
	ctxLen := t.infoClient.GetModelContextLength(t.model)
	if ctxLen > 0 {
		t.actualCtxLimit = ctxLen
		if t.debug {
			log.Printf("[tokenizer] Initialized with actual context limit: %d", ctxLen)
		}
		return nil
	}
	return fmt.Errorf("failed to get actual context limit from server")
}

// ResolveMaxTokens возвращает фактический лимит токенов:
// 1. Если удалось получить реальный контекст от сервера - использует его
// 2. Иначе использует переданный maxTokens из конфига
func (t *LlamaServerTokenizer) ResolveMaxTokens() int {
	if actual := t.GetActualContextLimit(); actual > 0 {
		return actual
	}
	return t.maxTokens
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
		"model":   t.model,
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

// MaxContextLength возвращает максимальную длину контекста.
// Сначала пытается использовать реальный контекст от сервера, иначе - конфигурационный.
func (t *LlamaServerTokenizer) MaxContextLength() int {
	if actual := t.GetActualContextLimit(); actual > 0 {
		return actual
	}
	return t.maxTokens
}

// Name возвращает имя токенайзера
func (t *LlamaServerTokenizer) Name() string {
	return "llama-server-" + t.model
}
