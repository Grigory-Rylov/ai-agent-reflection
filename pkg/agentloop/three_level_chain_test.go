package agentloop

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

func TestThreeLevelChainResumeLeadWorkerReviewer(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"lead.txt", "worker.txt", "reviewer.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("You are a helpful assistant."), 0644); err != nil {
			t.Fatal(err)
		}
	}

	peerID := int64(999)
	leadID := "lead-uuid"
	workerID := "worker-uuid"
	reviewerID := "reviewer-uuid"
	now := time.Now()

	dbStore := newSubAgentToolTestStore(t)

	// Simulate three-level chain saved before crash: lead → worker → reviewer
	sessions := []*store.AgentSessionData{
		{ID: leadID, ParentID: "", AgentName: "lead", PeerID: peerID,
			SystemPrompt: "You are the lead.", LastPrompt: "build the project",
			Status: "active", CreatedAt: now, UpdatedAt: now},
		{ID: workerID, ParentID: leadID, AgentName: "worker", PeerID: peerID,
			SystemPrompt: "You are the worker.", LastPrompt: "implement feature X",
			Status: "active", CreatedAt: now, UpdatedAt: now},
		{ID: reviewerID, ParentID: workerID, AgentName: "reviewer", PeerID: peerID,
			SystemPrompt: "You are the reviewer.", LastPrompt: "review the code",
			Status: "active", CreatedAt: now, UpdatedAt: now},
	}
	for _, sd := range sessions {
		if err := dbStore.SaveAgentSession(sd); err != nil {
			t.Fatalf("SaveAgentSession(%s): %v", sd.ID, err)
		}
	}
	if err := dbStore.SaveAgentChain(peerID, []string{leadID, workerID, reviewerID}); err != nil {
		t.Fatalf("SaveAgentChain: %v", err)
	}

	// Verify chain state before resume
	chain, err := dbStore.GetAgentChain(peerID)
	if err != nil || len(chain.Chain) != 3 {
		t.Fatalf("expected chain [lead, worker, reviewer], got %v (err=%v)", chain, err)
	}
	if chain.Chain[0] != leadID || chain.Chain[1] != workerID || chain.Chain[2] != reviewerID {
		t.Fatalf("wrong chain order: %v", chain.Chain)
	}

	// Mock LLM that returns different results per agent
	server := newMultiAgentMockLLM(t, map[string]string{
		"reviewer": "code reviewed, looks good",
		"worker":   "feature implemented and reviewed",
		"lead":     "project build complete with reviews",
	})
	defer server.Close()

	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models:  map[string]modelsconfig.ModelEntry{"test": {Name: "test-model", Host: server.URL}},
	})

	reg := tools.NewRegistry()
	reg.Register(&tools.FileReadTool{})
	reg.Register(&tools.TimeGetTool{})

	vkClient := NewStubVKClient(filepath.Join(t.TempDir(), "vk.log"))

	orchestrator := NewOrchestrator(OrchestratorConfig{
		ModelHolder:     modelHolder,
		MaxTokens:       8192,
		Temperature:     0.7,
		ToolRegistry:    reg,
		Debug:           false,
		SystemPromptDir: dir,
		Store:           dbStore,
		VKClient:        vkClient,
	})

	// Resume — simulates restart recovery
	if err := orchestrator.ResumeActiveChains(context.Background()); err != nil {
		t.Fatalf("ResumeActiveChains: %v", err)
	}

	// Verify all sessions cleaned up
	for _, id := range []string{leadID, workerID, reviewerID} {
		if sd, _ := dbStore.GetAgentSession(id); sd != nil {
			t.Errorf("expected session %s deleted after resume, still exists: %+v", id, sd)
		}
	}

	// Verify chain cleared
	chain, err = dbStore.GetAgentChain(peerID)
	if err != nil {
		t.Fatalf("GetAgentChain: %v", err)
	}
	if chain != nil && len(chain.Chain) != 0 {
		t.Errorf("expected empty chain after resume, got %v", chain.Chain)
	}

	// Verify result sent to user
	if !vkClient.Contains("Resumed result") {
		t.Errorf("expected final result sent to user, log: %v", vkClient.ReadLog())
	}
}

func TestThreeLevelChainResumePartialCrash(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"lead.txt", "worker.txt", "reviewer.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("You are a helpful assistant."), 0644); err != nil {
			t.Fatal(err)
		}
	}

	peerID := int64(888)
	leadID := "lead-uuid"
	workerID := "worker-uuid"
	reviewerID := "reviewer-uuid"
	now := time.Now()

	dbStore := newSubAgentToolTestStore(t)

	// All three sessions saved
	sessions := []*store.AgentSessionData{
		{ID: leadID, ParentID: "", AgentName: "lead", PeerID: peerID,
			SystemPrompt: "You are the lead.", LastPrompt: "build the project",
			Status: "active", CreatedAt: now, UpdatedAt: now},
		{ID: workerID, ParentID: leadID, AgentName: "worker", PeerID: peerID,
			SystemPrompt: "You are the worker.", LastPrompt: "implement feature X",
			Status: "active", CreatedAt: now, UpdatedAt: now},
		{ID: reviewerID, ParentID: workerID, AgentName: "reviewer", PeerID: peerID,
			SystemPrompt: "You are the reviewer.", LastPrompt: "review the code",
			Status: "active", CreatedAt: now, UpdatedAt: now},
	}
	for _, sd := range sessions {
		if err := dbStore.SaveAgentSession(sd); err != nil {
			t.Fatalf("SaveAgentSession(%s): %v", sd.ID, err)
		}
	}
	if err := dbStore.SaveAgentChain(peerID, []string{leadID, workerID, reviewerID}); err != nil {
		t.Fatalf("SaveAgentChain: %v", err)
	}

	// Simulate partial crash: reviewer session lost from DB (corrupted/deleted)
	if err := dbStore.DeleteAgentSession(reviewerID); err != nil {
		t.Fatalf("DeleteAgentSession(reviewer): %v", err)
	}

	server := newMultiAgentMockLLM(t, map[string]string{
		"worker": "feature implemented",
		"lead":   "project build complete",
	})
	defer server.Close()

	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models:  map[string]modelsconfig.ModelEntry{"test": {Name: "test-model", Host: server.URL}},
	})

	reg := tools.NewRegistry()
	reg.Register(&tools.FileReadTool{})
	reg.Register(&tools.TimeGetTool{})

	vkClient := NewStubVKClient(filepath.Join(t.TempDir(), "vk.log"))

	orchestrator := NewOrchestrator(OrchestratorConfig{
		ModelHolder:     modelHolder,
		MaxTokens:       8192,
		Temperature:     0.7,
		ToolRegistry:    reg,
		Debug:           false,
		SystemPromptDir: dir,
		Store:           dbStore,
		VKClient:        vkClient,
	})

	if err := orchestrator.ResumeActiveChains(context.Background()); err != nil {
		t.Fatalf("ResumeActiveChains: %v", err)
	}

	// Reviewer session was missing — should be skipped, chain trimmed to [lead, worker]
	// Then worker resumes (no child result since reviewer was missing), then lead resumes

	reviewerSess, _ := dbStore.GetAgentSession(reviewerID)
	if reviewerSess != nil {
		t.Error("expected reviewer session deleted (was already missing)")
	}

	chain, err := dbStore.GetAgentChain(peerID)
	if err != nil {
		t.Fatalf("GetAgentChain: %v", err)
	}
	if chain != nil && len(chain.Chain) != 0 {
		t.Errorf("expected empty chain after resume, got %v", chain.Chain)
	}
}

func newMultiAgentMockLLM(t *testing.T, responses map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)

		body, _ := io.ReadAll(r.Body)
		s := string(body)

		var content string
		for agent, resp := range responses {
			if strings.Contains(s, agent+".txt") || strings.Contains(s, "You are the "+agent) {
				content = resp
				break
			}
		}
		if content == "" {
			content = "done"
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", content)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "[DONE]\n")
	}))
}
