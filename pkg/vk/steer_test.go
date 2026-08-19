package vk

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)


// steeringMockAgentLoop mimics the real agent loop: it drains the peer inbox at
// the END of its processing (i.e. promotes messages that arrived mid-turn).
type steeringMockAgentLoop struct {
	*mockAgentLoop
	mu      sync.Mutex
	drained []string
}

func newSteeringMockAgentLoop() *steeringMockAgentLoop {
	return &steeringMockAgentLoop{mockAgentLoop: newMockAgentLoop()}
}

func (m *steeringMockAgentLoop) ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error) {
	select {
	case <-ctx.Done():
		m.mu.Lock()
		m.cancelled = true
		m.mu.Unlock()
		return "", ctx.Err()
	case <-time.After(150 * time.Millisecond):
	}

	// Mimic the real agent loop: promote any admitted messages into the run.
	if s := m.GetSession(peerID); s != nil {
		if in := s.GetPeerInput(); in != nil {
			if msgs := in.Drain(); len(msgs) > 0 {
				m.mu.Lock()
				m.drained = append(m.drained, msgs...)
				m.mu.Unlock()
			}
		}
	}
	return "done: " + prompt, nil
}

func (m *steeringMockAgentLoop) Drained() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.drained))
	copy(out, m.drained)
	return out
}

func (m *steeringMockAgentLoop) GetCancelled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancelled
}


// TestSteer_InjectedIntoRunningTurn verifies the core "steer" wiring at the
// handler level: a message that arrives while a run is active is handed to the
// running loop via the peer inbox (promoted), instead of being processed as a
// separate queued turn — and it must not cancel the running turn.
func TestSteer_InjectedIntoRunningTurn(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newSteeringMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)
	peerID := int64(90901)

	steer := "расскажи что ты успел"

	var firstResult string
	done := make(chan struct{})
	go func() {
		defer close(done)
		firstResult = handler.ProcessMessage("задача", peerID)
	}()

	// Let the run become active, then send the second message. It blocks until
	// the running turn finishes, then finds its message was already promoted.
	time.Sleep(50 * time.Millisecond)
	secondResult := handler.ProcessMessage(steer, peerID)

	<-done

	if secondResult != "" {
		t.Errorf("second message should be consumed by the running turn (got %q)", secondResult)
	}
	drained := mock.Drained()
	if len(drained) != 1 || drained[0] != steer {
		t.Errorf("expected the running turn to promote %q, got %v", steer, drained)
	}
	if firstResult == "" {
		t.Error("first (owner) message should return a non-empty result")
	}
	if mock.GetCancelled() {
		t.Error("second message must not cancel the running turn")
	}
}
