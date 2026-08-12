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
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

type loggedRequest struct {
	Messages []map[string]interface{} `json:"messages"`
	Tools    []interface{}            `json:"tools,omitempty"`
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

		// Запросы /tokenize (подсчёт токенов контекста) не являются
		// чат-запросами LLM — их не записываем.
		if r.URL.Path == "/v1/chat/completions" {
			var req loggedRequest
			if err := json.Unmarshal(body, &req); err == nil {
				mu.Lock()
				requests = append(requests, req)
				mu.Unlock()
			}
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
		MaxTokens:       8192,
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

func TestOrchestratorClearActiveSessions_CancelsRegisteredContexts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "worker.txt"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()

	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: server.URL},
		},
	})

	o := NewOrchestrator(OrchestratorConfig{
		ModelHolder:     modelHolder,
		SystemPromptDir: dir,
		MaxTokens:       4096,
		Temperature:     0.7,
		ToolRegistry:    tools.NewRegistry(),
	})

	ctx1, cancel1 := context.WithCancel(context.Background())
	o.registerAgentContext("session-1", 123, cancel1)

	ctx2, cancel2 := context.WithCancel(context.Background())
	o.registerAgentContext("session-2", 456, cancel2)

	o.ClearActiveSessions(123)

	select {
	case <-ctx1.Done():
		// session-1 для peer 123 — должна быть отменена
	default:
		t.Error("expected session-1 context to be cancelled after ClearActiveSessions(123)")
	}

	select {
	case <-ctx2.Done():
		t.Error("session-2 for peer 456 should NOT be cancelled by ClearActiveSessions(123)")
	default:
		// session-2 для peer 456 — не должна быть затронута
	}

	cancel2()
}

func TestOrchestratorClearActiveSessions_CancelsRunningAgent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "worker.txt"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	blockCh := make(chan struct{})
	var requestCount int
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if r.URL.Path == "/v1/chat/completions" {
			mu.Lock()
			requestCount++
			mu.Unlock()

			select {
			case <-blockCh:
				w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\n"))
				w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
				w.Write([]byte("[DONE]\n"))
			case <-r.Context().Done():
				return
			}
		}
	}))
	defer server.Close()

	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: server.URL},
		},
	})

	o := NewOrchestrator(OrchestratorConfig{
		ModelHolder:     modelHolder,
		SystemPromptDir: dir,
		MaxTokens:       4096,
		Temperature:     0.7,
		ToolRegistry:    tools.NewRegistry(),
	})

	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = o.RunAgent(ctx, "worker", "test task", 123)
	}()

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if requestCount == 0 {
		t.Fatal("expected at least one LLM request before ClearActiveSessions")
	}
	mu.Unlock()

	o.ClearActiveSessions(123)

	doneCh := make(chan struct{})
	go func() { wg.Wait(); close(doneCh) }()
	select {
	case <-doneCh:
		// Agent finished after cancel — OK.
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not stop after ClearActiveSessions")
	}

	close(blockCh)
}

func TestOrchestratorClearActiveSessions_ReleasesSlots(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "worker.txt"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()

	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: server.URL},
		},
	})

	o := NewOrchestrator(OrchestratorConfig{
		ModelHolder:     modelHolder,
		SystemPromptDir: dir,
		MaxTokens:       4096,
		Temperature:     0.7,
		ToolRegistry:    tools.NewRegistry(),
	})

	ctx1, cancel1 := context.WithCancel(context.Background())
	o.registerAgentContext("session-1", 123, cancel1)

	blockCh := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		select {
		case <-ctx1.Done():
		case <-blockCh:
		}
	}()

	time.Sleep(20 * time.Millisecond)

	o.ClearActiveSessions(123)
	close(blockCh)
	wg.Wait()

	if len(o.activeAgents) != 0 {
		t.Errorf("expected active agents cleared, got %d", len(o.activeAgents))
	}
}
