package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
