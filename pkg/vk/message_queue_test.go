package vk

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencode/llama-client/pkg/logger"
)

// slowMockAgentLoop — mock который задерживает ответ для тестирования очереди
type slowMockAgentLoop struct {
	mockAgentLoop
	delay       time.Duration // задержка перед ответом
	calls       int64         // счётчик вызовов (атомарный)
	cancelled   bool          // был ли контекст отменён во время выполнения
	mu          sync.Mutex    // для синхронизации cancelled
	processedMu sync.Mutex
	processed   []string      // порядок обработки сообщений
}

func newSlowMockAgentLoop(delay time.Duration) *slowMockAgentLoop {
	return &slowMockAgentLoop{
		mockAgentLoop: *newMockAgentLoop(),
		delay:         delay,
	}
}

func (m *slowMockAgentLoop) ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error) {
	atomic.AddInt64(&m.calls, 1)

	// Проверяем контекст перед началом работы
	select {
	case <-ctx.Done():
		m.mu.Lock()
		m.cancelled = true
		m.mu.Unlock()
		return "", ctx.Err()
	default:
	}

	// Эмулируем медленную обработку (как agent с tool calls)
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

// TestMessageQueue_WhenAgentBusy_DoesNotCancelCurrentContext проверяет что при
// поступлении нового сообщения во время обработки текущего, новый запрос НЕ
// отменяет контекст работающего агента. Сообщение должно встать в очередь и
// обработаться после завершения текущей задачи без cancel() предыдущего ctx.
func TestMessageQueue_WhenAgentBusy_DoesNotCancelCurrentContext(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())

	// Создаём mock с задержкой 200ms — достаточно чтобы второй вызов пришёл до завершения первого
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

	// Даем времени первому сообщению начать обработку (50ms — пока оно работает 200ms)
	time.Sleep(50 * time.Millisecond)

	go func() {
		defer wg.Done()
		secondResult = handler.ProcessMessage("second message", peerID)
	}()

	wg.Wait()

	if firstResult == "" && secondResult == "" {
		t.Fatal("Both requests returned empty — likely both were canceled")
	}

	// Проверяем что контекст НЕ был отменён у первого запроса
	if slowMock.GetCancelled() {
		t.Error("FAIL: first request context was CANCELED by second request — queueing not working")
	}

	// Оба запроса должны завершиться успешно (не через cancel)
	if firstResult == "" && !slowMock.GetCancelled() {
		t.Error("First request returned empty despite no cancellation detected")
	}

	// Второй запрос должен был дождаться первого и обработаться последовательно
	slowMock.processedMu.Lock()
	processed := make([]string, len(slowMock.processed))
	copy(processed, slowMock.processed)
	slowMock.processedMu.Unlock()
	t.Logf("Processed order (expected sequential): %v", processed)

	if secondResult == "" && !slowMock.GetCancelled() {
		t.Error("Second request returned empty despite no cancellation detected")
	}
}
