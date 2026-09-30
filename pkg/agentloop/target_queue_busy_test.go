package agentloop

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestTargetQueueWaitsForActiveOutOfQueueInstance(t *testing.T) {
	recorder := newTargetRecorder()
	started := make(chan struct{})

	queue := NewTargetQueue(
		func(ctx context.Context, agentName, prompt string, peerID int64) (string, error) {
			close(started)
			return "resp", nil
		},
		recorder.deliver,
	)

	check := &manualBusyCheck{busy: true}
	queue.SetBusyCheck(check.busyCheck)
	queue.waitInterval = 2 * time.Millisecond

	queue.Submit("worker", "task", 1)

	select {
	case <-started:
		t.Fatal("runner started while out-of-queue instance was active")
	case <-time.After(50 * time.Millisecond):
	}

	check.setBusy(false)

	if !recorder.waitForDelivered(1, 3*time.Second) {
		t.Fatalf("timed out waiting for delivery, got %v", recorder.deliveredCopy())
	}
}

func TestTargetQueueProceedsAfterMaxBusyWait(t *testing.T) {
	recorder := newTargetRecorder()

	queue := NewTargetQueue(
		func(ctx context.Context, agentName, prompt string, peerID int64) (string, error) {
			return "resp", nil
		},
		recorder.deliver,
	)

	queue.SetBusyCheck(func(string) bool { return true })
	queue.waitInterval = time.Millisecond
	queue.maxBusyWait = 5 * time.Millisecond

	queue.Submit("worker", "task", 1)

	if !recorder.waitForDelivered(1, 3*time.Second) {
		t.Fatalf("runner never started after max busy wait, got %v", recorder.deliveredCopy())
	}
}

func TestTargetQueuePositionReflectsOutOfQueueInstance(t *testing.T) {
	queue := NewTargetQueue(nil, nil)
	queue.SetBusyCheck(func(name string) bool { return name == "worker" })

	if pos := queue.Submit("worker", "task", 1); pos != 1 {
		t.Errorf("position with active out-of-queue instance = %d, want 1", pos)
	}
	if pos := queue.Submit("explore", "task", 1); pos != 0 {
		t.Errorf("position for idle agent = %d, want 0", pos)
	}
}

type manualBusyCheck struct {
	mu   sync.Mutex
	busy bool
}

func (c *manualBusyCheck) busyCheck(string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.busy
}

func (c *manualBusyCheck) setBusy(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.busy = v
}
