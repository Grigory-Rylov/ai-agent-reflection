package vk

import (
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)


func TestClearCancelsActiveLLMRequest(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	mock.blockCh = make(chan struct{}) 

	orch := &mockOrchestrator{clearedPeers: make(map[int64]bool)}
	handler := NewBotHandlerWithPeerID(nil, mock, log, 0, 0, orch, nil)

	peerID := int64(555)

	
	msgDone := make(chan string, 1)
	go func() {
		msgDone <- handler.ProcessMessage("долгий запрос к LLM", peerID)
	}()

	
	time.Sleep(50 * time.Millisecond)

	
	clearResult := handler.ProcessMessage("/clear", peerID)
	t.Logf("/clear result: %s", clearResult)

	
	select {
	case result := <-msgDone:
		if result != "" {
			t.Errorf("expected empty result after cancel, got %q", result)
		}
	case <-time.After(2 * time.Second):
		
		close(mock.blockCh)
		result := <-msgDone
		if mock.cancelled {
			t.Log("context was cancelled, unblocking manually confirmed")
		} else {
			t.Error("expected context to be cancelled by /clear, but it was NOT cancelled")
		}
		if result != "" {
			t.Errorf("expected empty result, got %q", result)
		}
	}

	
	if !mock.cancelled {
		t.Error("BUG: /clear did NOT cancel active LLM request context")
	}
	if !orch.clearedPeers[peerID] {
		t.Error("expected ClearActiveSessions called for peer")
	}

	
	sess := mock.GetSession(peerID)
	if sess == nil {
		t.Fatal("session should exist after /clear")
	}
	if sess.HistoryLength() != 0 {
		t.Errorf("expected empty history after /clear, got %d", sess.HistoryLength())
	}
}


func TestNewSessionCancelsActiveLLMRequest(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	mock.blockCh = make(chan struct{})

	orch := &mockOrchestrator{clearedPeers: make(map[int64]bool)}
	handler := NewBotHandlerWithPeerID(nil, mock, log, 0, 0, orch, nil)

	peerID := int64(666)

	msgDone := make(chan string, 1)
	go func() {
		msgDone <- handler.ProcessMessage("ещё один долгий запрос", peerID)
	}()

	time.Sleep(50 * time.Millisecond)

	
	handler.ProcessMessage("/n /tmp", peerID)

	select {
	case <-msgDone:
		
	case <-time.After(2 * time.Second):
		close(mock.blockCh)
		<-msgDone
	}

	if !mock.cancelled {
		t.Error("BUG: /n did NOT cancel active LLM request context")
	}

	close(mock.blockCh)
}


func TestClearNotBlockedBySlowLLM(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	mock.slowDelay = 5 * time.Second 

	handler := NewBotHandler(nil, mock, log)
	peerID := int64(777)

	
	msgDone := make(chan string, 1)
	go func() {
		msgDone <- handler.ProcessMessage("медленный запрос к LLM", peerID)
	}()

	
	time.Sleep(100 * time.Millisecond)

	
	start := time.Now()
	clearResult := handler.ProcessMessage("/clear", peerID)
	elapsed := time.Since(start)

	if clearResult == "" {
		t.Error("expected non-empty /clear result")
	}
	if elapsed >= 2*time.Second {
		t.Errorf("/clear blocked for %v — должен обрабатываться мгновенно, не дожидаясь LLM (5с)", elapsed)
	}

	
	select {
	case result := <-msgDone:
		if result != "" {
			t.Errorf("expected empty result after cancel, got %q", result)
		}
	case <-time.After(6 * time.Second):
		t.Error("active LLM request did not finish after /clear")
	}

	if !mock.cancelled {
		t.Error("expected /clear to cancel the active LLM request context")
	}

	
	if sess := mock.GetSession(peerID); sess == nil {
		t.Fatal("session should exist after /clear")
	} else if sess.HistoryLength() != 0 {
		t.Errorf("expected empty history after /clear, got %d", sess.HistoryLength())
	}
}
