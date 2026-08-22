package vk

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

type recordingLoop struct {
	*mockAgentLoop
	mu      sync.Mutex
	prompts []string
}

func (r *recordingLoop) ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error) {
	r.mu.Lock()
	r.prompts = append(r.prompts, prompt)
	r.mu.Unlock()
	return r.mockAgentLoop.ProcessMessage(ctx, prompt, peerID)
}

func (r *recordingLoop) hasPrompt(prompt string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.prompts {
		if p == prompt {
			return true
		}
	}
	return false
}

func TestPinQueuesBehindActiveRequest(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	loop := &recordingLoop{mockAgentLoop: newMockAgentLoop()}
	loop.blockCh = make(chan struct{})
	handler := NewBotHandlerWithPeerID(nil, loop, log, 0, 0, nil, nil)

	peerID := int64(1111)
	pinText := "отвечай кратко"

	userDone := make(chan string, 1)
	go func() {
		userDone <- handler.ProcessMessage("долгая задача", peerID)
	}()
	time.Sleep(50 * time.Millisecond)

	pinDone := make(chan string, 1)
	go func() {
		pinDone <- handler.ProcessMessage("/pin "+pinText, peerID)
	}()

	select {
	case res := <-pinDone:
		t.Fatalf("BUG: /pin executed while user request still active, got %q", res)
	case <-time.After(300 * time.Millisecond):
	}
	if loop.cancelled {
		t.Fatal("BUG: /pin cancelled the active user request context")
	}

	close(loop.blockCh)

	select {
	case <-userDone:
	case <-time.After(2 * time.Second):
		t.Fatal("user request did not finish")
	}

	select {
	case res := <-pinDone:
		if res == "" {
			t.Error("expected non-empty pin result")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued pin did not execute after active request finished")
	}

	if !loop.hasPrompt(pinText) {
		t.Errorf("expected pin prompt executed, prompts=%v", loop.prompts)
	}
	sess := loop.GetSession(peerID)
	if sess == nil {
		t.Fatal("expected session after pin turn")
	}
	pinned := false
	for _, p := range sess.GetPinned() {
		if p == pinText {
			pinned = true
		}
	}
	if !pinned {
		t.Errorf("expected pinned prompt saved, pinned=%v", sess.GetPinned())
	}
}

func TestClearDropsQueuedPinExecution(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	loop := &recordingLoop{mockAgentLoop: newMockAgentLoop()}
	loop.blockCh = make(chan struct{})
	handler := NewBotHandlerWithPeerID(nil, loop, log, 0, 0, nil, nil)

	peerID := int64(2222)
	pinText := "работай по чеклисту"

	userDone := make(chan string, 1)
	go func() {
		userDone <- handler.ProcessMessage("долгая задача", peerID)
	}()
	time.Sleep(50 * time.Millisecond)

	pinDone := make(chan string, 1)
	go func() {
		pinDone <- handler.ProcessMessage("/pin "+pinText, peerID)
	}()
	time.Sleep(100 * time.Millisecond)

	clearResult := handler.ProcessMessage("/clear", peerID)
	if clearResult == "" {
		t.Error("expected non-empty clear result")
	}

	close(loop.blockCh)

	select {
	case <-userDone:
	case <-time.After(2 * time.Second):
		t.Fatal("user request did not finish")
	}
	select {
	case <-pinDone:
	case <-time.After(2 * time.Second):
		t.Fatal("queued pin goroutine did not return after clear")
	}

	time.Sleep(100 * time.Millisecond)
	if loop.hasPrompt(pinText) {
		t.Error("BUG: queued pin executed after session was cleared")
	}
}
