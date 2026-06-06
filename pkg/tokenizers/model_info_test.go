package tokenizers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ============================================================
// Mock server для тестирования
// ============================================================

func createMockServer(response string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))
			return
		}
		if r.URL.Path == "/props" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{
				"default_generation_settings": {
					"n_ctx": 8192
				}
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

// ============================================================
// Tests for ServerInfoClient
// ============================================================

func TestServerInfoClient_GetModelContextLength(t *testing.T) {
	t.Run("gets n_ctx_train from /v1/models", func(t *testing.T) {
		response := `{
			"object": "list",
			"data": [{
				"id": "test-model",
				"object": "model",
				"created": 1234567890,
				"owned_by": "test",
				"meta": {
					"vocab_type": 2,
					"n_vocab": 128256,
					"n_ctx_train": 131072
				}
			}]
		}`

		server := createMockServer(response)
		defer server.Close()

		client := NewServerInfoClient(server.URL)
		ctxLen := client.GetModelContextLength()

		if ctxLen != 131072 {
			t.Errorf("Expected context length 131072, got %d", ctxLen)
		}
	})

	t.Run("falls back to /props when /v1/models fails", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/models" {
				callCount++
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if r.URL.Path == "/props" {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{
					"default_generation_settings": {
						"n_ctx": 4096
					}
				}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		client := NewServerInfoClient(server.URL)
		ctxLen := client.GetModelContextLength()

		if ctxLen != 4096 {
			t.Errorf("Expected context length 4096 from /props, got %d", ctxLen)
		}
		if callCount != 1 {
			t.Errorf("Expected /v1/models to be called once, got %d calls", callCount)
		}
	})

	t.Run("returns -1 when both endpoints fail", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		client := NewServerInfoClient(server.URL)
		ctxLen := client.GetModelContextLength()

		if ctxLen != -1 {
			t.Errorf("Expected -1 on failure, got %d", ctxLen)
		}
	})

	t.Run("handles empty meta field - falls back to /props", func(t *testing.T) {
		// Когда meta=null, функция делает фоллбэк на /props
		response := `{
			"object": "list",
			"data": [{
				"id": "test-model",
				"object": "model",
				"created": 1234567890,
				"owned_by": "test",
				"meta": null
			}]
		}`

		server := createMockServer(response)
		defer server.Close()

		client := NewServerInfoClient(server.URL)
		ctxLen := client.GetModelContextLength()

		// Ожидаем фоллбэк на /props который возвращает 8192
		if ctxLen != 8192 {
			t.Errorf("Expected fallback to /props (8192), got %d", ctxLen)
		}
	})

	t.Run("handles missing data array - falls back to /props", func(t *testing.T) {
		// Когда data пустой, функция делает фоллбэк на /props
		response := `{
			"object": "list",
			"data": []
		}`

		server := createMockServer(response)
		defer server.Close()

		client := NewServerInfoClient(server.URL)
		ctxLen := client.GetModelContextLength()

		// Ожидаем фоллбэк на /props который возвращает 8192
		if ctxLen != 8192 {
			t.Errorf("Expected fallback to /props (8192), got %d", ctxLen)
		}
	})
}

func TestServerInfoClient_GetModelInfo(t *testing.T) {
	t.Run("returns full model info", func(t *testing.T) {
		response := `{
			"object": "list",
			"data": [{
				"id": "test-model.gguf",
				"object": "model",
				"created": 1735142223,
				"owned_by": "llamacpp",
				"meta": {
					"vocab_type": 2,
					"n_vocab": 128256,
					"n_ctx_train": 131072,
					"n_embd": 4096,
					"n_params": 8030261312,
					"size": 4912898304
				}
			}]
		}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(response))
		}))
		defer server.Close()

		client := NewServerInfoClient(server.URL)
		info, err := client.GetModelInfo()

		if err != nil {
			t.Fatalf("GetModelInfo failed: %v", err)
		}

		if info.ID != "test-model.gguf" {
			t.Errorf("Expected ID 'test-model.gguf', got '%s'", info.ID)
		}

		if info.Meta == nil {
			t.Fatal("Expected meta to be non-nil")
		}

		if info.Meta.NCtxTrain != 131072 {
			t.Errorf("Expected n_ctx_train 131072, got %d", info.Meta.NCtxTrain)
		}

		if info.Meta.NVocab != 128256 {
			t.Errorf("Expected n_vocab 128256, got %d", info.Meta.NVocab)
		}

		if info.Meta.NEmbd != 4096 {
			t.Errorf("Expected n_embd 4096, got %d", info.Meta.NEmbd)
		}
	})

	t.Run("handles malformed JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("invalid json"))
		}))
		defer server.Close()

		client := NewServerInfoClient(server.URL)
		_, err := client.GetModelInfo()

		if err == nil {
			t.Error("Expected error for malformed JSON, got nil")
		}
	})
}

// ============================================================
// Tests for LlamaServerTokenizer with context detection
// ============================================================

func TestLlamaServerTokenizer_ContextDetection(t *testing.T) {
	t.Run("resolves to actual context when available", func(t *testing.T) {
		response := `{
			"object": "list",
			"data": [{
				"id": "llama-3.1",
				"meta": {
					"n_ctx_train": 131072
				}
			}]
		}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/models" {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(response))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		tokenizer := NewLlamaServerTokenizer(server.URL, "test-model", 200000)
		tokenizer.InitializeContextLimit()

		actualCtx := tokenizer.GetActualContextLimit()
		if actualCtx != 131072 {
			t.Errorf("Expected actual context 131072, got %d", actualCtx)
		}

		// MaxContextLength должен возвращать реальное значение
		maxCtx := tokenizer.MaxContextLength()
		if maxCtx != 131072 {
			t.Errorf("Expected MaxContextLength 131072, got %d", maxCtx)
		}

		// ResolveMaxTokens тоже должен возвращать реальное значение
		resolved := tokenizer.ResolveMaxTokens()
		if resolved != 131072 {
			t.Errorf("Expected ResolveMaxTokens 131072, got %d", resolved)
		}
	})

	t.Run("falls back to configured max when server unavailable", func(t *testing.T) {
		tokenizer := NewLlamaServerTokenizer("http://invalid:9999", "test-model", 8192)
		err := tokenizer.InitializeContextLimit()

		if err == nil {
			t.Error("Expected error when server unavailable, got nil")
		}

		// MaxContextLength должен вернуть конфигурационное значение
		maxCtx := tokenizer.MaxContextLength()
		if maxCtx != 8192 {
			t.Errorf("Expected MaxContextLength 8192 (fallback), got %d", maxCtx)
		}

		// ResolveMaxTokens тоже должен вернуть конфигурационное значение
		resolved := tokenizer.ResolveMaxTokens()
		if resolved != 8192 {
			t.Errorf("Expected ResolveMaxTokens 8192 (fallback), got %d", resolved)
		}
	})

	t.Run("caches actual context after first fetch", func(t *testing.T) {
		callCount := 0
		response := `{
			"object": "list",
			"data": [{
				"meta": {
					"n_ctx_train": 65536
				}
			}]
		}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/models" {
				callCount++
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(response))
			}
		}))
		defer server.Close()

		tokenizer := NewLlamaServerTokenizer(server.URL, "test", 4096)
		tokenizer.InitializeContextLimit()

		// Первый вызов
		tokenizer.GetActualContextLimit()
		firstCallCount := callCount

		// Второй вызов (должен использовать кеш)
		tokenizer.GetActualContextLimit()
		secondCallCount := callCount

		if firstCallCount != 1 {
			t.Errorf("Expected 1 call after first GetActualContextLimit, got %d", firstCallCount)
		}

		if secondCallCount != 1 {
			t.Errorf("Expected no additional calls (cached), got total %d calls", secondCallCount)
		}
	})

	t.Run("distinguishes between configured and actual context", func(t *testing.T) {
		// Симулируем ситуацию когда в конфиге 200k, а у модели реально 80k
		response := `{
			"object": "list",
			"data": [{
				"meta": {
					"n_ctx_train": 81920
				}
			}]
		}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(response))
		}))
		defer server.Close()

		configuredMax := 200000
		tokenizer := NewLlamaServerTokenizer(server.URL, "test", configuredMax)
		tokenizer.InitializeContextLimit()

		actualCtx := tokenizer.GetActualContextLimit()

		// Реальный контекст должен отличаться от конфигурационного
		if actualCtx == configuredMax {
			t.Errorf("Expected actual context (%d) to differ from configured (%d)",
				actualCtx, configuredMax)
		}

		if actualCtx != 81920 {
			t.Errorf("Expected actual context 81920, got %d", actualCtx)
		}

		// MaxContextLength должен вернуть реальное значение, а не конфигурационное
		maxCtx := tokenizer.MaxContextLength()
		if maxCtx != 81920 {
			t.Errorf("Expected MaxContextLength 81920 (actual), got %d", maxCtx)
		}
	})
}

// ============================================================
// Integration scenarios
// ============================================================

func TestScenarios_ContextLimitMismatch(t *testing.T) {
	scenarios := []struct {
		name           string
		configuredMax  int
		actualMax      int
		expectOverride bool
	}{
		{
			name:           "config underestimates model",
			configuredMax:  4000,
			actualMax:      131072,
			expectOverride: true,
		},
		{
			name:           "config overestimates model",
			configuredMax:  200000,
			actualMax:      81920,
			expectOverride: true,
		},
		{
			name:           "config matches model",
			configuredMax:  8192,
			actualMax:      8192,
			expectOverride: false,
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			response := fmt.Sprintf(`{
				"object": "list",
				"data": [{
					"meta": {
						"n_ctx_train": %d
					}
				}]
			}`, sc.actualMax)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(response))
			}))
			defer server.Close()

			tokenizer := NewLlamaServerTokenizer(server.URL, "test", sc.configuredMax)
			tokenizer.InitializeContextLimit()

			usedMax := tokenizer.MaxContextLength()

			if sc.expectOverride && usedMax == sc.configuredMax {
				t.Errorf("Expected to use actual context (%d), but using configured (%d)",
					sc.actualMax, sc.configuredMax)
			}

			if !sc.expectOverride && usedMax != sc.configuredMax {
				t.Errorf("Expected to use configured context (%d), but using %d",
					sc.configuredMax, usedMax)
			}

			t.Logf("Configured: %d, Actual: %d, Used: %d",
				sc.configuredMax, sc.actualMax, usedMax)
		})
	}
}

// ============================================================
// Debug mode tests
// ============================================================

func TestServerInfoClient_DebugMode(t *testing.T) {
	t.Run("debug logs are enabled", func(t *testing.T) {
		response := `{
			"object": "list",
			"data": [{
				"meta": {
					"n_ctx_train": 4096
				}
			}]
		}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(response))
		}))
		defer server.Close()

		client := NewServerInfoClient(server.URL)
		client.SetDebug(true) // Просто проверяем что не паникует

		ctxLen := client.GetModelContextLength()
		if ctxLen != 4096 {
			t.Errorf("Expected 4096, got %d", ctxLen)
		}
	})
}
