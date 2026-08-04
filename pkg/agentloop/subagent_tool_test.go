package agentloop

import (
	"path/filepath"
	"testing"

	"github.com/opencode/llama-client/pkg/store"
)

func newSubAgentToolTestStore(t *testing.T) store.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store.NewStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSubAgentToolCreatePersistsTask(t *testing.T) {
	st := newSubAgentToolTestStore(t)

	tool := &SubAgentTool{
		PeerID:   1,
		Store:    st,
		Chain:    []string{},
		MaxDepth: 4,
	}

	agent, _ := tool.createAgent("worker", "system prompt", "the task")
	_ = agent

	sd, err := st.GetAgentSession(tool.AgentSessionID)
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	if sd == nil {
		t.Fatal("expected session row after createAgent")
	}
	if sd.LastPrompt != "the task" {
		t.Errorf("expected LastPrompt %q, got %q", "the task", sd.LastPrompt)
	}
	if sd.AgentName != "worker" {
		t.Errorf("expected AgentName worker, got %q", sd.AgentName)
	}
	if sd.Status != "active" {
		t.Errorf("expected status active, got %q", sd.Status)
	}
}

func TestSubAgentToolSaveSessionPersistsMessages(t *testing.T) {
	st := newSubAgentToolTestStore(t)

	tool := &SubAgentTool{
		PeerID:   1,
		Store:    st,
		MaxDepth: 4,
	}

	agent, _ := tool.createAgent("worker", "system prompt", "the task")
	sess := agent.GetSession(1)
	sess.AddUserMessage("hello")
	sess.AddAssistantMessage("hi")

	tool.saveSessionHistory(agent, tool.AgentSessionID, "the task")

	sd, err := st.GetAgentSession(tool.AgentSessionID)
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	if sd == nil {
		t.Fatal("expected session row after save")
	}
	if sd.Messages == "" {
		t.Fatal("expected messages JSON to be persisted")
	}
	if sd.LastPrompt != "the task" {
		t.Errorf("expected LastPrompt %q, got %q", "the task", sd.LastPrompt)
	}
}

func TestSubAgentToolSaveParentHistoryPreservesLastPrompt(t *testing.T) {
	st := newSubAgentToolTestStore(t)
	peerID := int64(1)

	parent := &SubAgentTool{
		PeerID:   peerID,
		Store:    st,
		MaxDepth: 4,
	}
	parentAgent, _ := parent.createAgent("worker", "system prompt", "parent task")
	parentID := parent.AgentSessionID

	child := &SubAgentTool{
		PeerID:          peerID,
		Store:           st,
		MaxDepth:        4,
		ParentSessionID: parentID,
		ParentAgent:     parentAgent,
		Chain:           []string{parentID},
	}
	childAgent, _ := child.createAgent("reviewer", "system prompt", "child task")
	childAgent.GetSession(peerID).AddUserMessage("child work")
	childAgent.GetSession(peerID).AddAssistantMessage("child result")

	child.saveParentHistory()

	parentSess, err := st.GetAgentSession(parentID)
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	if parentSess == nil {
		t.Fatal("expected parent session")
	}
	if parentSess.LastPrompt != "parent task" {
		t.Errorf("expected LastPrompt %q preserved, got %q", "parent task", parentSess.LastPrompt)
	}
	if parentSess.Messages == "" {
		t.Error("expected parent messages to be persisted")
	}
}

func TestSubAgentToolCompleteDeletesAndPopsChain(t *testing.T) {
	st := newSubAgentToolTestStore(t)
	peerID := int64(1)

	if err := st.SaveAgentChain(peerID, []string{"parent"}); err != nil {
		t.Fatalf("SaveAgentChain: %v", err)
	}

	tool := &SubAgentTool{
		PeerID:   peerID,
		Store:    st,
		Chain:    []string{"parent"},
		MaxDepth: 4,
	}

	_, _ = tool.createAgent("worker", "system prompt", "the task")
	childID := tool.AgentSessionID

	tool.completeAgentSession()

	sd, err := st.GetAgentSession(childID)
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	if sd != nil {
		t.Error("expected session row deleted after complete")
	}

	chain, err := st.GetAgentChain(peerID)
	if err != nil {
		t.Fatalf("GetAgentChain: %v", err)
	}
	if chain == nil || len(chain.Chain) != 1 || chain.Chain[0] != "parent" {
		t.Errorf("expected chain [parent], got %+v", chain)
	}
}

func TestSubAgentToolCancelDeletesAndPopsChain(t *testing.T) {
	st := newSubAgentToolTestStore(t)
	peerID := int64(1)

	if err := st.SaveAgentChain(peerID, []string{"parent"}); err != nil {
		t.Fatalf("SaveAgentChain: %v", err)
	}

	tool := &SubAgentTool{
		PeerID:   peerID,
		Store:    st,
		Chain:    []string{"parent"},
		MaxDepth: 4,
	}

	_, _ = tool.createAgent("worker", "system prompt", "the task")
	childID := tool.AgentSessionID

	tool.cancelAgentSession()

	sd, err := st.GetAgentSession(childID)
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	if sd != nil {
		t.Error("expected session row deleted after cancel")
	}

	chain, err := st.GetAgentChain(peerID)
	if err != nil {
		t.Fatalf("GetAgentChain: %v", err)
	}
	if chain == nil || len(chain.Chain) != 1 || chain.Chain[0] != "parent" {
		t.Errorf("expected chain [parent], got %+v", chain)
	}
}
