package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

// TestSteerPromotedIntoNextLLMRequest verifies the opencode-style "steer"
// delivery: a user message admitted to the peer inbox while the agent is
// working becomes part of the very next LLM request instead of being ignored.
func TestSteerPromotedIntoNextLLMRequest(t *testing.T) {
	var lastBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, validSSEStream())
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.RetryDelay = time.Millisecond
	config.EnableCompression = false // keep the test deterministic
	a := NewAgent(config)

	peerID := int64(77701)
	steer := "расскажи что ты уже успел сделать?"

	// A message "arrives" while the (previous) turn would be running: it is
	// sitting in the peer inbox before this turn starts.
	a.GetSession(peerID).GetPeerInput().Admit(steer)

	response, err := a.ProcessMessage(context.Background(), "выполни задачу", peerID)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}
	if response == "" {
		t.Fatal("expected non-empty response")
	}
	if !strings.Contains(lastBody, steer) {
		t.Errorf("steer message not injected into the LLM request body:\n%s", lastBody)
	}

	// The steer must also land in the session history, so it survives the run.
	hist := a.GetSession(peerID).GetHistory()
	found := false
	for _, m := range hist {
		if strings.Contains(m.Content, steer) {
			found = true
			break
		}
	}
	if !found {
		t.Error("steer message not present in session history after the run")
	}
}


// steerBlocker is a tool whose execution blocks until released. It lets the
// test pause the agent mid-task while a user message arrives.
type steerBlocker struct {
	release     chan struct{}
	releaseOnce sync.Once
	calledCh    chan struct{}
	once        sync.Once
}

func (t *steerBlocker) Name() string { return "steer_blocker" }

func (t *steerBlocker) Description() string {
	return "blocks the agent until released (test helper)"
}

func (t *steerBlocker) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *steerBlocker) Release() {
	t.releaseOnce.Do(func() { close(t.release) })
}

func (t *steerBlocker) Execute(ctx context.Context, _ map[string]string) (tools.ToolResult, error) {
	t.once.Do(func() { close(t.calledCh) })
	select {
	case <-ctx.Done():
		return tools.ToolResult{Success: false, Error: "cancelled"}, nil
	case <-t.release:
	}
	return tools.ToolResult{Success: true, Data: map[string]interface{}{"ok": true}}, nil
}


// TestSteer_DeliveredDuringRunningToolLoop reproduces the reported bug: the
// agent is mid-task (executing a tool), the user writes "расскажи что ты уже
// успел сделать?", and previously the message was silently parked behind the
// peer mutex. Now it must be promoted into the agent's NEXT LLM request.
func TestSteer_DeliveredDuringRunningToolLoop(t *testing.T) {
	var (
		mu       sync.Mutex
		bodies   []string
		reqCount int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		reqCount++
		n := reqCount
		body := string(b)
		bodies = append(bodies, body)
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			// First turn: ask the model to call the blocking tool (native format).
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"steer_blocker\",\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		} else {
			w.Write([]byte(validSSEStream()))
		}
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.RetryDelay = time.Millisecond
	config.EnableCompression = false // keep the test deterministic
	a := NewAgent(config)

	blocker := &steerBlocker{release: make(chan struct{}), calledCh: make(chan struct{})}
	defer blocker.Release()
	a.toolsRegistry.Register(blocker)

	peerID := int64(80801)
	steer := "расскажи что ты уже успел сделать?"

	type runResult struct {
		resp string
		err  error
	}
	done := make(chan runResult, 1)
	go func() {
		resp, err := a.ProcessMessage(context.Background(), "выполни длинную задачу", peerID)
		done <- runResult{resp, err}
	}()

	// Wait until the agent is busy executing the tool.
	select {
	case <-blocker.calledCh:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking tool never started")
	}

	// The user writes a message while the agent is still busy. It must be
	// handed to the running turn via the peer inbox, not ignored.
	a.GetSession(peerID).GetPeerInput().Admit(steer)

	// Release the tool; the loop continues and promotes the steer into the
	// SECOND LLM request.
	blocker.Release()

	res := <-done
	if res.err != nil {
		t.Fatalf("ProcessMessage failed: %v", res.err)
	}
	if res.resp != "Hello" {
		t.Errorf("expected response %q, got %q", "Hello", res.resp)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < 2 {
		t.Fatalf("expected at least 2 LLM requests (tool call + follow-up), got %d", len(bodies))
	}
	if strings.Contains(bodies[0], steer) {
		t.Errorf("steer must NOT be in the first request (it arrived after it):\n%s", bodies[0])
	}
	if !strings.Contains(bodies[1], steer) {
		t.Errorf("steer was NOT promoted into the request after the tool result:\n%s", bodies[1])
	}
}

