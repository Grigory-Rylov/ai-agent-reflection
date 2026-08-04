package agentloop

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencode/llama-client/pkg/agentpolicy"
	"github.com/opencode/llama-client/pkg/modelsconfig"
	"github.com/opencode/llama-client/pkg/tools"
)

func TestRunAgentBeginEndRootSession(t *testing.T) {
	st := newSubAgentToolTestStore(t)
	orchestrator := NewOrchestrator(OrchestratorConfig{Store: st})
	peerID := int64(1)

	rootID := orchestrator.beginRootSession("lead", "lead system prompt", "build project", peerID)
	if rootID == "" {
		t.Fatal("expected non-empty root session ID")
	}

	sd, err := st.GetAgentSession(rootID)
	if err != nil || sd == nil {
		t.Fatalf("expected root session persisted, err=%v", err)
	}
	if sd.AgentName != "lead" || sd.Status != "active" || sd.LastPrompt != "build project" {
		t.Errorf("unexpected root session: %+v", sd)
	}

	chain, err := st.GetAgentChain(peerID)
	if err != nil || chain == nil || len(chain.Chain) != 1 || chain.Chain[0] != rootID {
		t.Errorf("expected chain [rootID], got %+v (err=%v)", chain, err)
	}

	orchestrator.endRootSession(peerID, rootID)

	if sd, _ := st.GetAgentSession(rootID); sd != nil {
		t.Error("expected root session deleted after end")
	}
	chain, _ = st.GetAgentChain(peerID)
	if chain != nil && len(chain.Chain) != 0 {
		t.Errorf("expected empty chain after end, got %+v", chain)
	}
}

func TestRunAgentNoStoreIsNoop(t *testing.T) {
	orchestrator := NewOrchestrator(OrchestratorConfig{})
	if id := orchestrator.beginRootSession("lead", "sys", "t", 1); id != "" {
		t.Errorf("expected empty root ID without store, got %q", id)
	}
	orchestrator.endRootSession(1, "") // must not panic
}

func TestRunAgentWiresRootIntoSubAgentTool(t *testing.T) {
	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models:  map[string]modelsconfig.ModelEntry{"test": {Name: "m", Host: "http://127.0.0.1:1"}},
	})
	am := agentpolicy.NewAgentManager()
	am.RegisterAgent(agentpolicy.AgentInfo{Name: "lead", Mode: agentpolicy.ModePrimary, Coordinator: true})

	orchestrator := NewOrchestrator(OrchestratorConfig{
		ModelHolder:  modelHolder,
		MaxTokens:    4096,
		Temperature:  0.7,
		ToolRegistry: tools.NewRegistry(),
		AgentManager: am,
	})

	a, err := orchestrator.makeSubAgent("lead", "lead prompt", 1)
	if err != nil {
		t.Fatalf("makeSubAgent: %v", err)
	}
	rootID := "root-uuid"
	tool, err := orchestrator.makeSubAgentTool("lead", a, 1, rootID, []string{rootID})
	if err != nil {
		t.Fatalf("makeSubAgentTool: %v", err)
	}

	if tool.ParentSessionID != rootID {
		t.Errorf("expected ParentSessionID %q, got %q", rootID, tool.ParentSessionID)
	}
	if tool.AgentSessionID != rootID {
		t.Errorf("expected AgentSessionID %q, got %q", rootID, tool.AgentSessionID)
	}
	if tool.ParentAgent == nil {
		t.Error("expected ParentAgent to be the owning agent (for parent history persistence)")
	}
	if len(tool.Chain) != 1 || tool.Chain[0] != rootID {
		t.Errorf("expected Chain [%q], got %v", rootID, tool.Chain)
	}
	if tool.CurrentDepth != 0 || tool.MaxDepth != 4 {
		t.Errorf("expected depth 0/max 4, got %d/%d", tool.CurrentDepth, tool.MaxDepth)
	}
}

// hangingWorkerLLM имитирует LLM, у которого первый запрос (лид) возвращает
// XML tool call на воркера, а все последующие (воркер) «зависают» до отмены
// контекста — так мы наблюдаем состояние БД посреди выполнения цепочки.
func hangingWorkerLLM(t *testing.T) (*httptest.Server, func() int32) {
	t.Helper()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if atomic.AddInt32(&calls, 1) == 1 {
			content := `<function=task><parameter=subagent_type>worker</parameter><parameter=prompt>implement feature</parameter></function>`
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", content)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "[DONE]\n")
			return
		}
		<-r.Context().Done()
	}))
	return server, func() int32 { return atomic.LoadInt32(&calls) }
}

// TestRunAgentPersistsRootChainWhileWorkerInFlight проверяет, что корневой агент
// (lead) персистится: пока воркер ждёт ответа LLM, в БД лежит цепочка
// [rootID, workerID] с ParentID воркера == rootID. После «краха» корень
// сохраняется и восстанавливается ResumeActiveChains, отдавая результат юзеру.
func TestRunAgentPersistsRootChainWhileWorkerInFlight(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"lead.txt", "worker.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("You are a helpful assistant."), 0644); err != nil {
			t.Fatal(err)
		}
	}

	server, callCount := hangingWorkerLLM(t)
	defer server.Close()

	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models:  map[string]modelsconfig.ModelEntry{"test": {Name: "test-model", Host: server.URL}},
	})

	am := agentpolicy.NewAgentManager()
	am.RegisterAgent(agentpolicy.AgentInfo{Name: "lead", Mode: agentpolicy.ModePrimary, Coordinator: true, Prompt: "lead prompt"})
	am.RegisterAgent(agentpolicy.AgentInfo{Name: "worker", Mode: agentpolicy.ModeSubagent, Leaf: true, Prompt: "worker prompt"})

	dbStore := newSubAgentToolTestStore(t)
	peerID := int64(777)

	reg := tools.NewRegistry()
	reg.Register(&tools.FileReadTool{})
	reg.Register(&tools.TimeGetTool{})

	orchestrator := NewOrchestrator(OrchestratorConfig{
		ModelHolder:     modelHolder,
		MaxTokens:       8192,
		Temperature:     0.7,
		ToolRegistry:    reg,
		AgentManager:    am,
		Debug:           false,
		SystemPromptDir: dir,
		Store:           dbStore,
	})

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_, _ = orchestrator.RunAgent(ctx, "lead", "build project", peerID)
	}()

	var rootID, workerID string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		chain, err := dbStore.GetAgentChain(peerID)
		if err == nil && chain != nil && len(chain.Chain) == 2 {
			rootID, workerID = chain.Chain[0], chain.Chain[1]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rootID == "" || workerID == "" {
		cancel()
		<-runDone
		t.Fatalf("expected chain [rootID, workerID] mid-flight, LLM calls=%d", callCount())
	}

	leadSess, err := dbStore.GetAgentSession(rootID)
	if err != nil || leadSess == nil {
		t.Fatalf("expected lead session persisted, err=%v", err)
	}
	if leadSess.AgentName != "lead" || leadSess.Status != "active" || leadSess.LastPrompt != "build project" {
		t.Errorf("unexpected lead session: %+v", leadSess)
	}

	workerSess, err := dbStore.GetAgentSession(workerID)
	if err != nil || workerSess == nil {
		t.Fatalf("expected worker session persisted, err=%v", err)
	}
	if workerSess.ParentID != rootID {
		t.Errorf("expected worker.ParentID == rootID, got %q", workerSess.ParentID)
	}
	chain, err := dbStore.GetAgentChain(peerID)
	if err != nil || len(chain.Chain) != 2 || chain.Chain[0] != rootID || chain.Chain[1] != workerID {
		t.Errorf("expected chain [rootID, workerID], got %+v (err=%v)", chain, err)
	}

	cancel()
	select {
	case <-runDone:
	case <-time.After(15 * time.Second):
		t.Fatal("RunAgent did not return after cancel")
	}

	if workerSess, _ := dbStore.GetAgentSession(workerID); workerSess != nil {
		t.Error("expected worker session cleaned up after error")
	}
	chain, err = dbStore.GetAgentChain(peerID)
	if err != nil || chain == nil || len(chain.Chain) != 1 || chain.Chain[0] != rootID {
		t.Errorf("expected chain [rootID] preserved for resume, got %+v (err=%v)", chain, err)
	}
	if leadSess, _ := dbStore.GetAgentSession(rootID); leadSess == nil || leadSess.Status != "active" {
		t.Errorf("expected lead session preserved for resume, got %+v", leadSess)
	}

	server2, _, _, _ := scriptedLLM(t)
	defer server2.Close()
	modelHolder2 := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models:  map[string]modelsconfig.ModelEntry{"test": {Name: "test-model", Host: server2.URL}},
	})
	vkClient := NewStubVKClient(filepath.Join(t.TempDir(), "vk.log"))
	resumeOrch := NewOrchestrator(OrchestratorConfig{
		ModelHolder:     modelHolder2,
		MaxTokens:       8192,
		Temperature:     0.7,
		ToolRegistry:    reg,
		AgentManager:    am,
		Debug:           false,
		SystemPromptDir: dir,
		Store:           dbStore,
		VKClient:        vkClient,
	})
	if err := resumeOrch.ResumeActiveChains(context.Background()); err != nil {
		t.Fatalf("ResumeActiveChains: %v", err)
	}
	if !vkClient.Contains("Resumed result") {
		t.Errorf("expected resumed result delivered to user, log: %v", vkClient.ReadLog())
	}
	chain, _ = dbStore.GetAgentChain(peerID)
	if chain != nil && len(chain.Chain) != 0 {
		t.Errorf("expected empty chain after resume, got %+v", chain)
	}
	if leadSess, _ := dbStore.GetAgentSession(rootID); leadSess != nil {
		t.Error("expected lead session deleted after resume")
	}
}
