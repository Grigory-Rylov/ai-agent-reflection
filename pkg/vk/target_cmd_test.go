package vk

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentloop"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

func newTargetTestHandler(names []string) *BotHandler {
	log, _ := logger.New(logger.DefaultConfig())
	mock := &mockAgentLoop{}
	orch := &mockOrchestrator{agentNames: names}
	return NewBotHandlerWithPeerID(nil, mock, log, 0, 0, orch, nil)
}

func TestTargetCommandAcceptsValidSubmission(t *testing.T) {
	handler := newTargetTestHandler([]string{"worker"})
	handler.SetTargetQueue(agentloop.NewTargetQueue(targetTestRunner, targetTestDeliver))

	reply := handler.handleCommand("/target #worker do x", 42)

	if !strings.HasPrefix(reply, "▶️") {
		t.Fatalf("expected immediate ack, got %q", reply)
	}
	if !strings.Contains(reply, "#worker") {
		t.Errorf("expected agent mention in ack, got %q", reply)
	}
}

func TestTargetCommandAcceptsNameWithoutHash(t *testing.T) {
	handler := newTargetTestHandler([]string{"worker"})
	handler.SetTargetQueue(agentloop.NewTargetQueue(targetTestRunner, targetTestDeliver))

	reply := handler.handleCommand("/target worker do x", 42)

	if !strings.HasPrefix(reply, "▶️") {
		t.Fatalf("expected immediate ack, got %q", reply)
	}
	if !strings.Contains(reply, "#worker") {
		t.Errorf("expected canonical name in ack, got %q", reply)
	}
}

func TestTargetCommandRejectsUnknownName(t *testing.T) {
	handler := newTargetTestHandler([]string{"worker", "qa"})
	handler.SetTargetQueue(agentloop.NewTargetQueue(targetTestRunner, targetTestDeliver))

	reply := handler.handleCommand("/target #nosuch do x", 42)

	if !strings.Contains(reply, "Неизвестный агент") {
		t.Fatalf("expected unknown-agent rejection, got %q", reply)
	}
	if !strings.Contains(reply, "worker") || !strings.Contains(reply, "qa") {
		t.Errorf("expected available-names list, got %q", reply)
	}
}

func TestTargetCommandReportsUsageOnMissingArgs(t *testing.T) {
	handler := newTargetTestHandler([]string{"worker"})
	handler.SetTargetQueue(agentloop.NewTargetQueue(targetTestRunner, targetTestDeliver))

	tests := []struct {
		name  string
		input string
	}{
		{"bare command", "/target"},
		{"name only", "/target #worker"},
		{"hash only", "/target #"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reply := handler.handleCommand(tc.input, 42)
			if !strings.Contains(reply, "Использование:") {
				t.Errorf("expected usage message, got %q", reply)
			}
			if !strings.Contains(reply, "worker") {
				t.Errorf("usage should list available agents, got %q", reply)
			}
		})
	}
}

func TestTargetCommandUnavailableWithoutQueue(t *testing.T) {
	handler := newTargetTestHandler([]string{"worker"})

	reply := handler.handleCommand("/target #worker do x", 42)

	if !strings.Contains(reply, "недоступна") {
		t.Fatalf("expected unavailable message, got %q", reply)
	}
}

func TestTargetCommandQueuedPositionReporting(t *testing.T) {
	handler := newTargetTestHandler([]string{"worker"})

	holdEnter := make(chan struct{})
	releaseHold := make(chan struct{})
	blockEnter := make(chan struct{})
	releaseBlock := make(chan struct{})

	queue := agentloop.NewTargetQueue(
		func(ctx context.Context, name, prompt string, peerID int64) (string, error) {
			switch prompt {
			case "hold":
				close(holdEnter)
				<-releaseHold
			case "block":
				close(blockEnter)
				<-releaseBlock
			}
			return "ok", nil
		},
		targetTestDeliver,
	)
	handler.SetTargetQueue(queue)

	first := handler.handleCommand("/target #worker hold", 1)
	<-holdEnter
	second := handler.handleCommand("/target #worker block", 1)
	third := handler.handleCommand("/target #worker tail", 1)
	fourth := handler.handleCommand("/target #worker last", 1)

	assertReplyPosition(t, first, "")
	assertReplyPosition(t, second, "1")
	assertReplyPosition(t, third, "2")
	assertReplyPosition(t, fourth, "3")

	close(releaseHold)
	<-blockEnter
	close(releaseBlock)
}

func TestTargetCommandTruncatesLongPromptInAck(t *testing.T) {
	handler := newTargetTestHandler([]string{"worker"})
	handler.SetTargetQueue(agentloop.NewTargetQueue(targetTestRunner, targetTestDeliver))

	longPrompt := strings.Repeat("x", 300)
	reply := handler.handleCommand("/target #worker "+longPrompt, 42)

	if len(reply) > 300 {
		t.Errorf("ack exceeded truncation budget, got %d chars", len(reply))
	}
	if !strings.Contains(reply, "...") {
		t.Errorf("expected ellipsis marker in truncated ack, got %q", reply)
	}
}

func TestTargetCommandQueuesBehindExistingWorkersInvocation(t *testing.T) {
	var mu sync.Mutex
	seen := []string{}
	inflight := 0
	maxInfl := 0
	leadEntered := make(chan struct{})
	releaseLead := make(chan struct{})

	queue := agentloop.NewTargetQueue(
		func(ctx context.Context, name, task string, peerID int64) (string, error) {
			mu.Lock()
			seen = append(seen, name+"@"+task)
			inflight++
			if inflight > maxInfl {
				maxInfl = inflight
			}
			isLeader := task == "lead-spawned"
			mu.Unlock()
			if isLeader {
				close(leadEntered)
				<-releaseLead
			}
			mu.Lock()
			inflight--
			mu.Unlock()
			return "ok:" + task, nil
		},
		nil,
	)

	handler := newTargetTestHandler([]string{"lead", "worker"})
	handler.SetTargetQueue(queue)

	pos0 := queue.Submit("worker", "lead-spawned", 100)
	if pos0 != 0 {
		t.Fatalf("lead-spawned position = %d, want 0", pos0)
	}
	<-leadEntered

	first := handler.handleCommand("/target #worker follow-up-from-human", 200)
	assertReplyPosition(t, first, "1")

	second := handler.handleCommand("/target #worker yet-another", 200)
	assertReplyPosition(t, second, "2")

	close(releaseLead)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"worker@lead-spawned",
		"worker@follow-up-from-human",
		"worker@yet-another",
	}
	if len(seen) != len(want) {
		t.Fatalf("dispatch sequence = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("dispatch[%d] = %q, want %q", i, seen[i], want[i])
		}
	}
	if maxInfl > 1 {
		t.Errorf("peak concurrency for worker = %d, want <= 1", maxInfl)
	}
}

func TestTargetCommandDifferentAgentNotAffectedByBlockedWorker(t *testing.T) {
	var mu sync.Mutex
	workersStarted := 0
	qaCompleted := 0
	releaseWorker := make(chan struct{})

	queue := agentloop.NewTargetQueue(
		func(ctx context.Context, name, task string, peerID int64) (string, error) {
			mu.Lock()
			switch name {
			case "worker":
				workersStarted++
				first := workersStarted == 1
				mu.Unlock()
				if first {
					<-releaseWorker
				}
			default:
				mu.Unlock()
			}
			mu.Lock()
			if name == "qa" {
				qaCompleted++
			}
			mu.Unlock()
			return "ok", nil
		},
		nil,
	)

	handler := newTargetTestHandler([]string{"lead", "worker", "qa"})
	handler.SetTargetQueue(queue)

	queue.Submit("worker", "occupies-worker-lane", 100)
	replyQA := handler.handleCommand("/target #qa quick-check", 200)
	assertReplyPosition(t, replyQA, "")

	deadline := time.Now().Add(2 * time.Second)
	ok := false
	for time.Now().Before(deadline) {
		mu.Lock()
		n := qaCompleted
		mu.Unlock()
		if n >= 1 {
			ok = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !ok {
		t.Fatalf("qa lane did not complete while worker lane was blocked")
	}

	queued := handler.handleCommand("/target #worker second-for-worker", 200)
	assertReplyPosition(t, queued, "1")

	close(releaseWorker)
}

func targetTestRunner(ctx context.Context, name, prompt string, peerID int64) (string, error) {
	return "ok", nil
}

func targetTestDeliver(name string, peerID int64, resp string, err error) {}

func assertReplyPosition(t *testing.T, reply, wantPos string) {
	t.Helper()
	if wantPos == "" {
		if !strings.HasPrefix(reply, "▶️") {
			t.Errorf("expected immediate ack (position 0), got %q", reply)
		}
		return
	}
	if !strings.Contains(reply, "⏳") {
		t.Errorf("expected queued ack, got %q", reply)
	}
	if !strings.Contains(reply, "позиция "+wantPos) {
		t.Errorf("expected position %s, got %q", wantPos, reply)
	}
}
