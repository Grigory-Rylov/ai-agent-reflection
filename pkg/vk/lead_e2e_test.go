package vk

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

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentloop"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentpolicy"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

func TestLeadHashEndToEnd(t *testing.T) {
	const leadContent = "You are a Lead Agent. Delegate tasks to worker and qa via the task tool."

	var mu sync.Mutex
	var capturedSystem string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		for _, m := range req.Messages {
			if m.Role == "system" {
				mu.Lock()
				capturedSystem = m.Content
				mu.Unlock()
				break
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", "done")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "[DONE]\n")
	}))
	defer server.Close()

	holder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models:  map[string]modelsconfig.ModelEntry{"test": {Name: "test-model", Host: server.URL, Context: 4096}},
	})

	dir := t.TempDir()
	sysPromptFile := filepath.Join(dir, "system_prompt.txt")
	if err := os.WriteFile(sysPromptFile, []byte("You are opencode, the main coordinator assistant."), 0644); err != nil {
		t.Fatal(err)
	}

	loopConfig := agentloop.DefaultLoopConfig()
	loopConfig.ModelHolder = holder
	loopConfig.SystemPromptFile = sysPromptFile
	loopConfig.EnableTools = true
	loopConfig.EnableCompression = true
	loopConfig.EnablePruning = true
	loopConfig.SessionConfig.WorkingDir = dir

	loop, err := agentloop.NewAgentLoop(loopConfig, nil, tools.NewRegistry())
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}

	am := agentpolicy.NewAgentManager()
	am.LoadFromConfig(map[string]agentpolicy.AgentCfg{
		"lead": {Mode: "primary", Prompt: leadContent},
	})

	orch := agentloop.NewOrchestrator(agentloop.OrchestratorConfig{
		ModelHolder:     holder,
		MaxTokens:       4096,
		ToolRegistry:    tools.NewRegistry(),
		AgentManager:    am,
		SystemPromptDir: dir,
	})

	log, _ := logger.New(logger.DefaultConfig())
	handler := NewBotHandlerWithPeerID(nil, loop, log, 0, 0, orch, holder)
	handler.SetTargetQueue(agentloop.NewTargetQueue(
		func(ctx context.Context, name, prompt string, peerID int64) (string, error) {
			return "queued-resp", nil
		},
		func(name string, peerID int64, response string, err error) {},
	))

	_ = handler.ProcessMessage("#lead build a project", 12345)

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(capturedSystem, "You are a Lead Agent") {
		t.Errorf("lead prompt was not applied end-to-end.\ncaptured system prompt: %q", capturedSystem)
	}
}
