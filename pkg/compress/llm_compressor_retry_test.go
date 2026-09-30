package compress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
)

func TestIsTransientSummarizationError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"connection refused", fmt.Errorf("send request: %w", syscall.ECONNREFUSED), true},
		{"stream eof", fmt.Errorf("read stream: %w", io.EOF), true},
		{"unexpected eof", fmt.Errorf("read stream: %w", io.ErrUnexpectedEOF), true},
		{"api status error", errors.New("API error: status 400"), false},
		{"marshal error", errors.New("marshal request: boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientSummarizationError(context.Background(), tc.err); got != tc.want {
				t.Fatalf("isTransientSummarizationError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestCompleteRetriesTransientThenSucceeds(t *testing.T) {
	prev := summarizationRetryDelay
	summarizationRetryDelay = 0
	defer func() { summarizationRetryDelay = prev }()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				return
			}
			if conn, _, err := hj.Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewLLMCompressor(srv.URL, "m", 0)
	text, err := c.Complete(context.Background(), "sys", "usr", 100)
	if err != nil {
		t.Fatalf("Complete after transient error returned %v", err)
	}
	if text != "recovered" {
		t.Fatalf("text = %q, want %q", text, "recovered")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("server calls = %d, want 2", got)
	}
}
