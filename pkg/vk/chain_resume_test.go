package vk

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

type chainRecordingOrchestrator struct {
	*mockOrchestrator
	mu        sync.Mutex
	peers     []int64
	resumeCtx context.Context
	started   chan struct{}
}

func (c *chainRecordingOrchestrator) ActiveChainPeers() []int64 { return c.peers }

func (c *chainRecordingOrchestrator) ResumeActiveChainsForPeer(ctx context.Context, peerID int64) error {
	c.mu.Lock()
	c.resumeCtx = ctx
	c.mu.Unlock()
	close(c.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestClearCancelsScheduledChainResume(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	loop := newMockAgentLoop()
	orch := &chainRecordingOrchestrator{
		mockOrchestrator: &mockOrchestrator{clearedPeers: make(map[int64]bool)},
		peers:            []int64{4242},
		started:          make(chan struct{}),
	}
	handler := NewBotHandlerWithPeerID(nil, loop, log, 4242, 0, orch, nil)

	handler.ScheduleChainResume()

	select {
	case <-orch.started:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled chain resume did not start")
	}

	handler.ProcessMessage("/clear", 4242)

	deadline := time.After(2 * time.Second)
	for {
		orch.mu.Lock()
		done := orch.resumeCtx != nil && orch.resumeCtx.Err() != nil
		orch.mu.Unlock()
		if done {
			return
		}
		select {
		case <-deadline:
			t.Fatal("BUG: /clear did NOT cancel scheduled chain resume context")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestScheduleChainResumeSkipsBusyPeer(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	loop := newMockAgentLoop()
	loop.blockCh = make(chan struct{})
	orch := &chainRecordingOrchestrator{
		mockOrchestrator: &mockOrchestrator{clearedPeers: make(map[int64]bool)},
		peers:            []int64{31337},
		started:          make(chan struct{}),
	}
	handler := NewBotHandlerWithPeerID(nil, loop, log, 31337, 0, orch, nil)

	userDone := make(chan string, 1)
	go func() {
		userDone <- handler.ProcessMessage("активная задача", 31337)
	}()
	time.Sleep(50 * time.Millisecond)

	handler.ScheduleChainResume()

	select {
	case <-orch.started:
		t.Fatal("BUG: chain resume started while user request is active")
	case <-time.After(300 * time.Millisecond):
	}
	if loop.cancelled {
		t.Fatal("chain resume cancelled the active user request")
	}

	close(loop.blockCh)
	select {
	case <-userDone:
	case <-time.After(2 * time.Second):
		t.Fatal("user request did not finish")
	}
}
