package agentloop

import (
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

type bgThinkingVK struct {
	mockVKClient
}

func TestSubAgentToolDeliveryLifecycle(t *testing.T) {
	hub := tools.NewBackgroundHub(4)
	hub.SetLogDir(t.TempDir())
	tools.SetBackgroundHub(hub)
	defer tools.SetBackgroundHub(nil)

	vk := &bgThinkingVK{}
	tool := &SubAgentTool{
		PeerID:       1,
		MaxDepth:     4,
		VKClient:     vk,
		ThinkingPeerID: 99,
	}

	agent, err := tool.createAgent("worker", "system prompt", "the task")
	if err != nil {
		t.Fatalf("createAgent: %v", err)
	}
	sessionID := tool.AgentSessionID
	if sessionID == "" {
		t.Fatal("expected non-empty AgentSessionID")
	}

	if hub.Owner("nonexistent") != "" {
		t.Fatal("sanity: unknown task owner should be empty")
	}

	delivery := hub.DeliveryFor(sessionID)
	if delivery == nil {
		t.Fatal("expected delivery registered for sub-agent session")
	}

	delivery(1, "[BG] task x finished")
	sess := agent.GetSession(1)
	if in := sess.GetPeerInput(); in != nil && in.HasPending() {
		msgs := in.Drain()
		if len(msgs) != 1 || !strings.Contains(msgs[0], "[BG]") {
			t.Fatalf("sub-agent session pending = %v, want [BG] message", msgs)
		}
	} else {
		t.Fatal("expected [BG] message admitted into sub-agent session")
	}
	if got := vk.GetThinking(); len(got) != 1 || !strings.Contains(got[0], "[worker]") {
		t.Fatalf("thinking = %v, want [worker]-prefixed [BG] message", got)
	}

	tool.cleanupAgentSession()
	if hub.DeliveryFor(sessionID) != nil {
		t.Fatal("expected delivery unregistered after cleanup")
	}
	if in := sess.GetPeerInput(); in != nil && in.HasPending() {
		t.Fatal("expected no pending after drain")
	}
}