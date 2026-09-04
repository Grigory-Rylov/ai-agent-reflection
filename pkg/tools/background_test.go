package tools

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func newTestHub(t *testing.T, max int) *BackgroundHub {
	t.Helper()
	h := NewBackgroundHub(max)
	h.SetLogDir(t.TempDir())
	return h
}

func waitForStatus(h *BackgroundHub, id, want string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s := h.Status(id); s == want {
			return s
		}
		time.Sleep(20 * time.Millisecond)
	}
	return h.Status(id)
}

func TestBackgroundStartAndCheck(t *testing.T) {
	h := newTestHub(t, 4)

	id, err := h.Start("echo hello-bg", "demo", true, 1)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.Status(id) != "running" {
		t.Errorf("status = %q, want running", h.Status(id))
	}
	if got := waitForStatus(h, id, "finished", 5*time.Second); got != "finished" {
		t.Fatalf("status = %q, want finished", got)
	}
	out := h.Output(id, 20)
	if !strings.Contains(out, "hello-bg") {
		t.Errorf("output = %q, want to contain hello-bg", out)
	}
}

func TestBackgroundKill(t *testing.T) {
	h := newTestHub(t, 4)

	id, err := h.Start("sleep 30", "long", true, 1)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if h.Status(id) != "running" {
		t.Fatalf("status = %q, want running", h.Status(id))
	}
	if err := h.Kill(id); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if got := waitForStatus(h, id, "killed", 5*time.Second); got != "killed" {
		t.Fatalf("status = %q, want killed", got)
	}
}

func TestBackgroundLimit(t *testing.T) {
	h := newTestHub(t, 2)

	for i := 0; i < 2; i++ {
		if _, err := h.Start("sleep 30", "busy", true, 1); err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
	}
	_, err := h.Start("sleep 30", "overflow", true, 1)
	if err == nil {
		t.Fatal("expected error when limit reached")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error = %q, want mention of limit", err.Error())
	}
}

func TestBackgroundNotify(t *testing.T) {
	h := newTestHub(t, 4)
	var notified []string
	h.SetNotifyFunc(func(peerID int64, text string) {
		notified = append(notified, text)
	})

	id, err := h.Start("echo done-bg", "notify-task", true, 42)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(notified) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if len(notified) == 0 {
		t.Fatal("expected notification after finish")
	}
	if !strings.Contains(notified[0], "notify-task") {
		t.Errorf("notification = %q, want task name", notified[0])
	}
	if !strings.Contains(notified[0], "0") {
		t.Errorf("notification = %q, want exit code", notified[0])
	}
	_ = id
}

func TestBackgroundNotifyAdmitsToSession(t *testing.T) {
	h := newTestHub(t, 4)
	h.SetDefaultPeer(7)

	var pending []string
	var notifiedPeers []int64
	h.SetNotifyFunc(func(peerID int64, text string) {
		notifiedPeers = append(notifiedPeers, peerID)
		pending = append(pending, text)
	})

	id, err := h.Start("echo model-bg", "model-task", true, 0)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := waitForStatus(h, id, "finished", 5*time.Second); got != "finished" {
		t.Fatalf("status = %q, want finished", got)
	}

	if len(pending) != 1 {
		t.Fatalf("pending = %v, want exactly 1 admitted message", pending)
	}
	if len(notifiedPeers) != 1 || notifiedPeers[0] != 7 {
		t.Fatalf("notifiedPeers = %v, want default peer 7", notifiedPeers)
	}
	if !strings.Contains(pending[0], "model-task") {
		t.Errorf("pending = %q, want task name", pending[0])
	}
	if !strings.Contains(pending[0], "exit 0") {
		t.Errorf("pending = %q, want exit code", pending[0])
	}
	if !strings.Contains(pending[0], id) {
		t.Errorf("pending = %q, want task_id so the model can call shell_check", pending[0])
	}
}

func TestBackgroundNoNotify(t *testing.T) {
	h := newTestHub(t, 4)
	notifyCount := 0
	h.SetNotifyFunc(func(peerID int64, text string) { notifyCount++ })

	id, err := h.Start("echo quiet", "quiet-task", false, 1)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := waitForStatus(h, id, "finished", 5*time.Second); got != "finished" {
		t.Fatalf("status = %q, want finished", got)
	}
	time.Sleep(50 * time.Millisecond)
	if notifyCount != 0 {
		t.Errorf("notifyCount = %d, want 0", notifyCount)
	}
}

func TestBackgroundOutputTail(t *testing.T) {
	h := newTestHub(t, 4)

	id, err := h.Start("for i in 1 2 3 4 5; do echo line$i; done", "tail", true, 1)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := waitForStatus(h, id, "finished", 5*time.Second); got != "finished" {
		t.Fatalf("status = %q, want finished", got)
	}
	out := h.Output(id, 2)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Errorf("tail output lines = %d, want 2: %q", len(lines), out)
	}
	if !strings.Contains(out, "line5") {
		t.Errorf("tail output = %q, want line5", out)
	}
}

func TestBackgroundStatusUnknown(t *testing.T) {
	h := newTestHub(t, 4)
	if s := h.Status("nope"); s != "unknown" {
		t.Errorf("status = %q, want unknown", s)
	}
	if err := h.Kill("nope"); err == nil {
		t.Error("expected error for unknown task")
	}
}

func TestShellBackgroundToolExecute(t *testing.T) {
	SetBackgroundHub(newTestHub(t, 4))
	defer SetBackgroundHub(NewBackgroundHub(4))

	tool := &ShellBackgroundTool{}
	res, err := tool.Execute(nil, map[string]string{"command": "echo tool-bg", "name": "t1", "notify": "true"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success: %s", res.Error)
	}
	data := res.Data.(map[string]interface{})
	id := data["task_id"].(string)
	if id == "" {
		t.Fatal("expected task_id")
	}
}

func TestShellCheckToolExecute(t *testing.T) {
	h := newTestHub(t, 4)
	SetBackgroundHub(h)
	defer SetBackgroundHub(NewBackgroundHub(4))

	id, _ := h.Start("echo check-bg", "chk", true, 1)
	waitForStatus(h, id, "finished", 5*time.Second)

	tool := &ShellCheckTool{}
	res, err := tool.Execute(nil, map[string]string{"task_id": id, "tail": "5"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success: %s", res.Error)
	}
	text := res.Data.(map[string]interface{})["output"].(string)
	if !strings.Contains(text, "check-bg") {
		t.Errorf("output = %q, want check-bg", text)
	}
}
func TestBackgroundNotifyExactTextAndToolFlow(t *testing.T) {
	h := newTestHub(t, 4)
	h.SetDefaultPeer(7)
	SetBackgroundHub(h)
	defer SetBackgroundHub(NewBackgroundHub(4))

	var gotPeer int64
	var gotText string
	h.SetNotifyFunc(func(peerID int64, text string) {
		gotPeer = peerID
		gotText = text
	})

	bgTool := &ShellBackgroundTool{}
	res, err := bgTool.Execute(nil, map[string]string{
		"command": "sleep 15 && echo done-qa-test1",
		"name":    "qa-bg-15s",
		"notify":  "true",
	})
	if err != nil {
		t.Fatalf("shell_background Execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("shell_background failed: %s", res.Error)
	}
	id := res.Data.(map[string]interface{})["task_id"].(string)
	if id == "" {
		t.Fatal("expected task_id")
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && gotText == "" {
		time.Sleep(100 * time.Millisecond)
	}
	if gotText == "" {
		t.Fatal("expected [BG] notification after task finish")
	}
	want := fmt.Sprintf("[BG] task qa-bg-15s (id %s) finished (exit 0, 15s) — details via shell_check", id)
	if gotText != want {
		t.Errorf("notification text:\n got: %q\nwant: %q", gotText, want)
	}
	if gotPeer != 7 {
		t.Errorf("peer = %d, want default peer 7", gotPeer)
	}

	checkTool := &ShellCheckTool{}
	cres, err := checkTool.Execute(nil, map[string]string{"task_id": id, "tail": "5"})
	if err != nil {
		t.Fatalf("shell_check Execute: %v", err)
	}
	if !cres.Success {
		t.Fatalf("shell_check failed: %s", cres.Error)
	}
	data := cres.Data.(map[string]interface{})
	if data["status"] != "finished" {
		t.Errorf("status = %v, want finished", data["status"])
	}
	if !strings.Contains(data["output"].(string), "done-qa-test1") {
		t.Errorf("output = %q, want done-qa-test1", data["output"])
	}
}
