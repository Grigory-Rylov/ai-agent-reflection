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
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)


func TestProcessPromptWithSystemPrompt_AppliesLeadPrompt(t *testing.T) {
	const mainPrompt = "You are opencode, the main coordinator assistant."

	
	promptDir := t.TempDir()
	sysPromptFile := filepath.Join(promptDir, "system_prompt.txt")
	if err := os.WriteFile(sysPromptFile, []byte(mainPrompt), 0644); err != nil {
		t.Fatal(err)
	}

	
	
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
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: server.URL, Context: 4096},
		},
	})

	config := DefaultLoopConfig()
	config.ModelHolder = holder
	config.SystemPromptFile = sysPromptFile
	config.EnableTools = true
	config.EnableCompression = false
	config.EnablePruning = false
	config.SessionConfig.WorkingDir = promptDir

	loop, err := NewAgentLoop(config, &mockVKClient{}, tools.NewRegistry())
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}

	const leadPrompt = "You are a Lead Agent. Delegate tasks to worker and qa via the task tool."

	_, err = loop.ProcessPromptWithSystemPrompt(context.Background(), "build a project", 12345, leadPrompt)
	if err != nil {
		t.Fatalf("ProcessPromptWithSystemPrompt: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(capturedSystem, "You are a Lead Agent") {
		t.Errorf("lead system prompt was not sent to the LLM.\ncaptured system prompt: %q", capturedSystem)
	}
	if strings.Contains(capturedSystem, mainPrompt) {
		t.Errorf("lead system prompt must REPLACE the main prompt, not concatenate. main prompt leaked into system: %q", capturedSystem)
	}
}


func TestProcessPromptWithSystemPrompt_AppliesLeadPrompt_WithPersistedHistory(t *testing.T) {
	const mainPrompt = "You are opencode, the main coordinator assistant."

	promptDir := t.TempDir()
	sysPromptFile := filepath.Join(promptDir, "system_prompt.txt")
	if err := os.WriteFile(sysPromptFile, []byte(mainPrompt), 0644); err != nil {
		t.Fatal(err)
	}

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
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: server.URL, Context: 4096},
		},
	})

	dbStore, err := store.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer dbStore.Close()

	config := DefaultLoopConfig()
	config.ModelHolder = holder
	config.SystemPromptFile = sysPromptFile
	config.EnableTools = true
	config.EnableCompression = false
	config.EnablePruning = false
	config.SessionConfig.WorkingDir = promptDir
	
	config.SessionConfig.Store = dbStore
	config.SessionConfig.AutoSave = true

	loop, err := NewAgentLoop(config, &mockVKClient{}, tools.NewRegistry())
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}

	
	
	if _, err := loop.ProcessPrompt(context.Background(), "hello", 12345); err != nil {
		t.Fatalf("priming ProcessPrompt: %v", err)
	}

	
	const leadPrompt = "You are a Lead Agent. Delegate tasks to worker and qa via the task tool."
	capturedSystem = ""
	if _, err := loop.ProcessPromptWithSystemPrompt(context.Background(), "build a project", 12345, leadPrompt); err != nil {
		t.Fatalf("ProcessPromptWithSystemPrompt: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(capturedSystem, "You are a Lead Agent") {
		t.Errorf("lead system prompt was not sent to the LLM when session had persisted history.\ncaptured system prompt: %q", capturedSystem)
	}
	
	if strings.Contains(capturedSystem, mainPrompt) {
		t.Errorf("lead system prompt must REPLACE the main prompt, not concatenate. main prompt leaked: %q", capturedSystem)
	}
}


func TestProcessPromptWithSystemPrompt_FollowUpWithoutLeadKeepsContext(t *testing.T) {
	const mainPrompt = "You are opencode, the main coordinator assistant."

	promptDir := t.TempDir()
	sysPromptFile := filepath.Join(promptDir, "system_prompt.txt")
	if err := os.WriteFile(sysPromptFile, []byte(mainPrompt), 0644); err != nil {
		t.Fatal(err)
	}

	
	
	var mu sync.Mutex
	var lastMessages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	call := 0
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

		mu.Lock()
		call++
		lastMessages = req.Messages
		mu.Unlock()

		resp := fmt.Sprintf("response-%d", call)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", resp)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "[DONE]\n")
	}))
	defer server.Close()

	holder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: server.URL, Context: 4096},
		},
	})

	dbStore, err := store.NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer dbStore.Close()

	config := DefaultLoopConfig()
	config.ModelHolder = holder
	config.SystemPromptFile = sysPromptFile
	config.EnableTools = false
	config.EnableCompression = false
	config.EnablePruning = false
	config.SessionConfig.WorkingDir = promptDir
	config.SessionConfig.Store = dbStore
	config.SessionConfig.AutoSave = true

	loop, err := NewAgentLoop(config, &mockVKClient{}, tools.NewRegistry())
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}

	const leadPrompt = "You are a Lead Agent. Delegate tasks to worker and qa via the task tool."

	
	if _, err := loop.ProcessPromptWithSystemPrompt(context.Background(), "build a project", 777, leadPrompt); err != nil {
		t.Fatalf("lead ProcessPromptWithSystemPrompt: %v", err)
	}

	
	if _, err := loop.ProcessPrompt(context.Background(), "clarify please", 777); err != nil {
		t.Fatalf("follow-up ProcessPrompt: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	
	var sysPrompt string
	var historyContents []string
	for _, m := range lastMessages {
		if m.Role == "system" {
			sysPrompt = m.Content
		} else {
			historyContents = append(historyContents, m.Content)
		}
	}
	if strings.Contains(sysPrompt, "You are a Lead Agent") {
		t.Errorf("lead system prompt leaked into follow-up without #lead.\nsystem prompt: %q", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "You are opencode") {
		t.Errorf("follow-up system prompt is not the original main prompt.\nsystem prompt: %q", sysPrompt)
	}

	
	var hasLeadTask, hasLeadResponse, hasFollowUp bool
	for _, c := range historyContents {
		if strings.Contains(c, "build a project") {
			hasLeadTask = true
		}
		if strings.Contains(c, "response-1") {
			hasLeadResponse = true
		}
		if strings.Contains(c, "clarify please") {
			hasFollowUp = true
		}
	}
	if !hasLeadTask {
		t.Errorf("#lead task 'build a project' is missing from follow-up context. history: %v", historyContents)
	}
	if !hasLeadResponse {
		t.Errorf("#lead assistant response is missing from follow-up context. history: %v", historyContents)
	}
	if !hasFollowUp {
		t.Errorf("follow-up 'clarify please' is missing from its own request. history: %v", historyContents)
	}
}
