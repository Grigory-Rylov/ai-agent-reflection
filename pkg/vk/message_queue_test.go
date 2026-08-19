package vk

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)


type slowMockAgentLoop struct {
	mockAgentLoop
	delay       time.Duration 
	calls       int64         
	cancelled   bool          
	mu          sync.Mutex    
	processedMu sync.Mutex
	processed   []string      
}

func newSlowMockAgentLoop(delay time.Duration) *slowMockAgentLoop {
	return &slowMockAgentLoop{
		mockAgentLoop: *newMockAgentLoop(),
		delay:         delay,
	}
}

func (m *slowMockAgentLoop) ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error) {
	atomic.AddInt64(&m.calls, 1)

	
	select {
	case <-ctx.Done():
		m.mu.Lock()
		m.cancelled = true
		m.mu.Unlock()
		return "", ctx.Err()
	default:
	}

	
	select {
	case <-time.After(m.delay):
		m.processedMu.Lock()
		m.processed = append(m.processed, prompt)
		m.processedMu.Unlock()
		return "processed: " + prompt, nil
	case <-ctx.Done():
		m.mu.Lock()
		m.cancelled = true
		m.mu.Unlock()
		return "", ctx.Err()
	}
}

func (m *slowMockAgentLoop) GetCancelled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancelled
}

func (m *slowMockAgentLoop) ResetProcessed() {
	m.processedMu.Lock()
	defer m.processedMu.Unlock()
	m.processed = nil
}


func TestMessageQueue_WhenAgentBusy_DoesNotCancelCurrentContext(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())

	
	slowMock := newSlowMockAgentLoop(200 * time.Millisecond)
	handler := NewBotHandler(nil, slowMock, log)

	peerID := int64(12345)

	var wg sync.WaitGroup
	var firstResult, secondResult string

	wg.Add(2)

	go func() {
		defer wg.Done()
		firstResult = handler.ProcessMessage("first message", peerID)
	}()

	
	time.Sleep(50 * time.Millisecond)

	go func() {
		defer wg.Done()
		secondResult = handler.ProcessMessage("second message", peerID)
	}()

	wg.Wait()

	if firstResult == "" && secondResult == "" {
		t.Fatal("Both requests returned empty — likely both were canceled")
	}

	
	if slowMock.GetCancelled() {
		t.Error("FAIL: first request context was CANCELED by second request — queueing not working")
	}

	
	if firstResult == "" && !slowMock.GetCancelled() {
		t.Error("First request returned empty despite no cancellation detected")
	}

	
	slowMock.processedMu.Lock()
	processed := make([]string, len(slowMock.processed))
	copy(processed, slowMock.processed)
	slowMock.processedMu.Unlock()
	t.Logf("Processed order (expected sequential): %v", processed)

	if secondResult == "" && !slowMock.GetCancelled() {
		t.Error("Second request returned empty despite no cancellation detected")
	}
}
