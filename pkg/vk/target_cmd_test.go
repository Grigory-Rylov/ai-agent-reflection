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
	mock := newMockAgentLoop()
	orch := &mockOrchestrator{agentNames: names}
	return NewBotHandlerWithPeerID(nil, mock, log, 0, 0, orch, nil)
}

func TestHashMentionRoutesKnownAgentToQueue(t *testing.T) {
	handler := newTargetTestHandler([]string{"worker"})
	handler.SetTargetQueue(agentloop.NewTargetQueue(targetTestRunner, targetTestDeliver))

	reply := handler.ProcessMessage("#worker do x", 42)

	if !strings.HasPrefix(reply, "▶️") {
		t.Fatalf("expected immediate ack, got %q", reply)
	}
	if !strings.Contains(reply, "#worker") {
		t.Errorf("expected agent mention in ack, got %q", reply)
	}
}

func TestHashMentionMatchesCaseInsensitively(t *testing.T) {
	handler := newTargetTestHandler([]string{"worker"})
	handler.SetTargetQueue(agentloop.NewTargetQueue(targetTestRunner, targetTestDeliver))

	reply := handler.ProcessMessage("#WORKER do x", 42)

	if !strings.Contains(reply, "#worker") {
		t.Errorf("expected canonical name in ack, got %q", reply)
	}
}

func TestHashMentionEmptyTaskAsksForTask(t *testing.T) {
	handler := newTargetTestHandler([]string{"worker"})
	handler.SetTargetQueue(agentloop.NewTargetQueue(targetTestRunner, targetTestDeliver))

	reply := handler.ProcessMessage("#worker", 42)

	if !strings.Contains(reply, "Укажите задачу") {
		t.Fatalf("expected usage hint for empty task, got %q", reply)
	}
	if !strings.Contains(reply, "#worker") {
		t.Errorf("expected agent name in usage hint, got %q", reply)
	}
}

func TestHashMentionFallbackReachesMainAgentWithoutQueue(t *testing.T) {
	handler := newTargetTestHandler([]string{"worker"})
	mock := handler.aiAgent.(*mockAgentLoop)

	reply := handler.ProcessMessage("#worker do x", 42)

	if mock.lastMessage == "" {
		t.Fatalf("expected message forwarded to main agent, got empty")
	}
	if !strings.Contains(reply, "processed:") {
		t.Errorf("expected main-agent response, got %q", reply)
	}
}

func TestHashMentionQueuedPositionReporting(t *testing.T) {
	handler := newTargetTestHandler([]string{"worker"})

	heldEnter := make(chan struct{})
	releaseHeld := make(chan struct{})
	blockedEnter := make(chan struct{})
	releaseBlocked := make(chan struct{})

	queue := agentloop.NewTargetQueue(
		func(ctx context.Context, name, prompt string, peerID int64) (string, error) {
			switch prompt {
			case "held":
				close(heldEnter)
				<-releaseHeld
			case "blocked":
				close(blockedEnter)
				<-releaseBlocked
			}
			return "ok", nil
		},
		targetTestDeliver,
	)
	handler.SetTargetQueue(queue)

	first := handler.ProcessMessage("#worker held", 1)
	<-heldEnter
	second := handler.ProcessMessage("#worker blocked", 1)
	third := handler.ProcessMessage("#worker tail", 1)
	fourth := handler.ProcessMessage("#worker last", 1)

	assertReplyPosition(t, first, "")
	assertReplyPosition(t, second, "1")
	assertReplyPosition(t, third, "2")
	assertReplyPosition(t, fourth, "3")

	close(releaseHeld)
	<-blockedEnter
	close(releaseBlocked)
}

func TestHashMentionTruncatesLongPromptInAck(t *testing.T) {
	handler := newTargetTestHandler([]string{"worker"})
	handler.SetTargetQueue(agentloop.NewTargetQueue(targetTestRunner, targetTestDeliver))

	longPrompt := strings.Repeat("x", 300)
	reply := handler.ProcessMessage("#worker "+longPrompt, 42)

	if len(reply) > 300 {
		t.Errorf("ack exceeded truncation budget, got %d chars", len(reply))
	}
	if !strings.Contains(reply, "...") {
		t.Errorf("expected ellipsis marker in truncated ack, got %q", reply)
	}
}

func TestHashMentionQueuesBehindSpawnedSubagent(t *testing.T) {
	var mu sync.Mutex
	seen := []string{}
	inflight := 0
	maxInfl := 0
	spawnedEntered := make(chan struct{})
	releaseSpawned := make(chan struct{})

	queue := agentloop.NewTargetQueue(
		func(ctx context.Context, name, task string, peerID int64) (string, error) {
			mu.Lock()
			seen = append(seen, name+"@"+task)
			inflight++
			if inflight > maxInfl {
				maxInfl = inflight
			}
			isSpawned := task == "spawned-task"
			mu.Unlock()
			if isSpawned {
				close(spawnedEntered)
				<-releaseSpawned
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

	pos0 := queue.Submit("worker", "spawned-task", 100)
	if pos0 != 0 {
		t.Fatalf("spawned-task position = %d, want 0", pos0)
	}
	<-spawnedEntered

	first := handler.ProcessMessage("#worker follow-up-from-human", 200)
	assertReplyPosition(t, first, "1")

	second := handler.ProcessMessage("#worker yet-another", 200)
	assertReplyPosition(t, second, "2")

	close(releaseSpawned)

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
		"worker@spawned-task",
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

func TestHashMentionIndependentLanesPerAgent(t *testing.T) {
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
	replyQA := handler.ProcessMessage("#qa quick-check", 200)
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

	queued := handler.ProcessMessage("#worker second-for-worker", 200)
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

func TestHashMentionBypassesBusyMainAgentTurn(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newSteeringMockAgentLoop()
	orch := &mockOrchestrator{agentNames: []string{"worker"}}
	handler := NewBotHandlerWithPeerID(nil, mock, log, 0, 0, orch, nil)

	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})

	handler.SetTargetQueue(agentloop.NewTargetQueue(
		func(ctx context.Context, name, prompt string, peerID int64) (string, error) {
			close(entered)
			<-release
			return "ok", nil
		},
		targetTestDeliver,
	))

	peer := int64(70701)
	mock.onStart = make(chan struct{})
	go func() {
		defer close(done)
		handler.ProcessMessage("long main task", peer)
	}()

	select {
	case <-mock.onStart:
	case <-time.After(2 * time.Second):
		t.Fatal("main agent turn never started")
	}

	replyCh := make(chan string, 1)
	go func() { replyCh <- handler.ProcessMessage("#worker urgent side task", peer) }()

	select {
	case reply := <-replyCh:
		if !strings.HasPrefix(reply, "▶️") {
			t.Fatalf("expected immediate queue ack while main agent is busy, got %q", reply)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("#worker message blocked behind the running main-agent turn")
	}

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("target runner was never invoked while main agent was busy")
	}

	close(release)
	<-done

	for _, m := range mock.Drained() {
		if strings.HasPrefix(m, "#worker") {
			t.Errorf("#worker message leaked into the running main-agent turn: %v", mock.Drained())
		}
	}
}
