package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// validSSEStream — корректный SSE-ответ сервера.
func validSSEStream() string {
	return "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"[DONE]\n"
}

// TestStreamAndCollectRetriesServerErrors — HTTP 5xx (сервер перезагружается)
// должен ретраиться бесконечно до успеха.
func TestStreamAndCollectRetriesServerErrors(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n <= 2 {
			http.Error(w, "model still loading", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, validSSEStream())
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.RetryDelay = 5 * time.Millisecond
	a := NewAgent(config)

	ctx := context.Background()
	resp, _, _, toolCalls, _, _, err := a.streamAndCollect(ctx, StreamingConfig{Model: "m", MaxTokens: 100}, nil)
	if err != nil {
		t.Fatalf("expected retry to succeed, got error: %v", err)
	}
	if resp != "Hello" {
		t.Errorf("expected content %q, got %q", "Hello", resp)
	}
	if len(toolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(toolCalls))
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("expected 3 requests (2 failures + success), got %d", got)
	}
}

// TestStreamAndCollectRetriesTruncatedStream — пустой/оборванный стрим
// (HTTP 200 без данных) должен ретраиться.
func TestStreamAndCollectRetriesTruncatedStream(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			// Оборванный стрим: заголовок выставлен, данных нет.
			return
		}
		fmt.Fprint(w, validSSEStream())
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.RetryDelay = 5 * time.Millisecond
	a := NewAgent(config)

	ctx := context.Background()
	resp, _, _, _, _, _, err := a.streamAndCollect(ctx, StreamingConfig{Model: "m", MaxTokens: 100}, nil)
	if err != nil {
		t.Fatalf("expected retry to succeed, got error: %v", err)
	}
	if resp != "Hello" {
		t.Errorf("expected content %q, got %q", "Hello", resp)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("expected 2 requests (1 truncated + success), got %d", got)
	}
}

// TestStreamAndCollectNoRetryOnClientError — HTTP 4xx не должен ретраиться.
func TestStreamAndCollectNoRetryOnClientError(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.RetryDelay = 5 * time.Millisecond
	a := NewAgent(config)

	ctx := context.Background()
	_, _, _, _, _, _, err := a.streamAndCollect(ctx, StreamingConfig{Model: "m", MaxTokens: 100}, nil)
	if err == nil {
		t.Fatal("expected error for HTTP 400, got nil")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected 1 request (no retry on 4xx), got %d", got)
	}
}

// TestStreamAndCollectNoRetryOnSSEContextError — SSE-ошибка
// (context_length_exceeded) не должна ретраиться.
func TestStreamAndCollectNoRetryOnSSEContextError(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"ctx too long\",\"code\":\"context_length_exceeded\"}}\n\n")
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.RetryDelay = 5 * time.Millisecond
	a := NewAgent(config)

	ctx := context.Background()
	_, _, _, _, _, _, err := a.streamAndCollect(ctx, StreamingConfig{Model: "m", MaxTokens: 100}, nil)
	if err == nil {
		t.Fatal("expected error for SSE context_length_exceeded, got nil")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected 1 request (no retry on SSE error), got %d", got)
	}
}

// TestStreamAndCollectReadableServerError — когда LLM-сервер недоступен,
// ошибка должна содержать понятный текст, а не "context deadline exceeded".
func TestStreamAndCollectReadableServerError(t *testing.T) {
	tests := []struct {
		name             string
		getServerURL     func() string
		ctxTimeout       time.Duration
		expectShutdown   bool
	}{
		{
			name: "closed_server_returns_readable_error",
			getServerURL: func() string {
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
				server.Start()
				server.Close() // immediately close — no connection possible
				return server.Listener.Addr().String()
			},
			ctxTimeout:     100 * time.Millisecond,
			expectShutdown: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "http://" + tt.getServerURL()

			config := DefaultConfig()
			config.LlamaServerURL = url
			config.RetryDelay = 50 * time.Millisecond // enough for connection refused to happen fast
			a := NewAgent(config)

			ctx, cancel := context.WithTimeout(context.Background(), tt.ctxTimeout)
			defer cancel()

			_, _, _, _, _, _, err := a.streamAndCollect(ctx, StreamingConfig{Model: "m", MaxTokens: 100}, nil)
			if err == nil {
				t.Fatal("expected error when server is unavailable, got nil")
			}

			errMsg := err.Error()
			t.Logf("error message: %s", errMsg)

			// Error should contain readable text about LLM server shutdown, not raw context error
			if tt.expectShutdown {
				// When retries are exhausted, lastErr is wrapped. Should still be readable.
				hasReadable := strings.Contains(errMsg, "LLM request exhausted") ||
					strings.Contains(errMsg, "shutdown") || strings.Contains(errMsg, "unreachable")
				if !hasReadable {
					t.Errorf("expected readable error message (no raw 'context deadline exceeded'), got: %s", errMsg)
				}
			}
		})
	}
}

// TestStreamAndCollectPreservesLastErrorOnRetryExhaustion — при истощении ретраев
// через контекст, сохраняется исходная ошибка сервера в lastErr.
func TestStreamAndCollectPreservesLastErrorOnRetryExhaustion(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Start()
	server.Close() // immediately close — connection refused every attempt

	config := DefaultConfig()
	config.LlamaServerURL = "http://" + server.Listener.Addr().String()
	config.RetryDelay = 5 * time.Millisecond
	a := NewAgent(config)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, _, _, _, _, _, err := a.streamAndCollect(ctx, StreamingConfig{Model: "m", MaxTokens: 100}, nil)
	if err == nil {
		t.Fatal("expected error when retries exhausted, got nil")
	}

	errMsg := err.Error()
	t.Logf("error message: %s", errMsg)

	// lastErr should be preserved (wrapped in "LLM request exhausted"), not replaced by ctx.Err()
	if !strings.Contains(errMsg, "LLM request exhausted") {
		t.Errorf("expected 'LLM request exhausted' wrapper preserving last error, got: %s", errMsg)
	}
}
