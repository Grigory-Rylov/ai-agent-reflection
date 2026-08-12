package vk

import (
	"strings"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/session"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

// ============================================================
// RED TEST: Commands fail when long poll loop is running AND
// agent process was killed mid-LLM-request. The pending question
// from the old process blocks new commands because it persists in
// global state (tools.RegisterPendingQuestion).
// ============================================================

func TestBUG_CommandsBlockedWhenAgentDiesMidTask(t *testing.T) {
	messenger := newMockMessenger()
	mockAgent := newMockAgentLoop()
	handler := NewBotHandlerWithMessenger(messenger, nil, mockAgent, nil, 12345)

	// Simulate: agent was working on a task and registered a pending question.
	// Then the process was killed (but handler is still running).
	ch := tools.RegisterPendingQuestion(12345)

	// Now user sends /clear — it should work even with stale pending question!
	msg := VKMessage{ID: 1, PeerID: 12345, FromID: 12345, Text: "/clear"}
	handler.launchMessageHandler(msg, 12345, nil)

	time.Sleep(200 * time.Millisecond) // wait for goroutine

	last := messenger.getLastMessage()
	if last.Text == "" {
		t.Error("BUG CONFIRMED: /clear failed — command response not sent!")
	} else if !strings.Contains(last.Text, "Сессия сброшена") && !strings.Contains(last.Text, "сессия сброшена") {
		t.Errorf("/clear gave wrong response: %q", last.Text)
	}

	select {
	case <-ch:
	default:
		close(ch)
	}
}

// ============================================================
// RED TEST: When agent is processing a long LLM request (mutex held),
// and user sends /m, the command should NOT be blocked by semaphore.
// After our refactoring, commands go through separate goroutine but
// if there's a pending question from that same peerID — commands die.
// ============================================================

func TestBUG_CommandsDroppedBySemaphore(t *testing.T) {
	messenger := newMockMessenger()
	mockAgent := &mockAgentLoop{sessions: make(map[int64]*session.Session)}
	blockCh := make(chan struct{}) // blocks LLM request
	mockAgent.blockCh = blockCh

	handler := NewBotHandlerWithMessenger(messenger, nil, mockAgent, nil, 12345)

	// Start a long LLM request (fills semaphore slot).
	done := make(chan bool, 1)
	go func() {
		msg := VKMessage{ID: 0, PeerID: 12345, FromID: 12345, Text: "very long task"}
		handler.launchMessageHandler(msg, 12345, nil)
		done <- true
	}()

	time.Sleep(50 * time.Millisecond) // let mutex lock

	// Fill up all semaphore slots (maxConcurrentHandlers = 10).
	for i := 0; i < maxConcurrentHandlers-1; i++ {
		go func(pid int64) {
			blockCh2 := make(chan struct{})
			m2 := &mockAgentLoop{sessions: make(map[int64]*session.Session), blockCh: blockCh2}
			h2 := NewBotHandlerWithMessenger(messenger, nil, m2, nil, pid)
			msg := VKMessage{ID: int64(i + 100), PeerID: pid, FromID: pid, Text: "blocker"}
			h2.launchMessageHandler(msg, pid, nil)
		}(int64(90000 + i))
	}

	time.Sleep(50 * time.Millisecond) // let semaphore fill up

	// Now /clear from main peer — should NOT be dropped!
	msg := VKMessage{ID: 1, PeerID: 12345, FromID: 12345, Text: "/clear"}
	handler.launchMessageHandler(msg, 12345, nil)

	time.Sleep(200 * time.Millisecond) // wait for command goroutine

	last := messenger.getLastMessage()
	if last.Text == "" {
		t.Error("BUG CONFIRMED: /clear was dropped — semaphore full or pending question blocked it!")
	} else if !strings.Contains(last.Text, "Сессия сброшена") && !strings.Contains(last.Text, "сессия сброшена") {
		t.Errorf("/clear gave wrong response under load: %q", last.Text)
	}

	close(blockCh)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("LLM goroutine hung")
	}
}

// ============================================================
// RED TEST: Full flow — Start() → long poll simulation → /clear command.
// After refactoring messenger/vkClient, messages might not reach ProcessMessage.
// ============================================================

func TestBUG_CommandsNotReachingProcessMessage(t *testing.T) {
	// This test verifies the complete path from launchMessageHandler to messenger send.
	messenger := newMockMessenger()
	mockAgent := newMockAgentLoop()
	handler := NewBotHandlerWithMessenger(messenger, nil, mockAgent, nil, 12345)

	cmds := []string{"/clear", "/m", "/help", "/status"}
	results := make(chan string, len(cmds))

	for _, cmd := range cmds {
		go func(c string) {
			msg := VKMessage{ID: 1, PeerID: 12345, FromID: 12345, Text: c}
			handler.launchMessageHandler(msg, 12345, nil)
			time.Sleep(100 * time.Millisecond) // wait for goroutine
			messenger.mu.Lock()
			last := messenger.lastText
			messenger.mu.Unlock()
			results <- last
		}(cmd)
	}

	for _, cmd := range cmds {
		select {
		case result := <-results:
			if result == "" {
				t.Errorf("BUG CONFIRMED: command %q produced NO response — full flow broken!", cmd)
			} else if !strings.Contains(result, "Сессия сброшена") && // /clear
				!strings.Contains(result, "модель") && // /m (may fail without holder)
				!strings.Contains(result, "Доступные команды:") && // /help
				!strings.Contains(result, "AI Agent активен") { // /status
				t.Errorf("Command %q gave unexpected response: %q", cmd, result)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("Timeout waiting for command results — launchMessageHandler not dispatching!")
		}
	}

	close(results)
}
