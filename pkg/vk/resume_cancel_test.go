package vk

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

type resumeCapturingLoop struct {
	*mockAgentLoop
	mu          sync.Mutex
	resumeCtx   context.Context
	started     chan struct{}
	resumeCalls int
}

func (m *resumeCapturingLoop) ResumeInterruptedTask(ctx context.Context, peerID int64) {
	m.mu.Lock()
	m.resumeCtx = ctx
	m.resumeCalls++
	m.mu.Unlock()
	close(m.started)
	<-ctx.Done()
}

func (m *resumeCapturingLoop) resumeContextDone() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resumeCtx != nil && m.resumeCtx.Err() != nil
}

func waitForChannel(t *testing.T, ch chan struct{}, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal(msg)
	}
}

func TestClearCancelsScheduledResumeTurn(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	loop := &resumeCapturingLoop{
		mockAgentLoop: newMockAgentLoop(),
		started:       make(chan struct{}),
	}
	handler := NewBotHandlerWithPeerID(nil, loop, log, 0, 0, nil, nil)

	peerID := int64(888)
	handler.ScheduleResume(peerID)
	waitForChannel(t, loop.started, "scheduled resume turn did not start")

	handler.ProcessMessage("/clear", peerID)

	deadline := time.After(2 * time.Second)
	for {
		if loop.resumeContextDone() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("BUG: /clear did NOT cancel scheduled resume turn context")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestScheduleResumeKeepsActiveUserRequest(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	loop := &resumeCapturingLoop{
		mockAgentLoop: newMockAgentLoop(),
		started:       make(chan struct{}),
	}
	loop.blockCh = make(chan struct{})
	handler := NewBotHandlerWithPeerID(nil, loop, log, 0, 0, nil, nil)

	peerID := int64(999)
	userDone := make(chan string, 1)
	go func() {
		userDone <- handler.ProcessMessage("долгий запрос пользователя", peerID)
	}()
	time.Sleep(50 * time.Millisecond)

	handler.ScheduleResume(peerID)

	select {
	case <-loop.started:
		t.Fatal("resume must not start while a user request is active")
	case <-time.After(300 * time.Millisecond):
	}

	close(loop.blockCh)
	result := <-userDone

	if loop.cancelled {
		t.Error("active user request context must stay alive during ScheduleResume")
	}
	if result == "" {
		t.Error("expected non-empty result for user request")
	}

	loop.mu.Lock()
	calls := loop.resumeCalls
	loop.mu.Unlock()
	if calls != 0 {
		t.Errorf("expected resume skipped while user request active, ran %d times", calls)
	}
}
