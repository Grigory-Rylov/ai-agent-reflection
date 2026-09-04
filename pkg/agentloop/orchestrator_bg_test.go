package agentloop

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agent"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentpolicy"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func TestRunAgentUsesEngineTypeForSubAgent(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"qa.txt", "coordinator.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("You are a helpful assistant."), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(r.URL.Path, "chat/completions") {
			mu.Lock()
			bodies = append(bodies, body)
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"OK"}}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "ninfer-model",
		Models:  map[string]modelsconfig.ModelEntry{"ninfer-model": {Name: "m", Host: server.URL, Type: "ninfer"}},
	})

	orchestrator := NewOrchestrator(OrchestratorConfig{
		ModelHolder:     modelHolder,
		MaxTokens:       8192,
		Temperature:     0.7,
		SystemPromptDir: dir,
		AgentManager:    agentpolicy.NewAgentManager(),
	})

	if _, err := orchestrator.RunAgent(context.Background(), "qa", "do something", 1); err != nil {
		t.Fatalf("RunAgent: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 {
		t.Fatal("no LLM requests were made")
	}
	for _, b := range bodies {
		if strings.Contains(string(b), `"chat_template_kwargs"`) {
			t.Errorf("request must not contain chat_template_kwargs for ninfer engine: %s", b)
		}
		if !strings.Contains(string(b), `"enable_thinking":true`) {
			t.Errorf("request must contain top-level enable_thinking for ninfer engine: %s", b)
		}
	}
}

type captureAgent struct {
	reg *tools.Registry
}

func (c *captureAgent) ProcessMessage(ctx context.Context, message string, peerID int64) (string, error) {
	return "", nil
}
func (c *captureAgent) ResetSession(peerID int64)                       {}
func (c *captureAgent) GetSession(peerID int64) *session.Session        { return nil }
func (c *captureAgent) SetThinkingCallback(cb agent.ThinkingCallback)   {}
func (c *captureAgent) SetTools(toolSchemas []map[string]interface{})   {}
func (c *captureAgent) SetToolExecutor(executor agent.ToolExecutor)     {}
func (c *captureAgent) RegisterTools(reg *tools.Registry)               { c.reg = reg }
func (c *captureAgent) ReplaceTools(reg *tools.Registry)                { c.reg = reg }

func TestOrchestratorReadOnlyToolsIncludeBackground(t *testing.T) {
	o := NewOrchestrator(OrchestratorConfig{})
	cap := &captureAgent{}
	o.addReadOnlyTools(cap)
	if cap.reg == nil {
		t.Fatal("addReadOnlyTools did not replace tools")
	}
	for _, name := range []string{"shell_execute", "shell_background", "shell_check"} {
		if _, ok := cap.reg.Get(name); !ok {
			t.Errorf("read-only registry missing tool %q", name)
		}
	}
}

func TestOrchestratorRegistersBGDeliveryForSubAgent(t *testing.T) {
	hub := tools.NewBackgroundHub(4)
	hub.SetLogDir(t.TempDir())
	tools.SetBackgroundHub(hub)
	defer tools.SetBackgroundHub(nil)

	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models:  map[string]modelsconfig.ModelEntry{"test": {Name: "test-model", Host: "http://127.0.0.1:1"}},
	})

	o := NewOrchestrator(OrchestratorConfig{ModelHolder: modelHolder})
	a, sessionID, err := o.makeSubAgent("qa", "prompt", 1)
	if err != nil {
		t.Fatalf("makeSubAgent: %v", err)
	}
	if sessionID == "" {
		t.Fatal("expected non-empty session id")
	}
	if hub.DeliveryFor(sessionID) == nil {
		t.Fatal("expected BG delivery registered for sub-agent session")
	}

	o.cleanupAgentBG(sessionID)
	if hub.DeliveryFor(sessionID) != nil {
		t.Fatal("expected delivery unregistered after cleanup")
	}
	_ = a
}