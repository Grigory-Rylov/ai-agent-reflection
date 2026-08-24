package agentloop

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type targetRecorder struct {
	mu        sync.Mutex
	order     []string
	inflight  map[string]int
	maxInfl   int
	delivered []string
}

func newTargetRecorder() *targetRecorder {
	return &targetRecorder{inflight: make(map[string]int)}
}

func (r *targetRecorder) deliver(agentName string, peerID int64, resp string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := agentName
	if err != nil {
		key += "!ERR:" + err.Error()
	}
	r.delivered = append(r.delivered, key)
}

func (r *targetRecorder) waitForDelivered(count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := len(r.delivered)
		r.mu.Unlock()
		if n >= count {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func (r *targetRecorder) waitForDeliveryOf(prefix string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, d := range r.deliveredCopy() {
			if strings.HasPrefix(d, prefix) {
				return true
			}
		}
		time.Sleep(time.Millisecond)
	}
	return false
}

func (r *targetRecorder) orderCopy() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

func (r *targetRecorder) deliveredCopy() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.delivered))
	copy(out, r.delivered)
	return out
}

func (r *targetRecorder) snapshotInflight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxInfl
}

func TestTargetQueueSerializationSameName(t *testing.T) {
	recorder := newTargetRecorder()
	gated := make(chan struct{})

	queue := NewTargetQueue(
		func(ctx context.Context, agentName, prompt string, peerID int64) (string, error) {
			recordInflight(recorder, agentName, 1)
			recordOrder(recorder, agentName, prompt)
			<-gated
			recordInflight(recorder, agentName, -1)
			return "resp-" + agentName, nil
		},
		recorder.deliver,
	)

	prompts := []string{"a", "b", "c"}
	for _, p := range prompts {
		queue.Submit("worker", p, 1)
	}

	close(gated)
	if !recorder.waitForDelivered(len(prompts), 2*time.Second) {
		t.Fatalf("timed out waiting for delivery, got %v", recorder.deliveredCopy())
	}

	want := []string{"worker@a", "worker@b", "worker@c"}
	gotOrder := recorder.orderCopy()
	if len(gotOrder) != len(want) {
		t.Fatalf("runner order length = %d, want %d (%v)", len(gotOrder), len(want), gotOrder)
	}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Errorf("runner order[%d] = %q, want %q", i, gotOrder[i], want[i])
		}
	}
	if maxInfl := recorder.snapshotInflight(); maxInfl > 1 {
		t.Errorf("max concurrent runners for same name = %d, want <= 1", maxInfl)
	}
}

func TestTargetQueuePositionsIncreaseWhileBusy(t *testing.T) {
	gated := make(chan struct{})

	queue := NewTargetQueue(
		func(ctx context.Context, agentName, prompt string, peerID int64) (string, error) {
			<-gated
			return "resp", nil
		},
		nil,
	)

	first := queue.Submit("worker", "one", 1)
	second := queue.Submit("worker", "two", 1)
	third := queue.Submit("worker", "three", 1)

	if first != 0 {
		t.Errorf("first submit position = %d, want 0", first)
	}
	if second != 1 {
		t.Errorf("second submit position = %d, want 1", second)
	}
	if third != 2 {
		t.Errorf("third submit position = %d, want 2", third)
	}

	close(gated)
}

func TestTargetQueueIndependentLanes(t *testing.T) {
	recorder := newTargetRecorder()

	queue := NewTargetQueue(
		func(ctx context.Context, agentName, prompt string, peerID int64) (string, error) {
			time.Sleep(5 * time.Millisecond)
			return "resp-" + agentName, nil
		},
		recorder.deliver,
	)

	queue.Submit("alpha", "x", 1)
	queue.Submit("beta", "y", 2)

	if !recorder.waitForDeliveryOf("alpha", 2*time.Second) {
		t.Errorf("lane alpha did not deliver, got %v", recorder.deliveredCopy())
	}
	if !recorder.waitForDeliveryOf("beta", 2*time.Second) {
		t.Errorf("lane beta did not deliver, got %v", recorder.deliveredCopy())
	}
}

func TestTargetQueueNilCallbacksSafe(t *testing.T) {
	queue := NewTargetQueue(nil, nil)
	pos := queue.Submit("solo", "hello", 7)
	if pos != 0 {
		t.Errorf("submit position = %d, want 0", pos)
	}
	time.Sleep(10 * time.Millisecond)
}

func TestTargetQueueDeliversError(t *testing.T) {
	failErr := errors.New("boom")
	var mu sync.Mutex
	var capturedErr error
	queue := NewTargetQueue(
		func(ctx context.Context, agentName, prompt string, peerID int64) (string, error) {
			return "", failErr
		},
		func(agentName string, peerID int64, resp string, err error) {
			mu.Lock()
			capturedErr = err
			mu.Unlock()
		},
	)
	queue.Submit("broken", "anything", 1)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := capturedErr
		mu.Unlock()
		if got == failErr {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Errorf("error was not delivered")
}

func recordInflight(rec *targetRecorder, agentName string, delta int) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.inflight[agentName] += delta
	if rec.inflight[agentName] > rec.maxInfl {
		rec.maxInfl = rec.inflight[agentName]
	}
}

func recordOrder(rec *targetRecorder, agentName, prompt string) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.order = append(rec.order, agentName+"@"+prompt)
}
