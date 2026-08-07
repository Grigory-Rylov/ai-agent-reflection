package agentloop

import (
	"context"
	"testing"

	"github.com/opencode/llama-client/pkg/modelsconfig"
	"github.com/opencode/llama-client/pkg/store"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

// TestOrchestratorLeafSessionPersistsAndCleans проверяет, что beginLeafSession
// персистит сессию worker/qa и цепочку, а endLeafSession — удаляет их после
// успешного завершения.
func TestOrchestratorLeafSessionPersistsAndCleans(t *testing.T) {
	st := newSubAgentToolTestStore(t)
	orchestrator := NewOrchestrator(OrchestratorConfig{Store: st})
	peerID := int64(55)

	orchestrator.beginLeafSession("worker", "worker prompt", "do the work", peerID, "leaf-1")

	sd, err := st.GetAgentSession("leaf-1")
	if err != nil || sd == nil {
		t.Fatalf("expected leaf session persisted, err=%v", err)
	}
	if sd.AgentName != "worker" || sd.Status != "active" || sd.LastPrompt != "do the work" {
		t.Errorf("unexpected leaf session: %+v", sd)
	}
	chain, err := st.GetAgentChain(peerID)
	if err != nil || chain == nil || len(chain.Chain) != 1 || chain.Chain[0] != "leaf-1" {
		t.Errorf("expected chain [leaf-1], got %+v (err=%v)", chain, err)
	}

	orchestrator.endLeafSession(peerID, "leaf-1")

	if sd, _ := st.GetAgentSession("leaf-1"); sd != nil {
		t.Error("expected leaf session deleted after end")
	}
	chain, _ = st.GetAgentChain(peerID)
	if chain != nil && len(chain.Chain) != 0 {
		t.Errorf("expected empty chain after end, got %+v", chain)
	}
}

// TestOrchestratorSaveAgentHistory проверяет, что saveAgentHistory персистит
// историю сообщений сессии в БД, чтобы ResumeActiveChains восстановил контекст.
func TestOrchestratorSaveAgentHistory(t *testing.T) {
	st := newSubAgentToolTestStore(t)
	server, _, _, _ := scriptedLLM(t)
	defer server.Close()

	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models:  map[string]modelsconfig.ModelEntry{"test": {Name: "test-model", Host: server.URL}},
	})

	orchestrator := NewOrchestrator(OrchestratorConfig{
		ModelHolder:  modelHolder,
		MaxTokens:    4096,
		Temperature:  0.7,
		ToolRegistry: tools.NewRegistry(),
		Store:        st,
	})

	peerID := int64(66)
	a, sessionID, err := orchestrator.makeSubAgent("worker", "worker prompt", peerID)
	if err != nil {
		t.Fatalf("makeSubAgent: %v", err)
	}
	if err := st.SaveAgentSession(&store.AgentSessionData{
		ID: sessionID, AgentName: "worker", PeerID: peerID, SystemPrompt: "worker prompt", LastPrompt: "old", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}

	sess := a.GetSession(peerID)
	sess.AddUserMessage("hello")
	sess.AddAssistantMessage("hi")

	orchestrator.saveAgentHistory(a, sessionID, peerID, "do the work")

	sd, err := st.GetAgentSession(sessionID)
	if err != nil || sd == nil {
		t.Fatalf("expected session persisted, err=%v", err)
	}
	if sd.LastPrompt != "do the work" {
		t.Errorf("expected LastPrompt %q, got %q", "do the work", sd.LastPrompt)
	}
	if sd.Messages == "" {
		t.Error("expected messages JSON to be persisted")
	}
}

// TestAgentLoopResumeInterruptedTask проверяет, что ResumeInterruptedTask
// продолжает незавершённую задачу главного агента и снимает resume_prompt.
func TestAgentLoopResumeInterruptedTask(t *testing.T) {
	st := newSubAgentToolTestStore(t)
	peerID := int64(1)

	if err := st.SaveSession(&store.SessionData{
		PeerID:       peerID,
		ResumePrompt: "build feature",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(peerID, store.MessageData{PeerID: peerID, Role: "user", Content: "build feature"}); err != nil {
		t.Fatal(err)
	}

	server, _, _, chatCount := scriptedLLM(t)
	defer server.Close()

	loop, err := NewAgentLoop(LoopConfig{
		ModelHolder:       modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{Default: "test", Models: map[string]modelsconfig.ModelEntry{"test": {Name: "test-model", Host: server.URL}}}),
		SessionConfig:     session.Config{PeerID: peerID, SessionID: "main-s1", AutoSave: true, Store: st},
		EnableTools:       false,
		EnableCompression: false,
		MaxTokens:         4096,
		Temperature:       0.7,
	}, &mockVKClient{}, newMockToolRegistry())
	if err != nil {
		t.Fatal(err)
	}

	sess := loop.EnsureSession(peerID)
	if sess.GetResumePrompt() != "build feature" {
		t.Fatalf("expected resume_prompt loaded from store, got %q", sess.GetResumePrompt())
	}

	loop.ResumeInterruptedTask(context.Background(), peerID)

	if chatCount() == 0 {
		t.Fatal("expected a continuation LLM call after resume")
	}
	if got := sess.GetResumePrompt(); got != "" {
		t.Errorf("expected resume_prompt cleared after resume, got %q", got)
	}
	sd, _ := st.GetSession(peerID)
	if sd == nil || sd.ResumePrompt != "" {
		t.Errorf("expected resume_prompt cleared in store, got %+v", sd)
	}
}

// TestAgentLoopResumeInterruptedTaskNoop проверяет, что без resume_prompt
// продолжение не запускается.
func TestAgentLoopResumeInterruptedTaskNoop(t *testing.T) {
	server, _, _, chatCount := scriptedLLM(t)
	defer server.Close()

	loop, err := NewAgentLoop(LoopConfig{
		ModelHolder:   modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{Default: "test", Models: map[string]modelsconfig.ModelEntry{"test": {Name: "test-model", Host: server.URL}}}),
		SessionConfig: session.Config{PeerID: 7, SessionID: "main-s2", AutoSave: false},
		EnableTools:   false,
	}, &mockVKClient{}, newMockToolRegistry())
	if err != nil {
		t.Fatal(err)
	}

	// NewAgentLoop инициализирует токенизатор и обращается к серверу,
	// поэтому фиксируем счётчик вызовов после создания цикла.
	before := chatCount()
	loop.EnsureSession(7)
	loop.ResumeInterruptedTask(context.Background(), 7)

	if after := chatCount(); after != before {
		t.Errorf("expected no LLM calls without resume_prompt, got %d -> %d", before, after)
	}
}
