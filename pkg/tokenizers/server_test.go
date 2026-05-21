package tokenizers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ============================================================
// Тесты LlamaServerTokenizer
// ============================================================

func TestNewLlamaServerTokenizer(t *testing.T) {
	tokenizer := NewLlamaServerTokenizer("http://localhost:8081", "test-model", 8192)
	if tokenizer == nil {
		t.Fatal("tokenizer should not be nil")
	}
	if tokenizer.serverURL != "http://localhost:8081" {
		t.Errorf("expected URL 'http://localhost:8081', got '%s'", tokenizer.serverURL)
	}
}

func TestLlamaServerTokenizerName(t *testing.T) {
	tokenizer := NewLlamaServerTokenizer("http://localhost:8081", "qwen3.6", 8192)
	name := tokenizer.Name()
	if name != "llama-server-qwen3.6" {
		t.Errorf("expected 'llama-server-qwen3.6', got '%s'", name)
	}
}

func TestLlamaServerTokenizerCountTokens(t *testing.T) {
	// Создаём тестовый сервер
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"tokens": [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42]
		}`))
	}))
	defer server.Close()

	tokenizer := NewLlamaServerTokenizer(server.URL, "test", 8192)
	count, err := tokenizer.CountTokens("hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 42 {
		t.Errorf("expected 42 tokens, got %d", count)
	}
}

func TestLlamaServerTokenizerCountTokensEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tokenizer := NewLlamaServerTokenizer(server.URL, "test", 8192)
	count, err := tokenizer.CountTokens("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tokens for empty text, got %d", count)
	}
}

func TestLlamaServerTokenizerEncodeNotSupported(t *testing.T) {
	tokenizer := NewLlamaServerTokenizer("http://localhost:8081", "test", 8192)
	_, err := tokenizer.Encode("test")
	if err == nil {
		t.Error("expected error for encode")
	}
}

func TestLlamaServerTokenizerDecodeNotSupported(t *testing.T) {
	tokenizer := NewLlamaServerTokenizer("http://localhost:8081", "test", 8192)
	_, err := tokenizer.Decode([]int{1, 2, 3})
	if err == nil {
		t.Error("expected error for decode")
	}
}

func TestLlamaServerTokenizerMaxContextLength(t *testing.T) {
	tokenizer := NewLlamaServerTokenizer("http://localhost:8081", "test", 4096)
	length := tokenizer.MaxContextLength()
	if length != 4096 {
		t.Errorf("expected 4096, got %d", length)
	}
}

func TestLlamaServerTokenizerCountMessagesTokens(t *testing.T) {
	// Создаём тестовый сервер
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Генерируем массив из 100 токенов
		tokens := make([]int, 100)
		for i := 0; i < 100; i++ {
			tokens[i] = i + 1
		}
		response := map[string]interface{}{
			"tokens": tokens,
		}
		jsonData, _ := json.Marshal(response)
		w.Write(jsonData)
	}))
	defer server.Close()

	tokenizer := NewLlamaServerTokenizer(server.URL, "test", 8192)
	messages := []Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}
	count, err := tokenizer.CountMessagesTokens(messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 100 {
		t.Errorf("expected 100 tokens, got %d", count)
	}
}

func TestLlamaServerTokenizerCountMessagesTokensEmpty(t *testing.T) {
	tokenizer := NewLlamaServerTokenizer("http://localhost:8081", "test", 8192)
	count, err := tokenizer.CountMessagesTokens([]Message{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tokens for empty messages, got %d", count)
	}
}
