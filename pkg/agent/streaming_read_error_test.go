package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func TestCollectStream_ConvertsIsErrorEventToGoError(t *testing.T) {
	a := NewAgent(Config{SessionConfig: session.DefaultConfig()})

	ch := make(chan StreamChunkEvent, 4)
	ch <- StreamChunkEvent{Content: "partial"}
	ch <- StreamChunkEvent{
		Content:   "LLM stream read failed: connection reset",
		IsDone:    true,
		IsError:   true,
		ErrorCode: "stream_read_error",
	}
	close(ch)

	responseText, reasoningText, _, _, _, _, err := a.collectStreamResponseWithToolCalls(ch)
	if err == nil {
		t.Fatal("expected error when stream fails mid-way")
	}
	if !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("error should carry the underlying cause, got: %v", err)
	}
	if responseText != "" || reasoningText != "" {
		t.Errorf("failed stream must not leak into collected text, got content=%q reasoning=%q", responseText, reasoningText)
	}
}

func TestStreamRead_HandlesOversizedSSELine(t *testing.T) {
	bigReasoning := strings.Repeat("размышляем над задачей ", 20000)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, `data: {"choices":[{"delta":{"reasoning_content":"%s"}}]}`+"\n\n", bigReasoning)
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"Готово."}}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	a := NewAgent(Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		SessionConfig:  session.DefaultConfig(),
	})

	responseText, reasoningText, finishReason, _, _, _, err := a.streamAndCollect(context.Background(), StreamingConfig{
		Model:       "test-model",
		Temperature: 0.7,
		Stream:      true,
	}, []Message{{Role: "user", Content: "привет"}})
	if err != nil {
		t.Fatalf("stream with a %d-byte SSE line must complete, got error: %v", len(bigReasoning), err)
	}
	if reasoningText != bigReasoning {
		t.Errorf("reasoning was corrupted or truncated: got %d chars, want %d", len(reasoningText), len(bigReasoning))
	}
	if responseText != "Готово." {
		t.Errorf("unexpected content text: %q", responseText)
	}
	if finishReason != "stop" {
		t.Errorf("expected finish_reason=stop, got %q", finishReason)
	}
}
