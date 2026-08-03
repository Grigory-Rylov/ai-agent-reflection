package agentloop

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opencode/llama-client/pkg/modelsconfig"
	"github.com/opencode/llama-client/pkg/store"
	"github.com/opencode/llama-client/pkg/tools"
)

func TestOrchestratorResumeActiveChains(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"worker.txt", "qa.txt", "coordinator.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("You are a helpful assistant."), 0644); err != nil {
			t.Fatal(err)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Resumed result\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: server.URL},
		},
	})

	dbStore := newSubAgentToolTestStore(t)
	peerID := int64(12345)

	workerID := "worker-uuid"
	reviewerID := "reviewer-uuid"
	now := time.Now()
	for _, sd := range []*store.AgentSessionData{
		{ID: workerID, ParentID: "", AgentName: "worker", PeerID: peerID,
			SystemPrompt: "You are a helpful assistant.", LastPrompt: "implement feature",
			Status: "active", CreatedAt: now, UpdatedAt: now},
		{ID: reviewerID, ParentID: workerID, AgentName: "reviewer", PeerID: peerID,
			SystemPrompt: "You are a helpful assistant.", LastPrompt: "review the code",
			Status: "active", CreatedAt: now, UpdatedAt: now},
	} {
		if err := dbStore.SaveAgentSession(sd); err != nil {
			t.Fatalf("SaveAgentSession: %v", err)
		}
	}
	if err := dbStore.SaveAgentChain(peerID, []string{workerID, reviewerID}); err != nil {
		t.Fatalf("SaveAgentChain: %v", err)
	}

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

	workerSess, _ := dbStore.GetAgentSession(workerID)
	if workerSess != nil {
		t.Error("expected worker session deleted after resume")
	}
	reviewerSess, _ := dbStore.GetAgentSession(reviewerID)
	if reviewerSess != nil {
		t.Error("expected reviewer session deleted after resume")
	}

	chain, err := dbStore.GetAgentChain(peerID)
	if err != nil {
		t.Fatalf("GetAgentChain: %v", err)
	}
	if chain != nil && len(chain.Chain) != 0 {
		t.Errorf("expected empty chain after resume, got %v", chain.Chain)
	}

	if !vkClient.Contains("Resumed result") {
		t.Errorf("expected final result sent to user, log: %v", vkClient.ReadLog())
	}
}

func TestOrchestratorResumeActiveChainsNoStore(t *testing.T) {
	orchestrator := NewOrchestrator(OrchestratorConfig{})
	if err := orchestrator.ResumeActiveChains(context.Background()); err != nil {
		t.Fatalf("ResumeActiveChains with nil store should be no-op, got %v", err)
	}
}
