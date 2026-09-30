package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type mockAutoContinueCompressor struct {
	compressFunc func(ctx context.Context, req *compress.CompressionRequest) (*compress.CompressionResult, error)
}

func (m *mockAutoContinueCompressor) Compress(ctx context.Context, req *compress.CompressionRequest) (*compress.CompressionResult, error) {
	if m.compressFunc != nil {
		return m.compressFunc(ctx, req)
	}
	return &compress.CompressionResult{
		OriginalTokens:   100,
		CompressedTokens: 50,
		CompressionRatio: 0.5,
		CompressedMessages: []tokenizers.Message{
			{Role: "assistant", Content: "[SUMMARY] compacted conversation"},
		},
		Summary:      "[SUMMARY] compacted conversation",
		CompressedAt: time.Now(),
	}, nil
}

func (m *mockAutoContinueCompressor) Complete(ctx context.Context, systemPrompt, userPrompt string, maxTokens int) (string, error) {
	return "[SUMMARY] compacted conversation", nil
}

func newAutoContinueTestAgent(t *testing.T) *agentImpl {
	t.Helper()
	return newCompactionTestAgent(t, 50)
}

func newCompactionTestAgent(t *testing.T, maxTokens int) *agentImpl {
	t.Helper()
	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"
	config.MaxTokens = maxTokens
	config.CompactionKeepRecentTokens = 25
	agent := NewAgent(config)
	agent.compactor = compress.NewCompactor(&mockAutoContinueCompressor{})
	return agent
}

func TestPrepareContext_ProactiveCompactionAddsAutoContinueNotOverflow(t *testing.T) {
	agent := newCompactionTestAgent(t, 50)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test system prompt")
	for i := range 10 {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("user message %d: ", i), 20))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("assistant reply %d: ", i), 20))
	}

	messages, err := agent.prepareContext(context.Background(), s)
	if err != nil {
		t.Fatalf("prepareContext: %v", err)
	}
	if len(messages) == 0 {
		t.Fatal("expected non-empty messages after proactive compaction")
	}

	history := s.GetHistory()
	hasSummary := false
	for _, msg := range history {
		if msg.Summary {
			hasSummary = true
		}
		if msg.Role == sess.UserRole && msg.Content == tokenizers.CompactionOverflowContinueText {
			t.Error("CompactionOverflowContinueText should NOT appear in proactive compaction path")
		}
	}
	if !hasSummary {
		t.Error("expected summary message in history after proactive compaction")
	}
	if n := countUserMessages(history, tokenizers.CompactionAutoContinueText); n != 1 {
		t.Errorf("expected exactly 1 CompactionAutoContinueText in proactive compaction path, got %d", n)
	}
}

func TestPrepareContext_NoCompactionWhenContextSmall(t *testing.T) {
	agent := newCompactionTestAgent(t, 100000)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test")
	s.AddUserMessage("hello")

	messages, err := agent.prepareContext(context.Background(), s)
	if err != nil {
		t.Fatalf("prepareContext: %v", err)
	}

	hasUser := false
	for _, m := range messages {
		if m.Role == "user" && m.Content == "hello" {
			hasUser = true
		}
	}
	if !hasUser {
		t.Error("expected user message 'hello' in prepared context")
	}

	for _, msg := range s.GetHistory() {
		if msg.Summary {
			t.Error("no summary message expected when context is small")
		}
		if msg.Role == sess.UserRole && (msg.Content == tokenizers.CompactionAutoContinueText ||
			msg.Content == tokenizers.CompactionOverflowContinueText) {
			t.Error("no compaction continue text expected when context is small")
		}
	}
}

func TestRunTurn_OverflowRecovery_AddsOverflowContinueText(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			fmt.Fprint(w, `data: {"error":{"message":"prompt exceeds context length","code":"context_length_exceeded"}}`+"\n\n")
			return
		}
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"Done with the task."}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 100000
	config.RetryDelay = 5 * time.Millisecond

	agent := NewAgent(config)
	agent.compactor = compress.NewCompactor(&mockAutoContinueCompressor{})

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test system prompt")
	s.AddUserMessage("do something")
	for i := range 10 {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("message %d: ", i), 30))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("reply %d: ", i), 30))
	}

	result, err := agent.runTurn(context.Background(), s)
	if err != nil {
		t.Fatalf("runTurn returned error: %v", err)
	}
	if result != "Done with the task." {
		t.Errorf("unexpected final result %q", result)
	}

	foundOverflowContinue := false
	for _, msg := range s.GetHistory() {
		if msg.Role == sess.UserRole && msg.Content == tokenizers.CompactionOverflowContinueText {
			foundOverflowContinue = true
			break
		}
	}
	if !foundOverflowContinue {
		t.Error("expected user message with CompactionOverflowContinueText after reactive overflow recovery")
	}

	if got := callCount.Load(); got != 2 {
		t.Errorf("expected 2 LLM calls (overflow + retry), got %d", got)
	}
}

func TestRunTurn_NoOverflow_NoAutoContinueText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"OK done."},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 100000
	config.RetryDelay = 5 * time.Millisecond

	agent := NewAgent(config)
	agent.compactor = nil

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test")
	s.AddUserMessage("do it")

	result, err := agent.runTurn(context.Background(), s)
	if err != nil {
		t.Fatalf("runTurn error: %v", err)
	}
	if result != "OK done." {
		t.Errorf("unexpected final result %q", result)
	}

	for _, msg := range s.GetHistory() {
		if msg.Role == sess.UserRole && (msg.Content == tokenizers.CompactionAutoContinueText ||
			msg.Content == tokenizers.CompactionOverflowContinueText) {
			t.Error("auto-continue text should NOT appear when compactor is nil")
		}
	}
}

func newBatchTestAgent(t *testing.T) (*agentImpl, *StubToolExecutor) {
	t.Helper()
	a := NewAgent(Config{})
	executor := NewStubToolExecutor(filepath.Join(t.TempDir(), "tools.log"))
	a.SetToolExecutor(executor)
	return a, executor
}

func batchCall(id, name, args string) ToolCall {
	return ToolCall{ID: id, Type: "function", Function: ToolCallFunction{Name: name, Arguments: []byte(args)}}
}

func sessionToolMessages(history []sess.Message) []sess.Message {
	var out []sess.Message
	for _, m := range history {
		if m.Role == sess.ToolRole {
			out = append(out, m)
		}
	}
	return out
}

func TestExecuteAndAppendBatch_AllCallsInOneAssistantMessage(t *testing.T) {
	a, _ := newBatchTestAgent(t)
	s := sess.NewSession(sess.DefaultConfig())
	tc := a.newTurnContext(s)

	calls := []ToolCall{
		batchCall("id_1", "time_get", "{}"),
		batchCall("id_2", "calc", `{"expression":"2+2"}`),
		batchCall("id_3", "dir_list", `{"path":"."}`),
	}
	a.executeAndAppendBatch(context.Background(), tc, "working on it", calls)

	var assistantWithCalls []sess.Message
	for _, m := range s.GetHistory() {
		if m.Role == sess.AssistantRole && len(m.ToolCalls) > 0 {
			assistantWithCalls = append(assistantWithCalls, m)
		}
	}
	if len(assistantWithCalls) != 1 {
		t.Fatalf("expected exactly 1 assistant message holding tool calls, got %d", len(assistantWithCalls))
	}
	if len(assistantWithCalls[0].ToolCalls) != 3 {
		t.Errorf("expected all 3 calls in the single assistant message, got %d", len(assistantWithCalls[0].ToolCalls))
	}
	if assistantWithCalls[0].Content != "working on it" {
		t.Errorf("expected assistant content %q, got %q", "working on it", assistantWithCalls[0].Content)
	}
}

func TestExecuteAndAppendBatch_DuplicateGetsSyntheticResult(t *testing.T) {
	a, executor := newBatchTestAgent(t)
	s := sess.NewSession(sess.DefaultConfig())
	tc := a.newTurnContext(s)

	calls := []ToolCall{
		batchCall("id_1", "calc", `{"expression":"2+2"}`),
		batchCall("id_2", "calc", `{"expression":"2+2"}`),
	}
	a.executeAndAppendBatch(context.Background(), tc, "", calls)

	if got := executor.Count("[TOOL] Call: calc"); got != 1 {
		t.Errorf("duplicate call must not re-execute the tool, got %d executions", got)
	}

	toolMsgs := sessionToolMessages(s.GetHistory())
	if len(toolMsgs) != 2 {
		t.Fatalf("expected 2 tool messages (synthetic duplicate + real result), got %d", len(toolMsgs))
	}

	var dupMsg, realMsg *sess.Message
	for i := range toolMsgs {
		switch toolMsgs[i].ToolCallID {
		case "id_2":
			dupMsg = &toolMsgs[i]
		case "id_1":
			realMsg = &toolMsgs[i]
		}
	}
	if dupMsg == nil || dupMsg.Content != duplicateToolCallNotice {
		t.Errorf("expected synthetic duplicateToolCallNotice for id_2, got %+v", dupMsg)
	}
	if realMsg == nil || !strings.Contains(realMsg.Content, `"stub":true`) {
		t.Errorf("expected stub execution result for id_1, got %+v", realMsg)
	}
}

func TestExecuteAndAppendBatch_CheckpointFiresWithUniqueNames(t *testing.T) {
	a, _ := newBatchTestAgent(t)
	rec := &checkpointRecorder{}
	a.SetCheckpoint(rec.Record)

	s := sess.NewSession(sess.DefaultConfig())
	tc := a.newTurnContext(s)

	calls := []ToolCall{
		batchCall("id_1", "calc", `{"expression":"1+1"}`),
		batchCall("id_2", "time_get", "{}"),
		batchCall("id_3", "calc", `{"expression":"1+1"}`),
	}
	a.executeAndAppendBatch(context.Background(), tc, "", calls)

	got := rec.recorded()
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 checkpoint fire, got %d: %v", len(got), got)
	}
	if got[0] != "calc,time_get" {
		t.Errorf("checkpoint label = %q, want %q", got[0], "calc,time_get")
	}
}

func TestExecuteAndAppendBatch_ResultsAppendedInExecutionOrder(t *testing.T) {
	a, _ := newBatchTestAgent(t)
	s := sess.NewSession(sess.DefaultConfig())
	tc := a.newTurnContext(s)

	calls := []ToolCall{
		batchCall("id_1", "time_get", "{}"),
		batchCall("id_2", "calc", `{"expression":"3*3"}`),
		batchCall("id_3", "dir_list", `{"path":"."}`),
	}
	a.executeAndAppendBatch(context.Background(), tc, "", calls)

	toolMsgs := sessionToolMessages(s.GetHistory())
	if len(toolMsgs) != 3 {
		t.Fatalf("expected 3 tool messages, got %d", len(toolMsgs))
	}
	wantIDs := []string{"id_1", "id_2", "id_3"}
	wantNames := []string{"time_get", "calc", "dir_list"}
	for i, m := range toolMsgs {
		if m.ToolCallID != wantIDs[i] {
			t.Errorf("tool message #%d has ToolCallID %q, want %q", i, m.ToolCallID, wantIDs[i])
		}
		if m.Name != wantNames[i] {
			t.Errorf("tool message #%d has Name %q, want %q", i, m.Name, wantNames[i])
		}
	}
}

func TestShouldAddAutoContinue_NoDuplication(t *testing.T) {
	t.Run("empty session returns true", func(t *testing.T) {
		s := sess.NewSession(sess.DefaultConfig())
		if !shouldAddAutoContinue(s) {
			t.Error("expected shouldAddAutoContinue to return true for empty session")
		}
	})

	t.Run("last user message is regular text returns true", func(t *testing.T) {
		s := sess.NewSession(sess.DefaultConfig())
		s.AddUserMessage("hello world")
		if !shouldAddAutoContinue(s) {
			t.Error("expected shouldAddAutoContinue to return true for regular user message")
		}
	})

	t.Run("last user message is CompactionAutoContinueText returns false", func(t *testing.T) {
		s := sess.NewSession(sess.DefaultConfig())
		s.AddUserMessage(tokenizers.CompactionAutoContinueText)
		if shouldAddAutoContinue(s) {
			t.Error("expected shouldAddAutoContinue to return false when last user message is CompactionAutoContinueText")
		}
	})

	t.Run("last user message is CompactionOverflowContinueText returns false", func(t *testing.T) {
		s := sess.NewSession(sess.DefaultConfig())
		s.AddUserMessage(tokenizers.CompactionOverflowContinueText)
		if shouldAddAutoContinue(s) {
			t.Error("expected shouldAddAutoContinue to return false when last user message is CompactionOverflowContinueText")
		}
	})

	t.Run("auto-continue before tool messages — tool is not user, so auto-continue still last user msg returns false", func(t *testing.T) {
		s := sess.NewSession(sess.DefaultConfig())
		s.AddUserMessage(tokenizers.CompactionAutoContinueText)
		s.AddToolMessage("call_1", "test_tool", "tool result")
		if shouldAddAutoContinue(s) {
			t.Error("expected false: last user message is still CompactionAutoContinueText even with tool messages after it")
		}
	})

	t.Run("user message added after auto-continue — no assistant response → returns false", func(t *testing.T) {
		s := sess.NewSession(sess.DefaultConfig())
		s.AddUserMessage(tokenizers.CompactionAutoContinueText)
		s.AddToolMessage("call_1", "test_tool", "tool result")
		s.AddUserMessage("new user message")
		if shouldAddAutoContinue(s) {
			t.Error("expected false: no assistant response after auto-continue, so it's a duplicate guard")
		}
	})

	t.Run("assistant response after auto-continue → returns true", func(t *testing.T) {
		s := sess.NewSession(sess.DefaultConfig())
		s.AddUserMessage(tokenizers.CompactionAutoContinueText)
		s.AddAssistantMessage("continuing work...")
		if !shouldAddAutoContinue(s) {
			t.Error("expected true: model responded to auto-continue, safe to add new one")
		}
	})
}

func TestShouldAddAutoContinue_GuardWorks(t *testing.T) {
	s := sess.NewSession(sess.DefaultConfig())
	s.AddUserMessage("original prompt")
	s.AddAssistantMessage("doing work...")

	s.AddUserMessage(tokenizers.CompactionOverflowContinueText)

	if shouldAddAutoContinue(s) {
		t.Error("expected false: auto-continue already present with no model response after it")
	}

	s.AddAssistantMessage("continuing...")
	if !shouldAddAutoContinue(s) {
		t.Error("expected true: model responded to previous auto-continue")
	}
}

func countUserMessages(msgs []sess.Message, content string) int {
	count := 0
	for _, msg := range msgs {
		if msg.Role == sess.UserRole && msg.Content == content {
			count++
		}
	}
	return count
}
