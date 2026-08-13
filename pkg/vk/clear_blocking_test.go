package vk

import (
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

// ============================================================
// Тесты отмены активного LLM-запроса при /clear и /n
// ============================================================

// TestClearCancelsActiveLLMRequest проверяет, что /clear отменяет
// выполняющийся LLM-запрос главного агента, а не только сабагентов.
//
// Сценарий:
//  1. Пользователь отправляет обычное сообщение → агент начинает LLM-запрос
//  2. Не дожидаясь ответа, пользователь шлёт /clear
//  3. /clear должен отменить контекст активного LLM-запроса
//  4. LLM-запрос должен получить context.Canceled
//
// Без фикса: /clear очищает сессию и сабагентов, но НЕ отменяет основной
// LLM-запрос — он продолжает выполняться и после завершения пишет в уже
// очищенную сессию, а также может заспавнить новых сабагентов.
func TestClearCancelsActiveLLMRequest(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	mock.blockCh = make(chan struct{}) // будет блокировать ProcessMessage

	orch := &mockOrchestrator{clearedPeers: make(map[int64]bool)}
	handler := NewBotHandlerWithPeerID(nil, mock, log, 0, 0, orch, nil)

	peerID := int64(555)

	// Запускаем обычное сообщение в отдельной goroutine — оно заблокируется.
	msgDone := make(chan string, 1)
	go func() {
		msgDone <- handler.ProcessMessage("долгий запрос к LLM", peerID)
	}()

	// Даём время goroutine дойти до блокировки в mock.ProcessMessage.
	time.Sleep(50 * time.Millisecond)

	// Отправляем /clear — должно отменить контекст блокированного запроса.
	clearResult := handler.ProcessMessage("/clear", peerID)
	t.Logf("/clear result: %s", clearResult)

	// Ждём завершения первой goroutine (контекст отменён → вернёт "").
	select {
	case result := <-msgDone:
		if result != "" {
			t.Errorf("expected empty result after cancel, got %q", result)
		}
	case <-time.After(2 * time.Second):
		// Не дождались — разблокируем вручную и проверяем что контекст не отменён.
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

	// Проверяем флаги mock-а.
	if !mock.cancelled {
		t.Error("BUG: /clear did NOT cancel active LLM request context")
	}
	if !orch.clearedPeers[peerID] {
		t.Error("expected ClearActiveSessions called for peer")
	}

	// Сессия должна быть очищена.
	sess := mock.GetSession(peerID)
	if sess == nil {
		t.Fatal("session should exist after /clear")
	}
	if sess.HistoryLength() != 0 {
		t.Errorf("expected empty history after /clear, got %d", sess.HistoryLength())
	}
}

// TestNewSessionCancelsActiveLLMRequest проверяет, что /n тоже отменяет
// активный LLM-запрос (тот же механизм через handleNewSession).
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

	// /n (newsession) тоже должен отменять активный запрос.
	handler.ProcessMessage("/n /tmp", peerID)

	select {
	case <-msgDone:
		// ok
	case <-time.After(2 * time.Second):
		close(mock.blockCh)
		<-msgDone
	}

	if !mock.cancelled {
		t.Error("BUG: /n did NOT cancel active LLM request context")
	}

	close(mock.blockCh)
}

// TestClearNotBlockedBySlowLLM проверяет, что /clear доходит до обработки
// мгновенно, даже когда LLM-запрос главного агента выполняется долго (5 сек).
// Команды должны обрабатываться до захвата peer-мьютекса, поэтому /clear не
// должен «висеть» в ожидании ответа LLM.
func TestClearNotBlockedBySlowLLM(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	mock.slowDelay = 5 * time.Second // имитируем медленный LLM-запрос

	handler := NewBotHandler(nil, mock, log)
	peerID := int64(777)

	// Запускаем обычное сообщение в фоне — mock блокируется на 5 секунд.
	msgDone := make(chan string, 1)
	go func() {
		msgDone <- handler.ProcessMessage("медленный запрос к LLM", peerID)
	}()

	// Даём goroutine дойти до блокировки в mock.ProcessMessage.
	time.Sleep(100 * time.Millisecond)

	// /clear должен обработаться сразу, не дожидаясь 5-секундного ответа LLM.
	start := time.Now()
	clearResult := handler.ProcessMessage("/clear", peerID)
	elapsed := time.Since(start)

	if clearResult == "" {
		t.Error("expected non-empty /clear result")
	}
	if elapsed >= 2*time.Second {
		t.Errorf("/clear blocked for %v — должен обрабатываться мгновенно, не дожидаясь LLM (5с)", elapsed)
	}

	// Активный LLM-запрос должен быть отменён и завершиться.
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

	// Сессия должна быть очищена.
	if sess := mock.GetSession(peerID); sess == nil {
		t.Fatal("session should exist after /clear")
	} else if sess.HistoryLength() != 0 {
		t.Errorf("expected empty history after /clear, got %d", sess.HistoryLength())
	}
}
