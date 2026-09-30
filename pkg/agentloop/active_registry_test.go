package agentloop

import "testing"

func TestAgentActivityTracksEnterAndExit(t *testing.T) {
	if AgentCurrentlyActive("worker") {
		t.Fatal("worker should be idle initially")
	}

	leave := MarkAgentActive("worker")
	if !AgentCurrentlyActive("worker") {
		t.Fatal("worker should be active inside scope")
	}

	leave()
	if AgentCurrentlyActive("worker") {
		t.Fatal("worker should be idle after leave")
	}
}

func TestAgentActivityNestedScopes(t *testing.T) {
	outer := MarkAgentActive("reviewer")
	inner := MarkAgentActive("reviewer")

	inner()
	if !AgentCurrentlyActive("reviewer") {
		t.Fatal("reviewer should stay active while outer scope is open")
	}

	outer()
	if AgentCurrentlyActive("reviewer") {
		t.Fatal("reviewer should be idle after both scopes close")
	}
}

func TestAgentActivityDoubleLeaveIsSafe(t *testing.T) {
	leave := MarkAgentActive("qa")
	leave()
	leave()
	if AgentCurrentlyActive("qa") {
		t.Fatal("qa should be idle")
	}
	if got := globalAgentActivity.snapshot()["qa"]; got != 0 {
		t.Errorf("counter for qa = %d, want 0", got)
	}
}

func TestAgentActivityEmptyNameIsNoop(t *testing.T) {
	leave := MarkAgentActive("")
	if AgentCurrentlyActive("") {
		t.Fatal("empty name should never be active")
	}
	leave()
}
