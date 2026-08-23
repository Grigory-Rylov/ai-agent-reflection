package agentloop

import (
	"context"

	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func newPersistenceTestLoop(t *testing.T, sseResponses [][]string) (*agentLoop, store.Store) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "chat/completions") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{}`))
			return
		}
		var chunks []string
		if len(sseResponses) > 0 {
			chunks = sseResponses[0]
			sseResponses = sseResponses[1:]
		} else {
			chunks = []string{`{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			w.Write([]byte("data: " + c + "\n\n"))
		}
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(server.Close)

	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: server.URL},
		},
	})

	dbStore := newSubAgentToolTestStore(t)

	reg := tools.NewRegistry()
	reg.Register(&tools.TimeGetTool{})

	loop, err := NewAgentLoop(LoopConfig{
		ModelHolder:       modelHolder,
		MaxTokens:         8192,
		Temperature:       0.7,
		Debug:             false,
		EnableTools:       true,
		EnableCompression: false,
		SessionConfig:     sessionConfigForStore(dbStore),
	}, nil, reg)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	return loop.(*agentLoop), dbStore
}

func sessionConfigForStore(st store.Store) session.Config {
	cfg := session.DefaultConfig()
	cfg.Store = st
	cfg.AutoSave = true
	return cfg
}

func storedMessageCount(t *testing.T, st store.Store, peerID int64) int {
	t.Helper()
	msgs, err := st.GetMessages(peerID)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	return len(msgs)
}

func TestToolTrafficPersistsInSingleWriterSession(t *testing.T) {
	loop, st := newPersistenceTestLoop(t, [][]string{
		{
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"time_get","arguments":"{}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		},
		{
			`{"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`,
		},
	})

	peerID := int64(4321)
	if _, err := loop.ProcessPrompt(context.Background(), "который час?", peerID); err != nil {
		t.Fatalf("ProcessPrompt: %v", err)
	}

	msgs, err := st.GetMessages(peerID)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	hasToolCallMsg := false
	hasToolResultMsg := false
	for _, m := range msgs {
		if m.Role == "assistant" && strings.Contains(m.ToolCalls, "time_get") {
			hasToolCallMsg = true
		}
		if m.Role == "tool" && m.ToolName == "time_get" {
			hasToolResultMsg = true
		}
	}
	if !hasToolCallMsg {
		t.Error("BUG: assistant tool_call message missing from persisted history")
	}
	if !hasToolResultMsg {
		t.Error("BUG: tool result message missing from persisted history")
	}
}

func TestRestartedSessionKeepsFullHistory(t *testing.T) {
	loop, st := newPersistenceTestLoop(t, [][]string{
		{
			`{"choices":[{"delta":{"content":"first answer"},"finish_reason":"stop"}]}`,
		},
	})

	peerID := int64(4322)
	if _, err := loop.ProcessPrompt(context.Background(), "первый вопрос", peerID); err != nil {
		t.Fatalf("ProcessPrompt 1: %v", err)
	}
	countAfterFirst := storedMessageCount(t, st, peerID)

	reloaded, err := NewAgentLoop(LoopConfig{
		ModelHolder:       loop.config.ModelHolder,
		MaxTokens:         8192,
		Temperature:       0.7,
		Debug:             false,
		EnableTools:       true,
		EnableCompression: false,
		SessionConfig:     sessionConfigForStore(st),
	}, nil, loop.registry)
	if err != nil {
		t.Fatalf("NewAgentLoop reload: %v", err)
	}

	sess := reloaded.EnsureSession(peerID)
	hist := sess.GetHistory()
	if len(hist) < countAfterFirst {
		t.Errorf("BUG: reloaded session lost history: before=%d after=%d", countAfterFirst, len(hist))
	}
	found := false
	for _, m := range hist {
		if m.Role == "user" && m.Content == "первый вопрос" {
			found = true
		}
	}
	if !found {
		t.Error("reloaded session lost the original user prompt")
	}
}
