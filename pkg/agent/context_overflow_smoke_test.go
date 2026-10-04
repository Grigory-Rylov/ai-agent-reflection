package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func strictServer(t *testing.T, window int, rejections *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		decoded := decodeRequestBody(t, body)
		maxTokens, _ := decoded["max_tokens"].(float64)
		promptTokens := len(body) / 4

		if promptTokens+int(maxTokens) > window {
			rejections.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":{"type":"invalid_request_error","message":"prompt (%d tokens) + max tokens (%d) exceeds the context (%d); requests are never truncated"}}`,
				promptTokens, int(maxTokens), window)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func smokeAgent(t *testing.T, window int, url string) *agentImpl {
	t.Helper()
	config := DefaultConfig()
	config.LlamaServerURL = url
	config.Model = "test-model"
	config.MaxTokens = window
	config.EnableTools = false
	config.EnableCompression = true
	config.RetryDelay = 5 * time.Millisecond

	agent := NewAgent(config)
	agent.compactor = compress.NewCompactor(&stubSummarizer{summary: "[SUMMARY] compressed"})
	return agent
}

func TestSmokeNoRequestEverExceedsWindow(t *testing.T) {
	tests := []struct {
		name   string
		window int
		mass   int
	}{
		{"production incident", 262_144, 12_000},
		{"output reserve dead zone", 150_000, 6_100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rejections atomic.Int32
			server := strictServer(t, tt.window, &rejections)
			defer server.Close()

			agent := smokeAgent(t, tt.window, server.URL)
			s := sess.NewSession(sess.DefaultConfig())
			s.UpdateSystemPrompt("test system prompt")
			s.AddUserMessage("start")
			for i := range 40 {
				s.AddUserMessage(fmt.Sprintf("%d-%s", i, strings.Repeat("u", tt.mass)))
				s.AddAssistantMessage(fmt.Sprintf("%d-%s", i, strings.Repeat("a", tt.mass)))
			}

			result, err := agent.runTurn(context.Background(), s)
			if err != nil {
				t.Fatalf("runTurn: %v", err)
			}
			if result != "ok" {
				t.Errorf("result = %q, want %q", result, "ok")
			}
			if got := rejections.Load(); got != 0 {
				t.Errorf("server rejected %d requests, want 0", got)
			}
		})
	}
}
