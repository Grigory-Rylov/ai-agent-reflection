package agentloop

import (
	"context"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
)

func TestResumeActiveChainsForPeerFiltersOtherPeers(t *testing.T) {
	dbStore := newSubAgentToolTestStore(t)
	peerA := int64(100)
	peerB := int64(200)

	now := time.Now()
	bSession := &store.AgentSessionData{
		ID: "b-agent", ParentID: "", AgentName: "worker", PeerID: peerB,
		SystemPrompt: "You are a helpful assistant.", LastPrompt: "do work",
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := dbStore.SaveAgentSession(bSession); err != nil {
		t.Fatalf("SaveAgentSession: %v", err)
	}
	if err := dbStore.SaveAgentChain(peerA, []string{"missing-a1", "missing-a2"}); err != nil {
		t.Fatalf("SaveAgentChain A: %v", err)
	}
	if err := dbStore.SaveAgentChain(peerB, []string{"b-agent"}); err != nil {
		t.Fatalf("SaveAgentChain B: %v", err)
	}

	orchestrator := NewOrchestrator(OrchestratorConfig{Store: dbStore})

	peers := orchestrator.ActiveChainPeers()
	if len(peers) != 2 {
		t.Fatalf("expected 2 chain peers, got %v", peers)
	}

	if err := orchestrator.ResumeActiveChainsForPeer(context.Background(), peerA); err != nil {
		t.Fatalf("ResumeActiveChainsForPeer(A): %v", err)
	}

	chainA, err := dbStore.GetAgentChain(peerA)
	if err != nil {
		t.Fatalf("GetAgentChain A: %v", err)
	}
	if chainA != nil && len(chainA.Chain) != 0 {
		t.Errorf("expected peer A chain trimmed, got %v", chainA.Chain)
	}

	chainB, err := dbStore.GetAgentChain(peerB)
	if err != nil {
		t.Fatalf("GetAgentChain B: %v", err)
	}
	if chainB == nil || len(chainB.Chain) != 1 {
		t.Errorf("BUG: peer B chain must stay untouched, got %+v", chainB)
	}
	bSess, _ := dbStore.GetAgentSession("b-agent")
	if bSess == nil {
		t.Error("BUG: peer B agent session must stay untouched")
	}
}
