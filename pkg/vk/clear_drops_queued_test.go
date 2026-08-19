package vk

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)


type countingMockAgentLoop struct {
	*mockAgentLoop
	calls int64 
}

func newCountingMockAgentLoop() *countingMockAgentLoop {
	return &countingMockAgentLoop{mockAgentLoop: newMockAgentLoop()}
}

func (m *countingMockAgentLoop) ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error) {
	atomic.AddInt64(&m.calls, 1)
	return m.mockAgentLoop.ProcessMessage(ctx, prompt, peerID)
}


func waitForWaiting(t *testing.T, h *BotHandler, peerID int64, expected int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if n := h.waitingMessages(peerID); n >= expected {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for peer %d to have %d message(s) in queue, got %d",
		peerID, expected, h.waitingMessages(peerID))
}


func runStaleQueueScenario(t *testing.T, resetCommand string, peerID int64) {
	t.Helper()

	log, _ := logger.New(logger.DefaultConfig())
	mock := newCountingMockAgentLoop()
	mock.blockCh = make(chan struct{})

	handler := NewBotHandler(nil, mock, log)

	
	m1 := make(chan string, 1)
	go func() { m1 <- handler.ProcessMessage("долгая задача", peerID) }()
	time.Sleep(100 * time.Millisecond) 

	
	m2 := make(chan string, 1)
	go func() { m2 <- handler.ProcessMessage("старое сообщение", peerID) }()
	waitForWaiting(t, handler, peerID, 1) 

	
	if res := handler.ProcessMessage(resetCommand, peerID); res == "" {
		t.Fatalf("command %q returned empty result", resetCommand)
	}

	select {
	case r := <-m1:
		if r != "" {
			t.Errorf("M1 after cancel: expected \"\", got %q", r)
		}
	case <-time.After(3 * time.Second):
		close(mock.blockCh)
		t.Fatal("M1 did not finish within 3s after session reset")
	}

	select {
	case r := <-m2:
		if r != "" {
			t.Errorf("stale M2 must be dropped silently, got %q", r)
		}
	case <-time.After(3 * time.Second):
		close(mock.blockCh)
		t.Fatal("M2 did not finish within 3s after session reset")
	}

	if !mock.cancelled {
		t.Error("M1 context was not cancelled by the session reset command")
	}
	if got := atomic.LoadInt64(&mock.calls); got != 1 {
		t.Fatalf("agent must be called exactly once (only M1), got %d — stale queued message leaked into new session", got)
	}

	close(mock.blockCh) 
}

func TestClearDropsQueuedMessages(t *testing.T) {
	runStaleQueueScenario(t, "/clear", 90101)
}

func TestNewSessionDropsQueuedMessages(t *testing.T) {
	dir := t.TempDir()
	runStaleQueueScenario(t, "/n "+dir, 90102)
}
