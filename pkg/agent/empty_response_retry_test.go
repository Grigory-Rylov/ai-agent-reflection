package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type requestCapture struct {
	mu     sync.Mutex
	bodies []string
	count  atomic.Int32
}

func (c *requestCapture) record(body string) int32 {
	n := c.count.Add(1)
	c.mu.Lock()
	c.bodies = append(c.bodies, body)
	c.mu.Unlock()
	return n
}

func (c *requestCapture) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.bodies))
	copy(out, c.bodies)
	return out
}

func newEmptyRetryAgent(t *testing.T, respond func(n int32) string) (*agentImpl, *sess.Session, *requestCapture) {
	t.Helper()
	capture := &requestCapture{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := capture.record(string(body))

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: "+respond(n)+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 4096
	config.RetryDelay = 5 * time.Millisecond

	a := NewAgent(config)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("You are a helpful assistant")
	s.AddUserMessage("read the file")
	s.AddAssistantMessageWithToolCalls("", []sess.MsgToolCall{
		{ID: "call_1", Type: "function", Function: sess.MsgToolCallFunc{Name: "read", Arguments: `{"path":"build.sh"}`}},
	})
	s.AddToolMessage("call_1", "read", "#!/bin/bash\necho hello")

	return a, s, capture
}

func countSessionMessagesWithContent(history []sess.Message, content string) int {
	count := 0
	for _, m := range history {
		if m.Content == content {
			count++
		}
	}
	return count
}

func lastAssistantSessionMessage(history []sess.Message) (sess.Message, bool) {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == sess.AssistantRole {
			return history[i], true
		}
	}
	return sess.Message{}, false
}

func TestRunTurn_EmptyResponseRetriesWithReminder(t *testing.T) {
	finalText := "File read complete. The build.sh script rebuilds the agent binary."
	a, s, capture := newEmptyRetryAgent(t, func(n int32) string {
		if n <= 2 {
			return `{"choices":[{"delta":{"content":""},"finish_reason":"stop"}]}`
		}
		return fmt.Sprintf(`{"choices":[{"delta":{"content":%q},"finish_reason":"stop"}]}`, finalText)
	})

	result, err := a.runTurn(context.Background(), s)
	if err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	if result != finalText {
		t.Errorf("expected final text %q, got %q", finalText, result)
	}

	if got := capture.count.Load(); got != 3 {
		t.Fatalf("expected 3 LLM calls (empty + 2 retries), got %d", got)
	}

	bodies := capture.snapshot()
	if len(bodies) != 3 {
		t.Fatalf("expected 3 captured request bodies, got %d", len(bodies))
	}
	if count := strings.Count(bodies[0], emptyResponseReminder); count != 0 {
		t.Errorf("first request must not contain emptyResponseReminder, found %d", count)
	}
	for i := 1; i < len(bodies); i++ {
		if count := strings.Count(bodies[i], emptyResponseReminder); count != 1 {
			t.Errorf("retry request #%d must contain exactly 1 emptyResponseReminder in payload, found %d", i+1, count)
		}
	}

	if n := countSessionMessagesWithContent(s.GetHistory(), emptyResponseReminder); n != 0 {
		t.Errorf("emptyResponseReminder must stay out of session, found %d messages", n)
	}

	last, ok := lastAssistantSessionMessage(s.GetHistory())
	if !ok || last.Content != finalText {
		t.Errorf("expected final assistant message %q in session, got %+v", finalText, last)
	}
}

func TestRunTurn_EmptyResponseExhaustsRetries(t *testing.T) {
	a, s, capture := newEmptyRetryAgent(t, func(n int32) string {
		return `{"choices":[{"delta":{"content":""},"finish_reason":"stop"}]}`
	})

	before := len(s.GetHistory())
	result, err := a.runTurn(context.Background(), s)
	if err != nil {
		t.Fatalf("runTurn: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty response after exhausting retries, got: %s", result)
	}

	if got := capture.count.Load(); got != int32(1+maxEmptyRetries) {
		t.Errorf("expected %d LLM calls (initial + %d retries), got %d", 1+maxEmptyRetries, maxEmptyRetries, got)
	}

	bodies := capture.snapshot()
	for i := 1; i < len(bodies); i++ {
		if count := strings.Count(bodies[i], emptyResponseReminder); count != 1 {
			t.Errorf("retry request #%d must contain exactly 1 emptyResponseReminder, found %d", i+1, count)
		}
	}

	if n := countSessionMessagesWithContent(s.GetHistory(), emptyResponseReminder); n != 0 {
		t.Errorf("emptyResponseReminder must stay out of session, found %d messages", n)
	}
	if n := len(s.GetHistory()); n != before {
		t.Errorf("exhausted retries must not append messages to session, history grew from %d to %d", before, n)
	}
}

func TestIsTerminalResponse(t *testing.T) {
	tests := []struct {
		name         string
		responseText string
		hasToolCalls bool
		hasReasoning bool
		want         bool
	}{
		{"has content", "hello world", false, false, true},
		{"empty no tools", "", false, false, false},
		{"empty with tools", "", true, false, true},
		{"whitespace only", "   ", false, false, false},
		{"reasoning only", "", false, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTerminalResponse(tt.responseText, tt.hasToolCalls, tt.hasReasoning)
			if got != tt.want {
				t.Errorf("isTerminalResponse(%q, %v, %v) = %v, want %v", tt.responseText, tt.hasToolCalls, tt.hasReasoning, got, tt.want)
			}
		})
	}
}
