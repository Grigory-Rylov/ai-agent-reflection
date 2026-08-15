package vk

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

// ============================================================
// Тесты: /clear и /n должны сбрасывать НЕ только активный запрос, но и
// сообщения, которые уже встали в очередь (ждут peer-mutex). Без фикса после
// /clear старое сообщение запускает нового агента в чистой сессии — агент
// «продолжает работу» вопреки просьбе пользователя.
// Инцидент 2026-08-15: «пришли в send-files», отправленное за 20 минут до
// /clear, выполнилось уже после сброса сессии и ушло файлом в VK.
// ============================================================

type countingMockAgentLoop struct {
	*mockAgentLoop
	calls int64 // сколько раз реально вызывался агент (атомарно)
}

func newCountingMockAgentLoop() *countingMockAgentLoop {
	return &countingMockAgentLoop{mockAgentLoop: newMockAgentLoop()}
}

func (m *countingMockAgentLoop) ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error) {
	atomic.AddInt64(&m.calls, 1)
	return m.mockAgentLoop.ProcessMessage(ctx, prompt, peerID)
}

// waitForWaiting ждёт, пока у пира не накопится expected сообщений, ожидающих
// peer-mutex. Это гарантирует: M2 уже зафиксировал генерацию сессии ДО сброса —
// без этой точки синхронизации тест зависел бы от расписания goroutine'ов.
func waitForWaiting(t *testing.T, h *BotHandler, peerID int64, expected int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if n := h.waitingMessages(peerID); n >= expected {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for peer %d to have %d message(s) in queue, got %d",
		peerID, expected, h.waitingMessages(peerID))
}

// runStaleQueueScenario — общий сценарий для /clear и /n:
//   - M1 (долгая задача) держит peer-mutex в агенте;
//   - M2 приходит во время обработки → встаёт в очередь, фиксирует генерацию;
//   - команда сброса (/clear или /n);
//   - после освобождения mutex'а M2 обязан быть ВЫБРОШЕН (у него чужая генерация);
//   - агент вызывается ровно один раз — только с M1.
func runStaleQueueScenario(t *testing.T, resetCommand string, peerID int64) {
	t.Helper()

	log, _ := logger.New(logger.DefaultConfig())
	mock := newCountingMockAgentLoop()
	mock.blockCh = make(chan struct{})

	handler := NewBotHandler(nil, mock, log)

	// M1 — «активная долгоживущая задача», держит peer-mutex на время работы агента.
	m1 := make(chan string, 1)
	go func() { m1 <- handler.ProcessMessage("долгая задача", peerID) }()
	time.Sleep(100 * time.Millisecond) // M1 дошёл до mock и сидит в blockCh

	// M2 — старое сообщение, встающее в очередь за M1.
	m2 := make(chan string, 1)
	go func() { m2 <- handler.ProcessMessage("старое сообщение", peerID) }()
	waitForWaiting(t, handler, peerID, 1) // M2 встал в очередь и зафиксировал генерацию

	// /clear (или /n) — пока M1 ещё активен.
	if res := handler.ProcessMessage(resetCommand, peerID); res == "" {
		t.Fatalf("command %q returned empty result", resetCommand)
	}

	select {
	case r := <-m1:
		if r != "" {
			t.Errorf("M1 after cancel: expected \"\", got %q", r)
		}
	case <-time.After(3 * time.Second):
		close(mock.blockCh)
		t.Fatal("M1 did not finish within 3s after session reset")
	}

	select {
	case r := <-m2:
		if r != "" {
			t.Errorf("stale M2 must be dropped silently, got %q", r)
		}
	case <-time.After(3 * time.Second):
		close(mock.blockCh)
		t.Fatal("M2 did not finish within 3s after session reset")
	}

	if !mock.cancelled {
		t.Error("M1 context was not cancelled by the session reset command")
	}
	if got := atomic.LoadInt64(&mock.calls); got != 1 {
		t.Fatalf("agent must be called exactly once (only M1), got %d — stale queued message leaked into new session", got)
	}

	close(mock.blockCh) // подстраховка, если mock всё ещё в блокнере
}

func TestClearDropsQueuedMessages(t *testing.T) {
	runStaleQueueScenario(t, "/clear", 90101)
}

func TestNewSessionDropsQueuedMessages(t *testing.T) {
	dir := t.TempDir()
	runStaleQueueScenario(t, "/n "+dir, 90102)
}
