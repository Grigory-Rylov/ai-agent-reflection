package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func writeNativeCallEvent(w http.ResponseWriter, id, name, args string) {
	w.Write([]byte("data: {\"choices\": [{\"delta\": {\"tool_calls\": [{\"index\": 0, \"id\": \"" + id + "\", \"type\": \"function\", \"function\": {\"name\": \"" + name + "\", \"arguments\": " + args + "}}]}, \"finish_reason\": null}]}\n\n"))
	w.Write([]byte("data: {\"choices\": [{\"delta\": {}, \"finish_reason\": \"tool_calls\"}]}\n\n"))
}

func writeStopEvent(w http.ResponseWriter, content string) {
	w.Write([]byte("data: {\"choices\": [{\"delta\": {\"content\": " + jsonString(content) + "}, \"finish_reason\": null}]}\n\n"))
	w.Write([]byte("data: {\"choices\": [{\"delta\": {}, \"finish_reason\": \"stop\"}]}\n\n"))
}

func jsonString(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '\\':
			out = append(out, '\\', '\\')
		case '"':
			out = append(out, '\\', '"')
		case '\n':
			out = append(out, '\\', 'n')
		default:
			out = append(out, byte(r))
		}
	}
	return string(append(out, '"'))
}

func newDuplicateScenarioAgent(t *testing.T, peerID int64, handler func(w http.ResponseWriter, reqNum int)) (*agentImpl, *StubToolExecutor, *[]string, *int) {
	t.Helper()
	reqCount := 0
	thinking := &[]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.Header().Set("Content-Type", "text/event-stream")
		handler(w, reqCount)
		w.Write([]byte("[DONE]\n"))
	}))
	t.Cleanup(server.Close)

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      4096,
		Temperature:    0.7,
		EnableTools:    true,
		SessionConfig:  session.DefaultConfig(),
	}
	config.SessionConfig.PeerID = peerID

	a, executor := newTestAgentWithStub(t, config)
	a.toolsRegistry.Register(&tools.ShellExecuteTool{})
	a.SetThinkingCallback(func(peerID int64, content string) error {
		*thinking = append(*thinking, content)
		return nil
	})
	counter := &reqCount
	return a, executor, thinking, counter
}

func countSubstring(list []string, substr string) int {
	n := 0
	for _, s := range list {
		if strings.Contains(s, substr) {
			n++
		}
	}
	return n
}

func TestDuplicateSingleToolCallDoesNotTriggerFalseLoop(t *testing.T) {
	echoArgs := "\"{\\\"command\\\": \\\"echo hello\\\"}\""
	a, executor, thinking, llmCalls := newDuplicateScenarioAgent(t, 99932, func(w http.ResponseWriter, num int) {
		switch num {
		case 1:
			writeNativeCallEvent(w, "call_a", "shell_execute", echoArgs)
		case 2:
			writeNativeCallEvent(w, "call_b", "shell_execute", echoArgs)
		default:
			writeStopEvent(w, "Done.")
		}
	})

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "Test", 99932)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	t.Logf("llmCalls=%d response=%q thinking=%v", *llmCalls, response, *thinking)

	if *llmCalls < 3 {
		t.Errorf("expected turn to continue after duplicate (>=3 LLM calls incl. nudge recovery), got %d", *llmCalls)
	}
	if got := executor.Count("[TOOL] Call: shell_execute"); got != 1 {
		t.Errorf("expected shell_execute executed exactly once, got %d", got)
	}
	if got := countSubstring(*thinking, "[LOOP]"); got != 0 {
		t.Errorf("expected no false [LOOP] alert for a single duplicate response, got %d: %v", got, *thinking)
	}
	if !strings.Contains(response, "Done.") {
		t.Errorf("expected recovery response containing %q, got %q", "Done.", response)
	}
}

func TestPersistentDuplicateLoopAlertsOnceAndStaysBounded(t *testing.T) {
	echoArgs := "\"{\\\"command\\\": \\\"echo hello\\\"}\""
	a, executor, thinking, llmCalls := newDuplicateScenarioAgent(t, 99940, func(w http.ResponseWriter, num int) {
		writeNativeCallEvent(w, "call_a", "shell_execute", echoArgs)
	})

	ctx := context.Background()
	_, err := a.ProcessMessage(ctx, "Test", 99940)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	maxCalls := 1 + 1 + maxDuplicateNudges
	t.Logf("llmCalls=%d thinking=%v", *llmCalls, *thinking)

	if *llmCalls > maxCalls {
		t.Errorf("expected bounded LLM calls (<= %d), got %d", maxCalls, *llmCalls)
	}
	if got := executor.Count("[TOOL] Call: shell_execute"); got != 1 {
		t.Errorf("expected shell_execute executed exactly once, got %d", got)
	}
	if got := countSubstring(*thinking, "getting stuck in a loop"); got != 1 {
		t.Errorf("expected exactly 1 genuine [LOOP] alert for persistent repetition, got %d: %v", got, *thinking)
	}
}

func TestMixedDuplicateAndNewToolCallExecutesNewOne(t *testing.T) {
	echoArgs := "\"{\\\"command\\\": \\\"echo hello\\\"}\""
	pwdArgs := "\"{\\\"command\\\": \\\"pwd\\\"}\""
	a, executor, thinking, llmCalls := newDuplicateScenarioAgent(t, 99941, func(w http.ResponseWriter, num int) {
		switch num {
		case 1:
			writeNativeCallEvent(w, "call_a", "shell_execute", echoArgs)
		case 2:
			w.Write([]byte("data: {\"choices\": [{\"delta\": {\"tool_calls\": [{\"index\": 0, \"id\": \"call_b\", \"type\": \"function\", \"function\": {\"name\": \"shell_execute\", \"arguments\": " + echoArgs + "}}]}, \"finish_reason\": null}]}\n\n"))
			w.Write([]byte("data: {\"choices\": [{\"delta\": {\"tool_calls\": [{\"index\": 1, \"id\": \"call_c\", \"type\": \"function\", \"function\": {\"name\": \"shell_execute\", \"arguments\": " + pwdArgs + "}}]}, \"finish_reason\": null}]}\n\n"))
			w.Write([]byte("data: {\"choices\": [{\"delta\": {}, \"finish_reason\": \"tool_calls\"}]}\n\n"))
		default:
			writeStopEvent(w, "Mixed done.")
		}
	})

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "Test", 99941)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	t.Logf("llmCalls=%d response=%q log=%v", *llmCalls, response, executor.ReadLog())

	if got := executor.Count("[TOOL] Call: shell_execute"); got != 2 {
		t.Errorf("expected 2 executions (echo once + pwd once), got %d", got)
	}
	if !executor.Contains("\"pwd\"") {
		t.Error("expected pwd command to be executed")
	}
	if got := countSubstring(*thinking, "[LOOP]"); got != 0 {
		t.Errorf("expected no [LOOP] alert, got %d", got)
	}
	if !strings.Contains(response, "Mixed done.") {
		t.Errorf("expected %q in response, got %q", "Mixed done.", response)
	}
}
