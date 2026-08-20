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
	config.EnableCompression = false
	a := NewAgent(config)

	peerID := int64(77701)
	steer := "расскажи что ты уже успел сделать?"

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
	config.EnableCompression = false
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

	select {
	case <-blocker.calledCh:
	case <-time.After(5 * time.Second):
		t.Fatal("blocking tool never started")
	}

	a.GetSession(peerID).GetPeerInput().Admit(steer)

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

