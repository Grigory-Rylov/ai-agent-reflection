package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

const serverOverflowBody = `{"error":{"type":"invalid_request_error","message":"prompt (232869 tokens) + max tokens (32768) exceeds the context (262144); requests are never truncated"}}`

type overflowTransport struct {
	mu        sync.Mutex
	bodies    []string
	failFirst int
}

func (tr *overflowTransport) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	tr.mu.Lock()
	tr.bodies = append(tr.bodies, string(body))
	call := len(tr.bodies)
	tr.mu.Unlock()

	if call <= tr.failFirst {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, serverOverflowBody)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
}

func (tr *overflowTransport) requests() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]string{}, tr.bodies...)
}

type stubSummarizer struct {
	summary string
	err     error
}

func (s *stubSummarizer) Compress(context.Context, *compress.CompressionRequest) (*compress.CompressionResult, error) {
	return &compress.CompressionResult{}, nil
}

func (s *stubSummarizer) Complete(context.Context, string, string, int) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.summary, nil
}

func preflightAgent(t *testing.T, window int, summarizer compress.LLMCompressorInterface, transport http.Handler) *agentImpl {
	t.Helper()
	server := httptest.NewServer(transport)
	t.Cleanup(server.Close)

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = window
	config.EnableTools = false
	config.EnableCompression = true
	config.CompactionKeepRecentTokens = 100
	config.RetryDelay = time.Millisecond

	agent := NewAgent(config)
	agent.compactor = compress.NewCompactor(summarizer)
	return agent
}

func conversation(turns int, size int) *session.Session {
	s := session.NewSession(session.DefaultConfig())
	for i := range turns {
		s.AddUserMessage(fmt.Sprintf("%d-%s", i, strings.Repeat("u", size)))
		s.AddAssistantMessage(fmt.Sprintf("%d-%s", i, strings.Repeat("a", size)))
	}
	return s
}

func assertEveryRequestFits(t *testing.T, bodies []string, window int) {
	t.Helper()
	for i, body := range bodies {
		decoded := decodeRequestBody(t, []byte(body))
		maxTokens, _ := decoded["max_tokens"].(float64)
		promptTokens := len(body) / 4
		if promptTokens+int(maxTokens) > window {
			t.Errorf("request %d: prompt ~%d tokens + max_tokens %d exceeds window %d",
				i, promptTokens, int(maxTokens), window)
		}
	}
}

func TestPreflightCompactsBeforeSendingOversizedRequest(t *testing.T) {
	transport := &overflowTransport{}
	agent := preflightAgent(t, 8_000, &stubSummarizer{summary: "[SUMMARY] compacted history"}, transport)
	tc := agent.newTurnContext(conversation(5, 2_000))

	_, _, err := agent.streamWithOverflowRecovery(context.Background(), tc)
	if err != nil {
		t.Fatalf("streamWithOverflowRecovery: %v", err)
	}

	bodies := transport.requests()
	if len(bodies) != 1 {
		t.Fatalf("requests sent = %d, want 1", len(bodies))
	}
	if !strings.Contains(bodies[0], "compacted history") {
		t.Error("oversized context was sent without compaction")
	}
	assertEveryRequestFits(t, bodies, 8_000)
}

func TestOverflowRecoveryDoesNotResendIdenticalRequest(t *testing.T) {
	transport := &overflowTransport{failFirst: 1}
	summarizer := &stubSummarizer{err: fmt.Errorf("completion request failed: API error: status 400, body: %s", serverOverflowBody)}
	agent := preflightAgent(t, 8_000, summarizer, transport)
	tc := agent.newTurnContext(conversation(2, 2_000))

	_, _, err := agent.streamWithOverflowRecovery(context.Background(), tc)
	if err == nil {
		t.Fatal("expected overflow error to reach the caller")
	}
	if !IsContextOverflowError(err) {
		t.Errorf("returned error should stay the overflow error: %v", err)
	}
	if got := len(transport.requests()); got != 1 {
		t.Errorf("requests sent = %d, want 1 (identical payload must not be resent)", got)
	}
}

func TestOverflowRecoveryRetriesAfterSuccessfulCompaction(t *testing.T) {
	transport := &overflowTransport{failFirst: 1}
	agent := preflightAgent(t, 8_000, &stubSummarizer{summary: "[SUMMARY] compacted history"}, transport)
	tc := agent.newTurnContext(conversation(2, 1_900))

	round, _, err := agent.streamWithOverflowRecovery(context.Background(), tc)
	if err != nil {
		t.Fatalf("recovery should succeed after compaction, got %v", err)
	}
	if round.text != "done" {
		t.Errorf("round text = %q, want %q", round.text, "done")
	}

	bodies := transport.requests()
	if len(bodies) != 2 {
		t.Fatalf("requests sent = %d, want 2", len(bodies))
	}
	if len(bodies[1]) >= len(bodies[0]) {
		t.Errorf("retry payload %d bytes is not smaller than the rejected %d bytes", len(bodies[1]), len(bodies[0]))
	}
	assertEveryRequestFits(t, bodies, 8_000)
}

func TestOverflowRecordsRealPromptTokensWithoutClobberingOutput(t *testing.T) {
	agent := preflightAgent(t, 8_000, &stubSummarizer{summary: "unused"}, &overflowTransport{})
	s := session.NewSession(session.DefaultConfig())
	s.AddUserMessage("question")
	s.AddAssistantMessage("answer")
	s.RecordAssistantUsage(1_000, 250)

	agent.recordOverflowPromptTokens(s, fmt.Errorf("API error: status 400, body: %s", serverOverflowBody))

	if got := s.LastUsageInputTokens(); got != 232_869 {
		t.Errorf("LastUsageInputTokens = %d, want 232869", got)
	}
	last := s.GetHistory()[len(s.GetHistory())-1]
	if last.UsageOutputTokens != 250 {
		t.Errorf("overflow recording clobbered output usage: %d, want 250", last.UsageOutputTokens)
	}

	agent.recordOverflowPromptTokens(s, errors.New("connection refused"))
	if got := s.LastUsageInputTokens(); got != 232_869 {
		t.Errorf("non-overflow error changed recorded usage: %d", got)
	}
}
