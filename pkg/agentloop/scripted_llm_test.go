package agentloop

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// llmCall фиксирует один LLM-запрос, сделанный в ходе восстановления цепочки.
type llmCall struct {
	agent string // "reviewer", "worker" или "lead"
	body  string // сырое тело запроса
}

// scriptedLLM возвращает httptest-сервер, который отвечает на запросы
// скриптовыми SSE-ответами, выбирая ответ по содержимому тела запроса.
// Так имитируется «мок ответов LLM» без реальной модели.
//
// Маршрутизация:
//   - body содержит WORKER_RESULT  → это lead (ему пришёл результат воркера)
//   - body содержит REVIEWER_RESULT → это worker (ему пришёл результат ревьюера)
//   - иначе                        → это reviewer (самый глубокий, восстанавливается первым)
func scriptedLLM(t *testing.T) (*httptest.Server, func() []llmCall, func() bool, func() int) {
	t.Helper()

	var mu sync.Mutex
	var calls []llmCall
	var inFlight, maxInflight int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("mock LLM: read body: %v", err)
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}

		cur := atomic.AddInt32(&inFlight, 1)
		defer atomic.AddInt32(&inFlight, -1)
		mu.Lock()
		if cur > maxInflight {
			maxInflight = cur
		}
		mu.Unlock()

		agent := "reviewer"
		switch {
		case strings.Contains(string(body), "WORKER_RESULT"):
			agent = "lead"
		case strings.Contains(string(body), "REVIEWER_RESULT"):
			agent = "worker"
		}

		mu.Lock()
		calls = append(calls, llmCall{agent: agent, body: string(body)})
		mu.Unlock()

		var content string
		switch agent {
		case "lead":
			content = "LEAD_FINAL"
		case "worker":
			content = "WORKER_RESULT"
		default:
			content = "REVIEWER_RESULT"
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", content)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "[DONE]\n")
	}))

	record := func() []llmCall {
		mu.Lock()
		defer mu.Unlock()
		out := make([]llmCall, len(calls))
		copy(out, calls)
		return out
	}
	hadConcurrency := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return maxInflight > 1
	}
	chatCount := func() int {
		return len(record())
	}
	return server, record, hadConcurrency, chatCount
}
