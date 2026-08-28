package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

const processWorkingDirPrompt = "You are a helpful assistant."

type capturedLLM struct {
	mu     sync.Mutex
	calls  int
	system string
}

func (c *capturedLLM) record(body string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++

	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return
	}
	for _, m := range req.Messages {
		if m.Role == "system" && c.system == "" {
			c.system = m.Content
		}
	}
}

func (c *capturedLLM) systemPrompt() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.system
}

func (c *capturedLLM) chatCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func startCapturedLLM(t *testing.T, captured *capturedLLM) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "chat/completions") {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read llm request body: %v", err)
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		captured.record(string(body))

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "[DONE]\n")
	}))
	t.Cleanup(server.Close)

	return server.URL
}

func writeSystemPromptFile(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "system_prompt.txt")
	if err := os.WriteFile(path, []byte(processWorkingDirPrompt), 0644); err != nil {
		t.Fatalf("write system prompt: %v", err)
	}

	return path
}

type workingDirFixture struct {
	loop       AgentLoop
	captured   *capturedLLM
	processDir string
	peerDir    string
}

func newWorkingDirFixture(t *testing.T) *workingDirFixture {
	t.Helper()

	processDir := t.TempDir()
	peerDir := t.TempDir()
	promptFile := writeSystemPromptFile(t, processDir)

	captured := &capturedLLM{}
	host := startCapturedLLM(t, captured)
	holder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: host, Context: 4096},
		},
	})

	oldWorkingDir := tools.WorkingDir
	t.Cleanup(func() { tools.SetWorkingDir(oldWorkingDir) })

	return &workingDirFixture{
		loop:       newLoopForWorkingDirs(t, holder, promptFile, processDir),
		captured:   captured,
		processDir: processDir,
		peerDir:    peerDir,
	}
}

func newLoopForWorkingDirs(t *testing.T, holder *modelsconfig.Holder, promptFile, processDir string) AgentLoop {
	t.Helper()

	config := DefaultLoopConfig()
	config.ModelHolder = holder
	config.SystemPromptFile = promptFile
	config.EnableTools = false
	config.EnableCompression = false
	config.EnablePruning = false
	config.SessionConfig.WorkingDir = processDir

	loop, err := NewAgentLoop(config, &mockVKClient{}, tools.NewRegistry())
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}

	return loop
}

func (f *workingDirFixture) switchPeerToWorkingDir(t *testing.T, peerID int64) {
	t.Helper()

	sess := f.loop.EnsureSession(peerID)
	if sess == nil {
		t.Fatal("EnsureSession returned nil")
	}
	if got := sess.GetWorkingDir(); got != f.processDir {
		t.Fatalf("session working dir before switch = %q, want process dir %q", got, f.processDir)
	}

	sess.SetWorkingDir(f.peerDir)
}

func (f *workingDirFixture) runPrompt(t *testing.T, peerID int64) {
	t.Helper()

	if _, err := f.loop.ProcessPrompt(context.Background(), "hello", peerID); err != nil {
		t.Fatalf("ProcessPrompt: %v", err)
	}
	if f.captured.chatCalls() == 0 {
		t.Fatal("no request reached the LLM")
	}
}

func TestSendToLLMUsesPeerWorkingDirInSystemPrompt(t *testing.T) {
	const peerID = int64(7101)

	fixture := newWorkingDirFixture(t)
	fixture.switchPeerToWorkingDir(t, peerID)
	fixture.runPrompt(t, peerID)

	system := fixture.captured.systemPrompt()
	if !strings.Contains(system, "Working directory: "+fixture.peerDir) {
		t.Errorf("system prompt must contain peer working dir %q\ngot system prompt:\n%s", fixture.peerDir, system)
	}
	if strings.Contains(system, fixture.processDir) {
		t.Errorf("system prompt leaked process working dir %q\ngot system prompt:\n%s", fixture.processDir, system)
	}
}

func TestSendToLLMKeepsGlobalWorkingDirOnPeer(t *testing.T) {
	const peerID = int64(7102)

	fixture := newWorkingDirFixture(t)
	fixture.switchPeerToWorkingDir(t, peerID)
	tools.SetWorkingDir(fixture.processDir)

	fixture.runPrompt(t, peerID)

	if got := tools.WorkingDir; got != fixture.peerDir {
		t.Errorf("global working dir after prompt = %q, want peer dir %q (must not roll back to process dir %q)",
			got, fixture.peerDir, fixture.processDir)
	}
}
