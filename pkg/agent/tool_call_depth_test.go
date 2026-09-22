package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func newAlwaysToolCallAgent(t *testing.T, config Config) (*agentImpl, *atomic.Int32) {
	t.Helper()
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range scriptedToolCallRound([2]string{"calc", `{"expression":"1+1"}`}) {
			fmt.Fprint(w, "data: "+chunk+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.RetryDelay = 5 * time.Millisecond

	a := NewAgent(config)
	a.SetToolExecutor(NewStubToolExecutor(filepath.Join(t.TempDir(), "tools.log")))

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test")
	s.AddUserMessage("go")
	a.mu.Lock()
	a.sessions[1] = s
	a.mu.Unlock()

	return a, &callCount
}

func TestRunTurn_MaxToolCallDepthFromConfig(t *testing.T) {
	config := DefaultConfig()
	config.MaxToolCallDepth = 2
	a, callCount := newAlwaysToolCallAgent(t, config)

	result, err := a.runTurn(context.Background(), a.getSession(1))
	if err != nil {
		t.Fatalf("runTurn error: %v", err)
	}

	want := "[TOOL] Tool call iteration limit reached (2 rounds in one turn), stopping to avoid an unbounded loop."
	if result != want {
		t.Errorf("expected limit message with configured depth, got %q", result)
	}
	if got := callCount.Load(); got != 2 {
		t.Errorf("expected exactly 2 LLM calls before the limit stopped the turn, got %d", got)
	}

	found := false
	for _, m := range a.getSession(1).GetHistory() {
		if m.Role == sess.AssistantRole && m.Content == want {
			found = true
		}
	}
	if !found {
		t.Error("expected limit message appended to session history")
	}
}

func TestRunTurn_ZeroMaxToolCallDepthFallsBackToDefault(t *testing.T) {
	config := Config{}
	a, callCount := newAlwaysToolCallAgent(t, config)

	result, err := a.runTurn(context.Background(), a.getSession(1))
	if err != nil {
		t.Fatalf("runTurn error: %v", err)
	}

	if !strings.Contains(result, strconv.Itoa(defaultMaxTurnIterations)) {
		t.Errorf("expected fallback to default limit %d, got %q", defaultMaxTurnIterations, result)
	}
	if got := callCount.Load(); got != int32(defaultMaxTurnIterations) {
		t.Errorf("expected exactly %d LLM calls before the default limit stopped the turn, got %d", defaultMaxTurnIterations, got)
	}
}
