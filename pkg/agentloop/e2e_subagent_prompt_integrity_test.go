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

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentpolicy"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

const (
	integrityLeadMarker     = "INTEGRITY_LEAD_SYSTEM"
	integrityWorkerMarker   = "INTEGRITY_WORKER_SYSTEM"
	integrityReviewerMarker = "INTEGRITY_REVIEWER_SYSTEM"

	integrityReviewerDone = "INTEGRITY_REVIEWER_DONE"
)

type integrityRequest struct {
	body         string
	userContents []string
}

type integrityCapture struct {
	mu       sync.Mutex
	requests []integrityRequest
}

func (c *integrityCapture) add(req integrityRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
}

func (c *integrityCapture) all() []integrityRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]integrityRequest, len(c.requests))
	copy(out, c.requests)
	return out
}

func (c *integrityCapture) userMessagesFor(marker string) []string {
	var out []string
	for _, req := range c.all() {
		if strings.Contains(req.body, marker) {
			out = append(out, req.userContents...)
		}
	}
	return out
}

func buildIntegrityPrompt(tag string, lines int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s_START\n", tag))
	for i := 1; i <= lines; i++ {
		b.WriteString(fmt.Sprintf("%s_LINE_%03d: implement module alpha and verify the produced output carefully\n", tag, i))
	}
	b.WriteString(fmt.Sprintf("%s_END", tag))
	return b.String()
}

func taskCallXML(agentName, prompt string) string {
	return fmt.Sprintf("Delegating now.\n<tool_call><function=task>"+
		"<parameter=subagent_type>%s</parameter>"+
		"<parameter=prompt>%s</parameter>"+
		"</function></tool_call>", agentName, prompt)
}

func parseIntegrityRequest(body []byte) integrityRequest {
	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	req := integrityRequest{body: string(body)}
	if err := json.Unmarshal(body, &payload); err != nil {
		return req
	}
	for _, m := range payload.Messages {
		if m.Role == "user" {
			req.userContents = append(req.userContents, m.Content)
		}
	}
	return req
}

func sseWrite(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", content)
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
	fmt.Fprint(w, "data: [DONE]\n")
}

func resolveIntegrityAgent(body string) string {
	switch {
	case strings.Contains(body, integrityReviewerMarker):
		return "reviewer"
	case strings.Contains(body, integrityWorkerMarker):
		return "worker"
	case strings.Contains(body, integrityLeadMarker):
		return "lead"
	}
	return ""
}

func newIntegrityMockLLM(t *testing.T, capture *integrityCapture, script map[string][]string) *httptest.Server {
	t.Helper()
	var stepsMu sync.Mutex
	stepsSeen := map[string]int{}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("mock LLM read body: %v", err)
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("mock LLM: unexpected path %s", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}

		capture.add(parseIntegrityRequest(body))

		agentKey := resolveIntegrityAgent(string(body))
		if agentKey == "" {
			t.Errorf("mock LLM: request matched no known agent")
			sseWrite(w, "UNKNOWN_AGENT")
			return
		}

		stepsMu.Lock()
		step := stepsSeen[agentKey]
		stepsSeen[agentKey] = step + 1
		stepsMu.Unlock()

		scripted := script[agentKey]
		answer := scripted[len(scripted)-1]
		if step < len(scripted) {
			answer = scripted[step]
		}
		sseWrite(w, answer)
	}))
}

func writeIntegrityPromptDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"lead.txt":     integrityLeadMarker + " Delegate everything through the task tool.",
		"worker.txt":   integrityWorkerMarker + " Implement what you are asked.",
		"reviewer.txt": integrityReviewerMarker + " Review what you are asked.",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func newIntegrityOrchestrator(t *testing.T, serverURL, promptDir string) *Orchestrator {
	t.Helper()
	holder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: serverURL, Context: 131072},
		},
	})

	am := agentpolicy.NewAgentManager()
	am.LoadFromConfig(map[string]agentpolicy.AgentCfg{
		"lead": {
			Description:   "Lead",
			Mode:          string(agentpolicy.ModePrimary),
			Coordinator:   true,
			SubagentTypes: []string{"worker"},
		},
		"worker": {
			Description:   "Worker",
			Mode:          string(agentpolicy.ModeSubagent),
			SubagentTypes: []string{"reviewer"},
		},
		"reviewer": {
			Description: "Reviewer",
			Mode:        string(agentpolicy.ModeSubagent),
			Review:      true,
		},
	})

	reg := tools.NewRegistry()
	reg.Register(&tools.FileReadTool{})
	reg.Register(&tools.TimeGetTool{})

	return NewOrchestrator(OrchestratorConfig{
		ModelHolder:     holder,
		MaxTokens:       131072,
		Temperature:     0.1,
		ToolRegistry:    reg,
		SystemPromptDir: promptDir,
		AgentManager:    am,
		Store:           newSubAgentToolTestStore(t),
	})
}

func assertPromptReachesModel(t *testing.T, capture *integrityCapture, marker, prompt, label string) {
	t.Helper()
	messages := capture.userMessagesFor(marker)
	if len(messages) == 0 {
		t.Errorf("BUG: %s never received a user message from %s", marker, label)
		return
	}
	for _, content := range messages {
		if strings.Contains(content, prompt) {
			return
		}
	}
	t.Errorf("BUG: %s prompt (%d chars) did not reach %s verbatim", label, len(prompt), marker)
	for i, content := range messages {
		t.Errorf("  candidate user message %d (%d chars): %.300q", i, len(content), content)
	}
}

func TestE2ESubAgentPromptReachesMockModelVerbatim(t *testing.T) {
	leadPrompt := buildIntegrityPrompt("LEADTASK", 90)
	workerPrompt := buildIntegrityPrompt("WORKERTASK", 90)
	reviewerPrompt := buildIntegrityPrompt("REVIEWERTASK", 90)

	capture := &integrityCapture{}
	server := newIntegrityMockLLM(t, capture, map[string][]string{
		"lead":     {taskCallXML("worker", workerPrompt)},
		"worker":   {taskCallXML("reviewer", reviewerPrompt)},
		"reviewer": {integrityReviewerDone},
	})
	defer server.Close()

	orchestrator := newIntegrityOrchestrator(t, server.URL, writeIntegrityPromptDir(t))

	if _, err := orchestrator.RunAgent(context.Background(), "lead", leadPrompt, 4242); err != nil {
		t.Fatalf("RunAgent(lead): %v", err)
	}

	assertPromptReachesModel(t, capture, integrityLeadMarker, leadPrompt, "user->lead")
	assertPromptReachesModel(t, capture, integrityWorkerMarker, workerPrompt, "lead->worker")
	assertPromptReachesModel(t, capture, integrityReviewerMarker, reviewerPrompt, "worker->reviewer")

	for _, req := range capture.all() {
		for _, content := range req.userContents {
			if strings.Contains(content, "[truncated]") || strings.Contains(content, "lines truncated") {
				t.Errorf("user message was truncated on the way to the model: %.300q", content)
			}
		}
	}

	if len(leadPrompt) < 3000 || len(workerPrompt) < 3000 || len(reviewerPrompt) < 3000 {
		t.Fatalf("test fixture is too small to be meaningful: %d/%d/%d bytes",
			len(leadPrompt), len(workerPrompt), len(reviewerPrompt))
	}

	requests := capture.all()
	if len(requests) < 3 {
		t.Fatalf("expected at least 3 LLM calls (lead, worker, reviewer), got %d", len(requests))
	}
	seenAgents := map[string]bool{}
	for _, req := range requests {
		if k := resolveIntegrityAgent(req.body); k != "" {
			seenAgents[k] = true
		}
	}
	for _, name := range []string{"lead", "worker", "reviewer"} {
		if !seenAgents[name] {
			t.Errorf("agent %q never reached the model: chain depth 3 was not exercised", name)
		}
	}
}
