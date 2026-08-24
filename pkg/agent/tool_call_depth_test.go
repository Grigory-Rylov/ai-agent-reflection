package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func newDepthTestAgent(t *testing.T, config Config) *agentImpl {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("LLM must not be called after the depth limit is reached")
	}))
	t.Cleanup(server.Close)

	config.LlamaServerURL = server.URL
	config.Model = "test-model"

	return NewAgent(config)
}

func TestProcessToolResults_MaxToolCallDepthFromConfig(t *testing.T) {
	config := DefaultConfig()
	config.MaxToolCallDepth = 2
	a := newDepthTestAgent(t, config)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test")
	a.mu.Lock()
	a.sessions[1] = s
	a.mu.Unlock()

	ctx := context.WithValue(context.Background(), toolCallDepthKey, 2)
	result, err := a.processToolResults(ctx, []Message{{Role: "user", Content: "go"}}, "", nil, nil, s, make(map[string]bool))
	if err != nil {
		t.Fatalf("processToolResults error: %v", err)
	}

	want := "[TOOL] Tool call recursion limit reached (2 batches in one turn), stopping to avoid an unbounded loop."
	if result != want {
		t.Errorf("expected limit message with configured depth, got %q", result)
	}
}

func TestProcessToolResults_ZeroMaxToolCallDepthFallsBackToDefault(t *testing.T) {
	config := Config{}
	a := newDepthTestAgent(t, config)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test")
	a.mu.Lock()
	a.sessions[1] = s
	a.mu.Unlock()

	ctx := context.WithValue(context.Background(), toolCallDepthKey, maxToolCallDepth)
	result, err := a.processToolResults(ctx, []Message{{Role: "user", Content: "go"}}, "", nil, nil, s, make(map[string]bool))
	if err != nil {
		t.Fatalf("processToolResults error: %v", err)
	}

	if !strings.Contains(result, strconv.Itoa(maxToolCallDepth)) {
		t.Errorf("expected fallback to default depth %d, got %q", maxToolCallDepth, result)
	}
}
