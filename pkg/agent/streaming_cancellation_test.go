package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestStreamingCancellationInterruptsRead проверяет, что отмена контекста во
// время стриминга реально прерывает чтение ответа LLM и ProcessMessage
// возвращается, а не «висит» на зависшем стриме.
//
// Сценарий: LLM-сервер шлёт неполную SSE-строку (без финального \n) и
// замолкает — bufio.ReadSlice блокируется в ожидании конца строки. Отмена
// контекста (как это делает /clear) должна прервать чтение и вернуть ошибку.
func TestStreamingCancellationInterruptsRead(t *testing.T) {
	// started закрывается, когда сервер получил запрос и отправил неполную
	// строку — значит клиент уже заблокирован в ReadSlice.
	started := make(chan struct{})
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("expected http.Flusher")
			return
		}
		// Неполная data-строка без \n — ReadSlice будет ждать конец строки.
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"partial`)
		flusher.Flush()
		close(started)
		<-release // зависаем, пока тест не отпустит
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 4096
	config.EnableTools = false
	config.EnableCompression = false
	config.EnablePruning = false

	a := NewAgent(config)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	peerID := int64(4242)
	done := make(chan error, 1)
	go func() {
		_, err := a.ProcessMessage(ctx, "hello", peerID)
		done <- err
	}()

	// Ждём, пока запрос дойдёт до зависшего чтения.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive the streaming request")
	}
	// Даём ReadSlice заблокироваться.
	time.Sleep(50 * time.Millisecond)

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected non-nil error after cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ProcessMessage did not return after context cancellation — streaming read is not interrupted")
	}
}

// TestStreamAndCollectCancelNotRetryableNoMislabel проверяет, что отмена
// контекста при отправке запроса (как это делает /clear) НЕ маскируется под
// «LLM server was shutdown or unreachable» и НЕ считается retryable-ошибкой:
// возвращается исходный context.Canceled без повторных попыток.
func TestStreamAndCollectCancelNotRetryableNoMislabel(t *testing.T) {
	var calls int32
	started := make(chan struct{})
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		close(started)
		<-release // держим соединение: клиент заблокирован в Do()
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.RetryDelay = 5 * time.Millisecond
	a := NewAgent(config)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, _, _, _, _, err := a.streamAndCollect(ctx, StreamingConfig{Model: "m", MaxTokens: 100}, nil)
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive the request")
	}
	cancel() // отмена в полёте — как при /clear

	var err error
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("streamAndCollect did not return after context cancellation")
	}
	if err == nil {
		t.Fatal("expected non-nil error after cancel, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected errors.Is(err, context.Canceled), got: %v", err)
	}
	if strings.Contains(err.Error(), "shutdown or unreachable") {
		t.Errorf("cancel must not be mislabeled as server shutdown, got: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("expected exactly 1 request (no retry on cancel), got %d", got)
	}
}

// TestStreamAndCollectCancelDuringRetryDelayReturnsCanceled проверяет, что если
// контекст отменён во время паузы перед ретраем серверной ошибки, возвращается
// сам context.Canceled, а не «LLM request exhausted: <server err>».
func TestStreamAndCollectCancelDuringRetryDelayReturnsCanceled(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "model still loading", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.RetryDelay = 50 * time.Millisecond
	a := NewAgent(config)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, _, _, _, _, err := a.streamAndCollect(ctx, StreamingConfig{Model: "m", MaxTokens: 100}, nil)
		done <- err
	}()

	// Ждём первой попытки — дальше цикл уходит в паузу RetryDelay.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&calls) < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()

	var err error
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("streamAndCollect did not return after cancel during retry delay")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected errors.Is(err, context.Canceled), got: %v", err)
	}
	if strings.Contains(err.Error(), "exhausted") {
		t.Errorf("user cancel must not surface as 'LLM request exhausted', got: %v", err)
	}
}

// TestStreamAndCollectCancelMidStreamPartialContent проверяет, что отмена в
// середине стрима после получения частичного контента (без finish_reason)
// возвращает context.Canceled, а не «успех» с обрезанным ответом.
func TestStreamAndCollectCancelMidStreamPartialContent(t *testing.T) {
	var calls int32
	started := make(chan struct{})
	release := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("expected http.Flusher")
			return
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"part\"}}]}\n\n")
		flusher.Flush()
		close(started)
		<-release // молчим без finish_reason — клиент ждёт продолжение
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	a := NewAgent(config)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		text string
		err  error
	}, 1)
	go func() {
		resp, _, finishReason, toolCalls, _, _, err := a.streamAndCollect(ctx, StreamingConfig{Model: "m", MaxTokens: 100}, nil)
		if finishReason == "" && len(toolCalls) == 0 && err == nil {
			resp = resp + "[no-finish]"
		}
		done <- struct {
			text string
			err  error
		}{resp, err}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not start streaming")
	}
	time.Sleep(50 * time.Millisecond) // даём клиенту прочитать частичный контент
	cancel()

	var got struct {
		text string
		err  error
	}
	select {
	case got = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("streamAndCollect did not return after mid-stream cancellation")
	}
	if !errors.Is(got.err, context.Canceled) {
		t.Errorf("expected errors.Is(err, context.Canceled), got text=%q err=%v", got.text, got.err)
	}
}
