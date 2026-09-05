package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

const longTestReasoning = "The user asked me to analyze the codebase. First I should look at the project structure and the build configuration. " +
	"Then I need to read the key files to understand the data flow between the gateway and the LLM client. " +
	"I will start with the configuration file, then inspect the main entry point and the streaming layer. " +
	"After that I should check the tests to understand the expected behavior before proposing any changes. " +
	"I also need to keep in mind the constraints: no new binaries, only build via build.sh, and functions must stay under fifty lines. " +
	"The plan is to verify assumptions with the search tools first, then implement the minimal change and run the test suite to confirm nothing breaks."

func writeMainCallStream(w http.ResponseWriter, reasoning, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	if reasoning != "" {
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":" + jsonString(reasoning) + "}}]}\n\n"))
	}
	if content != "" {
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":" + jsonString(content) + "}}]}\n\n"))
	}
	w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
	w.Write([]byte("[DONE]\n"))
}

func writeSummaryLLMResponse(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("{\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":" + jsonString(content) + "}}]}"))
}

func newReasoningSummaryAgent(t *testing.T, peerID int64, summarize bool, summaryStatus int, mainReasoning string) (*agentImpl, *int32) {
	t.Helper()

	var summaryCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), reasoningSummaryPrompt) {
			atomic.AddInt32(&summaryCalls, 1)
			if summaryStatus != http.StatusOK {
				http.Error(w, "summary model exploded", summaryStatus)
				return
			}
			writeSummaryLLMResponse(w, "SUMMARY-OK")
			return
		}
		writeMainCallStream(w, mainReasoning, "ANSWER")
	}))
	t.Cleanup(server.Close)

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.RetryDelay = 5 * time.Millisecond
	config.EnableTools = false
	config.EnableCompression = false
	config.SummarizeReasoning = summarize
	config.SessionConfig = session.DefaultConfig()
	config.SessionConfig.PeerID = peerID

	return NewAgent(config), &summaryCalls
}

func lastTwoHistoryMessages(t *testing.T, a *agentImpl, peerID int64) []session.Message {
	t.Helper()
	history := a.GetSession(peerID).GetHistory()
	if len(history) < 2 {
		t.Fatalf("expected at least 2 history messages, got %d", len(history))
	}
	return history[len(history)-2:]
}

func TestProcessMessageStoresReasoningSummaryInHistory(t *testing.T) {
	a, summaryCalls := newReasoningSummaryAgent(t, 424201, true, http.StatusOK, longTestReasoning)

	resp, err := a.ProcessMessage(context.Background(), "hello", 424201)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}
	if resp != "ANSWER" {
		t.Errorf("expected response %q, got %q", "ANSWER", resp)
	}
	if got := atomic.LoadInt32(summaryCalls); got != 1 {
		t.Errorf("expected exactly 1 summary LLM call, got %d", got)
	}

	last := lastTwoHistoryMessages(t, a, 424201)
	if last[0].Role != session.AssistantRole || last[0].Content != "ANSWER" {
		t.Errorf("expected answer message before summary, got role=%s content=%q", last[0].Role, last[0].Content)
	}
	if last[1].Role != session.AssistantRole {
		t.Errorf("expected summary message role assistant, got %s", last[1].Role)
	}
	if !strings.HasPrefix(last[1].Content, "[REASONING SUMMARY]") {
		t.Errorf("expected [REASONING SUMMARY] prefix, got %q", last[1].Content)
	}
	if !strings.Contains(last[1].Content, "SUMMARY-OK") {
		t.Errorf("expected LLM summary text in stored message, got %q", last[1].Content)
	}
}

func TestProcessMessageNoReasoningSummaryWhenDisabled(t *testing.T) {
	a, summaryCalls := newReasoningSummaryAgent(t, 424202, false, http.StatusOK, longTestReasoning)

	if _, err := a.ProcessMessage(context.Background(), "hello", 424202); err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}
	if got := atomic.LoadInt32(summaryCalls); got != 0 {
		t.Errorf("expected no summary LLM call when flag is off, got %d", got)
	}
	for _, m := range a.GetSession(424202).GetHistory() {
		if strings.Contains(m.Content, "[REASONING SUMMARY]") {
			t.Fatalf("unexpected summary message in history: %q", m.Content)
		}
	}
}

func TestProcessMessageSkipsShortReasoningSummary(t *testing.T) {
	a, summaryCalls := newReasoningSummaryAgent(t, 424203, true, http.StatusOK, "short reasoning")

	if _, err := a.ProcessMessage(context.Background(), "hello", 424203); err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}
	if got := atomic.LoadInt32(summaryCalls); got != 0 {
		t.Errorf("expected no summary LLM call for short reasoning, got %d", got)
	}
	for _, m := range a.GetSession(424203).GetHistory() {
		if strings.Contains(m.Content, "[REASONING SUMMARY]") {
			t.Fatalf("unexpected summary message in history: %q", m.Content)
		}
	}
}

func TestProcessMessageFallsBackToRawReasoningWhenSummaryFails(t *testing.T) {
	a, summaryCalls := newReasoningSummaryAgent(t, 424204, true, http.StatusBadGateway, longTestReasoning)

	if _, err := a.ProcessMessage(context.Background(), "hello", 424204); err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}
	if got := atomic.LoadInt32(summaryCalls); got != 1 {
		t.Errorf("expected 1 summary LLM attempt, got %d", got)
	}

	last := lastTwoHistoryMessages(t, a, 424204)
	if !strings.HasPrefix(last[1].Content, "[REASONING SUMMARY]") {
		t.Fatalf("expected fallback summary message, got %q", last[1].Content)
	}
	if !strings.Contains(last[1].Content, "data flow") {
		t.Errorf("expected raw reasoning fragment in fallback summary, got %q", last[1].Content)
	}
}

func TestProcessMessageWritesReasoningSummaryDebugDumps(t *testing.T) {
	base := t.TempDir()
	oldBase := tools.BaseDir
	tools.BaseDir = base
	t.Cleanup(func() { tools.BaseDir = oldBase })

	a, summaryCalls := newReasoningSummaryAgent(t, 424205, true, http.StatusOK, longTestReasoning)
	a.config.Debug = true

	if _, err := a.ProcessMessage(context.Background(), "hello", 424205); err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}
	if got := atomic.LoadInt32(summaryCalls); got != 1 {
		t.Fatalf("expected 1 summary LLM call, got %d", got)
	}

	debugDir := filepath.Join(base, "debug")
	for _, name := range []string{"debug_summary_prompt.txt", "debug_summary_response.txt"} {
		path := filepath.Join(debugDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("expected debug dump %s to exist: %v", name, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("expected non-empty debug dump %s", name)
		}
	}

	promptData, _ := os.ReadFile(filepath.Join(debugDir, "debug_summary_prompt.txt"))
	if !strings.Contains(string(promptData), "condense a language model's private chain-of-thought") {
		t.Errorf("expected summary system prompt in debug dump")
	}
}