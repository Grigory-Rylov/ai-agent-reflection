package vk

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

type sequencedResumeLoop struct {
	*mockAgentLoop
	mu     sync.Mutex
	events []string
	slow   time.Duration
}

func (m *sequencedResumeLoop) ResumeInterruptedTask(ctx context.Context, peerID int64) {
	m.mu.Lock()
	m.events = append(m.events, "task")
	m.mu.Unlock()
	time.Sleep(m.slow)
}

type sequencedChainOrchestrator struct {
	*mockOrchestrator
	mu        sync.Mutex
	events    []string
	chainDone chan struct{}
}

func (o *sequencedChainOrchestrator) ActiveChainPeers() []int64 { return []int64{70707} }

func (o *sequencedChainOrchestrator) ResumeActiveChainsForPeer(ctx context.Context, peerID int64) error {
	o.mu.Lock()
	o.events = append(o.events, "chain")
	o.mu.Unlock()
	select {
	case <-o.chainDone:
	default:
		close(o.chainDone)
	}
	return nil
}

func TestStartupResumeDoesNotStarveChainResume(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())

	loop := &sequencedResumeLoop{mockAgentLoop: newMockAgentLoop(), slow: 150 * time.Millisecond}
	orch := &sequencedChainOrchestrator{
		mockOrchestrator: &mockOrchestrator{clearedPeers: make(map[int64]bool)},
		chainDone:        make(chan struct{}),
	}
	handler := NewBotHandlerWithPeerID(nil, loop, log, 70707, 0, orch, nil)

	handler.ScheduleResume(70707)
	handler.ScheduleChainResume()

	select {
	case <-orch.chainDone:
	case <-time.After(3 * time.Second):
		t.Fatal("BUG: chain resume was skipped because task resume held the peer slot")
	}

	time.Sleep(300 * time.Millisecond)

	loop.mu.Lock()
	taskEvents := len(loop.events)
	loop.mu.Unlock()
	orch.mu.Lock()
	chainEvents := len(orch.events)
	orch.mu.Unlock()

	if taskEvents != 1 {
		t.Fatalf("task resume calls = %d, want 1", taskEvents)
	}
	if chainEvents != 1 {
		t.Fatalf("chain resume calls = %d, want 1", chainEvents)
	}
}
