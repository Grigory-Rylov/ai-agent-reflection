package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func TestNativeToolCallLoopBreaksOnCorrection(t *testing.T) {
	reqCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.Header().Set("Content-Type", "text/event-stream")
		switch reqCount {
		case 1:
			w.Write([]byte("data: {\"choices\": [{\"delta\": {\"tool_calls\": [{\"index\": 0, \"id\": \"call_1\", \"type\": \"function\", \"function\": {\"name\": \"shell_execute\", \"arguments\": \"{\\\"command\\\": \\\"echo hello\\\"}\"}}]}, \"finish_reason\": null}]}\n\n"))
			w.Write([]byte("data: {\"choices\": [{\"delta\": {}, \"finish_reason\": \"tool_calls\"}]}\n\n"))
		case 2:
			w.Write([]byte("data: {\"choices\": [{\"delta\": {\"tool_calls\": [{\"index\": 0, \"id\": \"call_2\", \"type\": \"function\", \"function\": {\"name\": \"shell_execute\", \"arguments\": \"{\\\"command\\\": \\\"echo hello\\\"}\"}}]}, \"finish_reason\": null}]}\n\n"))
			w.Write([]byte("data: {\"choices\": [{\"delta\": {}, \"finish_reason\": \"tool_calls\"}]}\n\n"))
		default:
			w.Write([]byte("data: {\"choices\": [{\"delta\": {\"content\": \"Done.\"}, \"finish_reason\": null}]}\n\n"))
			w.Write([]byte("data: {\"choices\": [{\"delta\": {}, \"finish_reason\": \"stop\"}]}\n\n"))
		}
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		EnableTools:    true,
		SessionConfig:  session.DefaultConfig(),
	}
	config.SessionConfig.PeerID = 99932

	a, _ := newTestAgentWithStub(t, config)
	a.toolsRegistry.Register(&tools.ShellExecuteTool{})

	var thinking []string
	a.SetThinkingCallback(func(peerID int64, content string) error {
		thinking = append(thinking, content)
		return nil
	})

	ctx := context.Background()
	_, err := a.ProcessMessage(ctx, "Test", 99932)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	s := a.GetSession(99932)
	history := s.GetHistory()

	hasCorrection := false
	for _, m := range history {
		if m.Role == session.UserRole && strings.Contains(m.Content, "stuck in a loop") {
			hasCorrection = true
			break
		}
	}
	if !hasCorrection {
		for i, m := range history {
			t.Logf("  [%d] role=%s content=%.80q", i, m.Role, m.Content)
		}
		t.Error("expected corrective user message injected into session history")
	}

	hasLoopAlert := false
	for _, th := range thinking {
		if strings.Contains(th, "[LOOP]") {
			hasLoopAlert = true
			break
		}
	}
	if !hasLoopAlert {
		t.Errorf("expected [LOOP] alert in thinking channel, got: %v", thinking)
	}
}
