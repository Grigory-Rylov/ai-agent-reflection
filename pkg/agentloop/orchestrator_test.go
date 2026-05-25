package agentloop

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/opencode/llama-client/pkg/tools"
)

type loggedRequest struct {
	Messages []map[string]string `json:"messages"`
	Tools    []interface{}       `json:"tools,omitempty"`
}

func TestOrchestratorSendsUserMessageToLLM(t *testing.T) {
	promptDir := setupSystemPromptDir(t)
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

	orchestrator := NewOrchestrator(OrchestratorConfig{
		LlamaServerURL:  server.URL,
		Model:           "test-model",
		MaxTokens:       100,
		Temperature:     0.7,
		ToolRegistry:    reg,
		Debug:           false,
		SystemPromptDir: promptDir,
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
	for _, msg := range first.Messages {
		if msg["role"] == "user" && strings.Contains(msg["content"], prompt) {
			foundUser = true
			break
		}
	}
	if !foundUser {
		t.Fatalf("first LLM request has no user message containing the task prompt. Messages: %+v", first.Messages)
	}
}

func TestOrchestrator_CoordinatorReturnsResponse(t *testing.T) {
	promptDir := setupSystemPromptDir(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hello from coordinator\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	reg := tools.NewRegistry()
	reg.Register(&tools.FileReadTool{})

	orchestrator := NewOrchestrator(OrchestratorConfig{
		LlamaServerURL:  server.URL,
		Model:           "test-model",
		MaxTokens:       100,
		Temperature:     0.7,
		ToolRegistry:    reg,
		Debug:           false,
		SystemPromptDir: promptDir,
	})

	ctx := context.Background()
	result, err := orchestrator.ExecuteTask(ctx, "тестовая задача", 12346)
	if err != nil {
		t.Fatalf("ExecuteTask failed: %v", err)
	}

	if !strings.Contains(result, "Hello from coordinator") {
		t.Errorf("expected result to contain coordinator response, got: %q", result)
	}
}
