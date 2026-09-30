package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSubagentWatcher struct {
	owner   string
	pending int
	onWait  func()
	mu      sync.Mutex
}

func (f *fakeSubagentWatcher) PendingSubagents(owner string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if owner != f.owner {
		return 0
	}
	return f.pending
}

func (f *fakeSubagentWatcher) WaitForSubagent(ctx context.Context, owner string, timeout time.Duration) bool {
	f.mu.Lock()
	onWait := f.onWait
	pending := f.pending
	match := owner == f.owner
	f.mu.Unlock()
	if !match || pending == 0 {
		return false
	}
	if onWait != nil {
		onWait()
	}
	return true
}

func TestAwaitSubagentResultCollectsAdmittedResult(t *testing.T) {
	config := DefaultConfig()
	config.BGOwner = "sess-hold-collect"
	a := NewAgent(config)
	s := a.GetSession(int64(77801))

	w := &fakeSubagentWatcher{owner: "sess-hold-collect", pending: 1}
	w.onWait = func() {
		s.GetPeerInput().Admit("[subagent worker job job-1] done: shipped")
		w.mu.Lock()
		w.pending = 0
		w.mu.Unlock()
	}
	a.config.SubagentWatch = w

	got := a.awaitSubagentResult(context.Background(), s)
	if len(got) != 1 || !strings.Contains(got[0], "shipped") {
		t.Fatalf("expected the admitted subagent result, got %v", got)
	}
}

func TestAwaitSubagentResultNoPendingReturnsNil(t *testing.T) {
	config := DefaultConfig()
	config.BGOwner = "sess-hold-none"
	a := NewAgent(config)
	s := a.GetSession(int64(77802))
	a.config.SubagentWatch = &fakeSubagentWatcher{owner: "sess-hold-none", pending: 0}

	if got := a.awaitSubagentResult(context.Background(), s); got != nil {
		t.Fatalf("expected nil when no subagents pending, got %v", got)
	}
}

func TestAwaitSubagentResultIgnoresOtherOwner(t *testing.T) {
	config := DefaultConfig()
	config.BGOwner = "sess-hold-mine"
	a := NewAgent(config)
	s := a.GetSession(int64(77803))
	a.config.SubagentWatch = &fakeSubagentWatcher{owner: "some-other-owner", pending: 3}

	if got := a.awaitSubagentResult(context.Background(), s); got != nil {
		t.Fatalf("expected nil when watcher reports another owner, got %v", got)
	}
}
