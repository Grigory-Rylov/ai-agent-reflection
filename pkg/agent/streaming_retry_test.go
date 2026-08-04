package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
