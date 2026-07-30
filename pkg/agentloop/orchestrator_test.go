package agentloop

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/opencode/llama-client/pkg/modelsconfig"
	"github.com/opencode/llama-client/pkg/tools"
)

type loggedRequest struct {
	Messages []map[string]interface{} `json:"messages"`
	Tools    []interface{}       `json:"tools,omitempty"`
}

func TestOrchestratorSendsUserMessageToLLM(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"worker.txt", "qa.txt", "coordinator.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("You are a helpful assistant."), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	var requests []loggedRequest


	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewBuffer(body))

		var req loggedRequest
		if err := json.Unmarshal(body, &req); err == nil {
			mu.Lock()
			requests = append(requests, req)
			mu.Unlock()
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Some response\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	prompt := "нужно изучить текущий проект и создать документацию с рекомендациями по доработке"

	reg := tools.NewRegistry()
	reg.Register(&tools.FileReadTool{})
	reg.Register(&tools.TimeGetTool{})

	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: server.URL},
		},
	})

	orchestrator := NewOrchestrator(OrchestratorConfig{
		ModelHolder:     modelHolder,
		MaxTokens:       100,
		Temperature:     0.7,
		ToolRegistry:    reg,
		Debug:           false,
		SystemPromptDir: dir,
	})

	ctx := context.Background()
	_, err := orchestrator.ExecuteTask(ctx, prompt, 12345)
	if err != nil {
		t.Fatalf("ExecuteTask failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(requests) == 0 {
		t.Fatal("no LLM requests were made")
	}

	first := requests[0]
	if len(first.Messages) < 2 {
		t.Fatalf("first LLM request has %d messages, expected >= 2 (system + user). Full request body: %+v",
			len(first.Messages), first)
	}

	foundUser := false
	foundSystem := false
	for _, msg := range first.Messages {
		role, _ := msg["role"].(string)
		switch role {
		case "system":
			foundSystem = true
		case "user":
			foundUser = true
		}
	}

	if !foundSystem {
		t.Error("LLM request should contain a system message")
	}
	if !foundUser {
		t.Error("LLM request should contain a user message")
	}

	hasUserPrompt := false
	for _, msg := range first.Messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		if role == "user" && strings.Contains(strings.ToLower(content), strings.ToLower(prompt)) {
			hasUserPrompt = true
			break
		}
	}
	if !hasUserPrompt {
		t.Errorf("expected user message to contain '%s', got messages: %+v", prompt, first.Messages)
	}
}
