package tokenizers

import (
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
			"usage": {
				"prompt_tokens": 42
			}
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
