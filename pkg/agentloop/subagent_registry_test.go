package agentloop

import (
	"context"
	"testing"
	"time"
)

func TestSubagentRegistryRegisterListFinish(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := subagentRegistry.register("worker", 42, 1, cancel)
	defer subagentRegistry.finish(id)

	found := false
	for _, s := range ListSubagentSpawns() {
		if s.ID != id {
			continue
		}
		found = true
		if s.AgentName != "worker" || s.PeerID != 42 || s.Depth != 1 {
			t.Errorf("spawn meta wrong: %+v", s)
		}
		if s.Status != SubagentRunning {
			t.Errorf("status = %v, want running", s.Status)
		}
	}
	if !found {
		t.Fatalf("registered spawn not present in list")
	}

	subagentRegistry.finish(id)
	for _, s := range ListSubagentSpawns() {
		if s.ID == id {
			t.Errorf("spawn still listed after finish")
		}
	}
}

func TestSubagentRegistryCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	id := subagentRegistry.register("qa", 7, 2, cancel)
	defer subagentRegistry.finish(id)

	if !CancelSubagentSpawn(id) {
		t.Fatalf("cancel returned false for active spawn")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatalf("spawn context was not cancelled")
	}
	if CancelSubagentSpawn("sp-nonexistent") {
		t.Errorf("cancel of unknown id should return false")
	}
}
