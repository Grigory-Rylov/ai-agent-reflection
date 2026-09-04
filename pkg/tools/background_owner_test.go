package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestBackgroundDeliveryRoutesToOwner(t *testing.T) {
	h := newTestHub(t, 4)
	h.SetDefaultPeer(1)

	var subTexts, mainTexts []string
	h.SetDelivery("sub-a", func(peerID int64, text string) { subTexts = append(subTexts, text) })
	h.SetDelivery("main", func(peerID int64, text string) { mainTexts = append(mainTexts, text) })

	id, err := h.StartFor("echo owner-bg", "owner-task", true, 0, "sub-a", "main")
	if err != nil {
		t.Fatalf("StartFor: %v", err)
	}
	if got := waitForStatus(h, id, "finished", 5*time.Second); got != "finished" {
		t.Fatalf("status = %q, want finished", got)
	}

	if len(subTexts) != 1 {
		t.Fatalf("sub delivery = %v, want exactly 1", subTexts)
	}
	if len(mainTexts) != 0 {
		t.Fatalf("main delivery = %v, want 0 (routed to owner)", mainTexts)
	}
	if !strings.Contains(subTexts[0], "owner-task") {
		t.Errorf("sub delivery = %q, want task name", subTexts[0])
	}
}

func TestBackgroundDeliveryFallbackToMain(t *testing.T) {
	h := newTestHub(t, 4)
	h.SetDefaultPeer(1)

	var mainTexts []string
	h.SetDelivery("main", func(peerID int64, text string) { mainTexts = append(mainTexts, text) })

	id, err := h.StartFor("echo dead-bg", "dead-task", true, 0, "dead-sub", "")
	if err != nil {
		t.Fatalf("StartFor: %v", err)
	}
	if got := waitForStatus(h, id, "finished", 5*time.Second); got != "finished" {
		t.Fatalf("status = %q, want finished", got)
	}

	if len(mainTexts) != 1 {
		t.Fatalf("main delivery = %v, want fallback to main", mainTexts)
	}
	if !strings.Contains(mainTexts[0], "dead-task") {
		t.Errorf("main delivery = %q, want task name", mainTexts[0])
	}
}

func TestBackgroundReleasePendingReroutes(t *testing.T) {
	h := newTestHub(t, 4)
	h.SetDefaultPeer(1)

	var subTexts, mainTexts []string
	h.SetDelivery("sub-a", func(peerID int64, text string) { subTexts = append(subTexts, text) })
	h.SetDelivery("main", func(peerID int64, text string) { mainTexts = append(mainTexts, text) })

	id, err := h.StartFor("sleep 30", "reroute-task", true, 0, "sub-a", "main")
	if err != nil {
		t.Fatalf("StartFor: %v", err)
	}
	if h.Owner(id) != "sub-a" {
		t.Fatalf("Owner = %q, want sub-a", h.Owner(id))
	}

	h.ReleasePending("sub-a")
	h.UnregisterDelivery("sub-a")
	if h.Owner(id) != "main" {
		t.Fatalf("Owner after release = %q, want main", h.Owner(id))
	}

	if err := h.Kill(id); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if got := waitForStatus(h, id, "killed", 5*time.Second); got != "killed" {
		t.Fatalf("status = %q, want killed", got)
	}

	if len(mainTexts) != 1 {
		t.Fatalf("main delivery = %v, want rerouted notification", mainTexts)
	}
	if len(subTexts) != 0 {
		t.Fatalf("sub delivery = %v, want 0 after release", subTexts)
	}
}

func TestShellBackgroundToolUsesContextOwner(t *testing.T) {
	h := newTestHub(t, 4)
	SetBackgroundHub(h)
	defer SetBackgroundHub(NewBackgroundHub(4))

	ctx := context.WithValue(context.Background(), BGOwnerContextKey, "sess-x")
	ctx = context.WithValue(ctx, BGParentOwnerContextKey, "sess-parent")

	tool := &ShellBackgroundTool{}
	res, err := tool.Execute(ctx, map[string]string{"command": "echo ctx-bg", "name": "ctx-task"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success: %s", res.Error)
	}
	id := res.Data.(map[string]interface{})["task_id"].(string)
	if h.Owner(id) != "sess-x" {
		t.Errorf("Owner = %q, want sess-x from context", h.Owner(id))
	}
}