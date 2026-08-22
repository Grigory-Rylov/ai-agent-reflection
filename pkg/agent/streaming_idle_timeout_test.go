package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func TestCollectStream_IdleTimeoutErrorIsRetryable(t *testing.T) {
	a := NewAgent(Config{SessionConfig: session.DefaultConfig()})

	ch := make(chan StreamChunkEvent, 1)
	ch <- StreamChunkEvent{
		Content:   "LLM stream stalled: no data for 5m0s",
		IsDone:    true,
		IsError:   true,
		ErrorCode: ErrCodeStreamIdleTimeout,
	}
	close(ch)

	_, _, _, _, _, _, err := a.collectStreamResponseWithToolCalls(ch)
	if err == nil {
		t.Fatal("expected error on idle-timeout event")
	}
	if !isRetryableError(err) {
		t.Fatalf("idle timeout must be retryable, got non-retryable: %v", err)
	}
}

func TestStreamIdleTimeout_FiresOnSilenceAndRetries(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"начало\"}}]}\n\n")
		flusher.Flush()
		if attempts.Add(1) == 1 {
			time.Sleep(600 * time.Millisecond)
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" конец\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "[DONE]\n")
		flusher.Flush()
	}))
	defer server.Close()

	a := NewAgent(Config{
		LlamaServerURL:    server.URL,
		Model:             "test-model",
		SessionConfig:     session.DefaultConfig(),
		RetryDelay:        10 * time.Millisecond,
		StreamIdleTimeout: 200 * time.Millisecond,
	})

	responseText, _, finishReason, _, _, _, err := a.streamAndCollect(context.Background(), StreamingConfig{
		Model:       "test-model",
		Temperature: 0.7,
		Stream:      true,
	}, []Message{{Role: "user", Content: "привет"}})
	if err != nil {
		t.Fatalf("retry after idle-timeout must succeed, got error: %v", err)
	}
	if responseText != "начало конец" {
		t.Errorf("expected full text from second attempt, got %q", responseText)
	}
	if finishReason != "stop" {
		t.Errorf("expected finish_reason=stop, got %q", finishReason)
	}
	if attempts.Load() < 2 {
		t.Errorf("expected at least 2 attempts after stall, got %d", attempts.Load())
	}
}

func TestStreamIdleTimeout_NegativeDisablesWatchdog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"медленно\"}}]}\n\n")
		flusher.Flush()
		time.Sleep(400 * time.Millisecond)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "[DONE]\n")
		flusher.Flush()
	}))
	defer server.Close()

	a := NewAgent(Config{
		LlamaServerURL:    server.URL,
		Model:             "test-model",
		SessionConfig:     session.DefaultConfig(),
		StreamIdleTimeout: -time.Second,
	})

	responseText, _, finishReason, _, _, _, err := a.streamAndCollect(context.Background(), StreamingConfig{
		Model:       "test-model",
		Temperature: 0.7,
		Stream:      true,
	}, []Message{{Role: "user", Content: "привет"}})
	if err != nil {
		t.Fatalf("negative timeout must disable watchdog, got error: %v", err)
	}
	if responseText != "медленно" || finishReason != "stop" {
		t.Errorf("unexpected result: text=%q finish=%q", responseText, finishReason)
	}
}

func TestStreamIdleTimeout_ZeroFallsBackToDefault(t *testing.T) {
	a := NewAgent(Config{SessionConfig: session.DefaultConfig()})
	if got := a.streamIdleTimeout(); got != DefaultStreamIdleTimeout {
		t.Errorf("zero config must fall back to default %v, got %v", DefaultStreamIdleTimeout, got)
	}

	a = NewAgent(Config{SessionConfig: session.DefaultConfig(), StreamIdleTimeout: -time.Minute})
	if got := a.streamIdleTimeout(); got != 0 {
		t.Errorf("negative config must disable watchdog, got %v", got)
	}
}
