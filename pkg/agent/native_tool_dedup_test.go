package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func TestNativeToolCallDedupInProcessToolResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\": [{\"delta\": {\"tool_calls\": [{\"index\": 0, \"id\": \"call_1\", \"type\": \"function\", \"function\": {\"name\": \"shell_execute\", \"arguments\": \"{\\\"command\\\": \\\"echo hello\\\"}\"}}]}, \"finish_reason\": null}]}\n\n"))
		w.Write([]byte("data: {\"choices\": [{\"delta\": {}, \"finish_reason\": \"tool_calls\"}]}\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
	}
	config.SessionConfig.PeerID = 99931

	a, executor := newTestAgentWithStub(t, config)
	a.toolsRegistry.Register(&tools.ShellExecuteTool{})

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "Test", 99931)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	t.Logf("response: %q", response)

	if got := executor.Count("[TOOL] Call: shell_execute"); got != 1 {
		t.Errorf("expected shell_execute to be executed exactly once (dedup), got %d", got)
	}
}
