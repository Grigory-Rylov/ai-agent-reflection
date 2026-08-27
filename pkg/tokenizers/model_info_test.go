package tokenizers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)


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


func TestServerInfoClient_GetModelContextLength(t *testing.T) {
	t.Run("gets --ctx-size from /v1/models status args", func(t *testing.T) {
		response := `{
			"object": "list",
			"data": [{
				"id": "test-model",
				"object": "model",
				"created": 1234567890,
				"owned_by": "test",
				"status": {
					"value": "loaded",
					"args": ["--ctx-size", "131072", "--n-gpu-layers", "999"]
				}
			}]
		}`

		server := createMockServer(response)
		defer server.Close()

		client := NewServerInfoClient(server.URL)
		ctxLen := client.GetModelContextLength("test-model")

		if ctxLen != 131072 {
			t.Errorf("Expected context length 131072, got %d", ctxLen)
		}
	})

	t.Run("matches current model by id among multiple models", func(t *testing.T) {
		response := `{
			"object": "list",
			"data": [
				{
					"id": "other-model",
					"status": {"value": "loaded", "args": ["--ctx-size", "4096"]}
				},
				{
					"id": "test-model",
					"status": {"value": "loaded", "args": ["--ctx-size", "148000"]}
				}
			]
		}`

		server := createMockServer(response)
		defer server.Close()

		client := NewServerInfoClient(server.URL)
		ctxLen := client.GetModelContextLength("test-model")

		if ctxLen != 148000 {
			t.Errorf("Expected context length 148000 for test-model, got %d", ctxLen)
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
		ctxLen := client.GetModelContextLength("test-model")

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
		ctxLen := client.GetModelContextLength("test-model")

		if ctxLen != -1 {
			t.Errorf("Expected -1 on failure, got %d", ctxLen)
		}
	})

	t.Run("handles empty meta field - falls back to /props", func(t *testing.T) {
		
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
		ctxLen := client.GetModelContextLength("test-model")

		
		if ctxLen != 8192 {
			t.Errorf("Expected fallback to /props (8192), got %d", ctxLen)
		}
	})

	t.Run("handles missing data array - falls back to /props", func(t *testing.T) {
		
		response := `{
			"object": "list",
			"data": []
		}`

		server := createMockServer(response)
		defer server.Close()

		client := NewServerInfoClient(server.URL)
		ctxLen := client.GetModelContextLength("test-model")

		
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


func TestLlamaServerTokenizer_ContextDetection(t *testing.T) {
	t.Run("resolves to actual context when available", func(t *testing.T) {
		response := `{
			"object": "list",
			"data": [{
				"id": "llama-3.1",
				"status": {"value": "loaded", "args": ["--ctx-size", "131072"]}
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

		
		maxCtx := tokenizer.MaxContextLength()
		if maxCtx != 131072 {
			t.Errorf("Expected MaxContextLength 131072, got %d", maxCtx)
		}

		
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

		
		maxCtx := tokenizer.MaxContextLength()
		if maxCtx != 8192 {
			t.Errorf("Expected MaxContextLength 8192 (fallback), got %d", maxCtx)
		}

		
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
				"status": {"value": "loaded", "args": ["--ctx-size", "65536"]}
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

		
		tokenizer.GetActualContextLimit()
		firstCallCount := callCount

		
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
		
		response := `{
			"object": "list",
			"data": [{
				"status": {"value": "loaded", "args": ["--ctx-size", "81920"]}
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

		
		if actualCtx == configuredMax {
			t.Errorf("Expected actual context (%d) to differ from configured (%d)",
				actualCtx, configuredMax)
		}

		if actualCtx != 81920 {
			t.Errorf("Expected actual context 81920, got %d", actualCtx)
		}

		
		maxCtx := tokenizer.MaxContextLength()
		if maxCtx != 81920 {
			t.Errorf("Expected MaxContextLength 81920 (actual), got %d", maxCtx)
		}
	})
}


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
					"status": {"value": "loaded", "args": ["--ctx-size", "%d"]}
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


func TestCtxSizeFromArgs(t *testing.T) {
	t.Run("parses --ctx-size", func(t *testing.T) {
		st := &ModelStatus{Value: "loaded", Args: []string{"--ctx-size", "148000", "--host", "127.0.0.1"}}
		if got := ctxSizeFromArgs(st); got != 148000 {
			t.Errorf("expected 148000, got %d", got)
		}
	})

	t.Run("parses -c short flag", func(t *testing.T) {
		st := &ModelStatus{Value: "loaded", Args: []string{"-c", "8192"}}
		if got := ctxSizeFromArgs(st); got != 8192 {
			t.Errorf("expected 8192, got %d", got)
		}
	})

	t.Run("returns 0 when args missing", func(t *testing.T) {
		if got := ctxSizeFromArgs(nil); got != 0 {
			t.Errorf("expected 0 for nil status, got %d", got)
		}
		st := &ModelStatus{Value: "loaded", Args: []string{"--ctx-size"}}
		if got := ctxSizeFromArgs(st); got != 0 {
			t.Errorf("expected 0 when value missing, got %d", got)
		}
		st = &ModelStatus{Value: "unloaded", Args: nil}
		if got := ctxSizeFromArgs(st); got != 0 {
			t.Errorf("expected 0 when args empty, got %d", got)
		}
	})
}

func TestServerInfoClient_GetModelContextLength_MetaNCtxFallback(t *testing.T) {
	response := `{
		"object": "list",
		"data": [{
			"id": "test-model",
			"meta": {"n_ctx": 148224, "n_ctx_train": 196608}
		}]
	}`

	server := createMockServer(response)
	defer server.Close()

	client := NewServerInfoClient(server.URL)
	ctxLen := client.GetModelContextLength("test-model")

	if ctxLen != 148224 {
		t.Errorf("Expected meta.n_ctx 148224 (not n_ctx_train), got %d", ctxLen)
	}
}

func TestServerInfoClient_DebugMode(t *testing.T) {
	t.Run("debug logs are enabled", func(t *testing.T) {
		response := `{
			"object": "list",
			"data": [{
				"status": {"value": "loaded", "args": ["--ctx-size", "4096"]}
			}]
		}`

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(response))
		}))
		defer server.Close()

		client := NewServerInfoClient(server.URL)
		client.SetDebug(true) 

		ctxLen := client.GetModelContextLength("test-model")
		if ctxLen != 4096 {
			t.Errorf("Expected 4096, got %d", ctxLen)
		}
	})
}


// Fixture captured from a real llama-server router (multi-instance mode, b10669).
// Quirks that previously broke parsing:
//  1. meta.vocabulary_type arrives as a boolean ("vocab_type": true), which made
//     json.Decode fail -> fallthrough to /props -> router reports n_ctx=0.
//  2. /props behind a router has default_generation_settings.n_ctx == 0, so the
//     authoritative value is meta.n_ctx from /v1/models.
func TestRouterFormat_RealFixture(t *testing.T) {
	modelsJSON := `{
		"data": [
			{
				"id": "qwen3.8-next-flash-iq4_xs",
				"aliases": [],
				"tags": [],
				"object": "model",
				"owned_by": "llamacpp",
				"created": 1787857076,
				"status": {
					"value": "loaded",
					"args": ["--ctx-size", "262144"]
				},
				"meta": {
					"vocab_type": true,
					"n_vocab": 248320,
					"n_ctx": 262144,
					"n_ctx_train": 262144,
					"n_embd": 2560,
					"n_params": 176943899520,
					"size": 93671559680,
					"ftype": "IQ4_XS - 4.25 bpw"
				}
			}
		]
	}`

	routerProps := `{"role":"router","default_generation_settings":{"params":null,"n_ctx":0},"build_info":"b10669-6c5afc86a"}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, modelsJSON)
		case "/props":
			fmt.Fprint(w, routerProps)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewServerInfoClient(server.URL)

	info, err := client.GetModelInfo()
	if err != nil {
		t.Fatalf("GetModelInfo failed on real router fixture: %v", err)
	}
	if info.Meta == nil {
		t.Fatal("Expected meta to parse despite vocab_type=true")
	}
	if info.Meta.NCtx != 262144 {
		t.Errorf("Expected meta.n_ctx 262144, got %d", info.Meta.NCtx)
	}

	ctxLen := client.GetModelContextLength("qwen3.8-next-flash-iq4_xs")
	if ctxLen != 262144 {
		t.Errorf("Expected context length 262144, got %d", ctxLen)
	}
}
