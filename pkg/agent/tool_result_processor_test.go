package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)

// ============================================================
// Mock compressor for auto-continue tests
// ============================================================

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

// newAutoContinueTestAgent создаёт агента с маленьким MaxTokens и моковым компактором.
func newAutoContinueTestAgent(t *testing.T) *agentImpl {
	t.Helper()
	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"
	config.MaxTokens = 50 // маленький лимит — компактизация сработает сразу

	agent := NewAgent(config)
	agent.compactor = compress.NewCompactor(&mockAutoContinueCompressor{})
	return agent
}

// ============================================================
// Test 1: compactIfNeededBeforeLLM (proactive path) — NO auto-continue
// Proactive compaction doesn't add "Continue..." because tool results
// are still in context and the model continues naturally.
// ============================================================

func TestCompactIfNeededBeforeLLM_NoAutoContinue(t *testing.T) {
	agent := newAutoContinueTestAgent(t)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test system prompt")

	// Наполняем сессию сообщениями, чтобы был контекст для компактизации.
	for i := 0; i < 10; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("user message %d: ", i), 20))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("assistant reply %d: ", i), 20))
	}

	// Создаём tool calls/results (как в processToolResults)
	longArgs := `{"path":"src/main.go","content":"package main\nfunc main() { fmt.Println(\"hello\") }"}`
	toolCalls := []ToolCall{
		{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "file_read", Arguments: []byte(longArgs)}},
	}
	toolResults := []ToolCallResult{
		{ToolCallID: "call_1", ToolName: "file_read", Content: strings.Repeat("result data ", 20)},
	}

	// Добавляем assistant+tool в сессию (как делает processToolResults до compactIfNeededBeforeLLM)
	sessionToolCalls := []sess.MsgToolCall{
		{ID: "call_1", Type: "function", Function: sess.MsgToolCallFunc{Name: "file_read", Arguments: longArgs}},
	}
	s.AddAssistantMessageWithToolCalls("executing tool", sessionToolCalls)
	for _, tr := range toolResults {
		s.AddToolMessage(tr.ToolCallID, tr.ToolName, tr.Content)
	}

	// Формируем messages (как buildToolResultMessages)
	messages := agent.buildToolResultMessages(
		[]Message{{Role: "user", Content: "read the file"}},
		"executing tool", toolCalls, toolResults,
	)

	ctx := context.Background()

	// Вызываем compactIfNeededBeforeLLM (проактивный путь)
	resultMessages := agent.compactIfNeededBeforeLLM(ctx, s, messages, "executing tool", toolCalls, toolResults)

	// Проверяем, что результат не пустой
	if len(resultMessages) == 0 {
		t.Fatal("expected non-empty result messages after compaction")
	}

	// Проверяем, что в сессии НЕ появилось CompactionAutoContinueText (проактивный путь его не добавляет).
	history := s.GetHistory()
	for _, msg := range history {
		if msg.Role == sess.UserRole && msg.Content == tokenizers.CompactionAutoContinueText {
			t.Error("CompactionAutoContinueText should NOT appear in proactive compaction path")
		}
	}
}

// findLastUserRoleMessage возвращает контент последнего user-сообщения.
func findLastUserRoleMessage(msgs []sess.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == sess.UserRole {
			return msgs[i].Content
		}
	}
	return ""
}

// ============================================================
// Test 2: processToolResults reactive overflow → CompactionOverflowContinueText
// ============================================================

func TestProcessToolResults_OverflowRecovery_AutoContinueText(t *testing.T) {
	var callCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			// Первый вызов — симулируем context overflow (SSE error)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"error":{"message":"prompt exceeds context length","code":"context_length_exceeded"}}

`)
			return
		}
		// Второй вызов — успешный ответ после компактизации.
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"Done with the task."}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

[DONE]
`)
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 50 // маленький лимит для компактизации
	config.RetryDelay = 5 * time.Millisecond

	agent := NewAgent(config)
	agent.compactor = compress.NewCompactor(&mockAutoContinueCompressor{})

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test system prompt")
	a := agent
	// Заменяем сессию в мапе (для agentPrefix и т.д.)
	a.mu.Lock()
	a.sessions[1] = s
	a.mu.Unlock()

	// Наполняем сессию сообщениями для компактизации.
	for i := 0; i < 10; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("message %d: ", i), 30))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("reply %d: ", i), 30))
	}

	// Создаём tool calls/results (уже добавлены в сессию выше, здесь — для processToolResults)
	toolCalls := []ToolCall{
		{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "shell_execute", Arguments: []byte(`{"command":"ls"}`)}},
	}
	toolResults := []ToolCallResult{
		{ToolCallID: "call_1", ToolName: "shell_execute", Content: "file.txt\n"},
	}

	ctx := context.Background()
	executed := make(map[string]bool)

	// Вызываем processToolResults. Он добавит assistant+tool в сессию, вызовет compactIfNeededBeforeLLM,
	// затем streamAndCollect → ошибка overflow → реактивная компактизация → авто-продолжение.
	result, err := a.processToolResults(ctx, []Message{{Role: "user", Content: "do something"}}, "", toolCalls, toolResults, s, executed)

	if err != nil {
		t.Fatalf("processToolResults returned error: %v", err)
	}
	t.Logf("processToolResults result: %q", result)

	// Проверяем, что в сессии появилось сообщение CompactionOverflowContinueText.
	history := s.GetHistory()
	foundOverflowContinue := false
	for _, msg := range history {
		if msg.Role == sess.UserRole && msg.Content == tokenizers.CompactionOverflowContinueText {
			foundOverflowContinue = true
			break
		}
	}
	if !foundOverflowContinue {
		t.Errorf("expected user message with CompactionOverflowContinueText in session after reactive overflow recovery")
		for i, msg := range history {
			t.Logf("  [%d] role=%s content=%q (summary=%v compacted=%v)", i, msg.Role, msg.Content, msg.Summary, msg.Compacted)
		}
	}

	// Проверяем, что сервер был вызван дважды: первый раз — overflow, второй — успех.
	if got := atomic.LoadInt32(&callCount); got != 2 {
		t.Errorf("expected 2 LLM calls (overflow + retry), got %d", got)
	}
}

// ============================================================
// Test 3: compactIfNeededBeforeLLM no overflow — messages unchanged
// ============================================================

func TestCompactIfNeededBeforeLLM_NoOverflow(t *testing.T) {
	agent := newAutoContinueTestAgent(t)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test")

	// Маленький контекст — не переполнит.
	s.AddUserMessage("hello")

	messages := []Message{
		{Role: "user", Content: "hello"},
	}

	ctx := context.Background()
	result := agent.compactIfNeededBeforeLLM(ctx, s, messages, "", nil, nil)

	// Результат должен быть идентичен исходным сообщениям.
	if len(result) != len(messages) {
		t.Fatalf("expected %d messages, got %d", len(messages), len(result))
	}
	if result[0].Content != "hello" {
		t.Errorf("expected content 'hello', got %q", result[0].Content)
	}

	// В сессии НЕ должно быть CompactionAutoContinueText.
	history := s.GetHistory()
	for _, msg := range history {
		if msg.Role == sess.UserRole && msg.Content == tokenizers.CompactionAutoContinueText {
			t.Error("CompactionAutoContinueText should NOT appear when there is no overflow")
		}
	}
}

// ============================================================
// Test 4: processToolResults without overflow — no auto-continue text
// ============================================================

func TestProcessToolResults_NoOverflow_NoAutoContinue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"OK done."}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

[DONE]
`)
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 10000 // большой лимит — нет переполнения
	config.RetryDelay = 5 * time.Millisecond

	agent := NewAgent(config)
	// compactor nil → компактизация выключена.

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test")
	a := agent
	a.mu.Lock()
	a.sessions[1] = s
	a.mu.Unlock()

	s.AddUserMessage("do it")

	toolCalls := []ToolCall{
		{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "calc", Arguments: []byte(`{"expression":"2+2"}`)}},
	}
	toolResults := []ToolCallResult{
		{ToolCallID: "call_1", ToolName: "calc", Content: "4"},
	}

	ctx := context.Background()
	executed := make(map[string]bool)

	result, err := a.processToolResults(ctx, []Message{{Role: "user", Content: "do it"}}, "", toolCalls, toolResults, s, executed)
	if err != nil {
		t.Fatalf("processToolResults error: %v", err)
	}
	t.Logf("result: %q", result)

	// Без компактизации не должно быть авто-продолжения.
	history := s.GetHistory()
	for _, msg := range history {
		if msg.Role == sess.UserRole && (msg.Content == tokenizers.CompactionAutoContinueText || msg.Content == tokenizers.CompactionOverflowContinueText) {
			t.Error("auto-continue text should NOT appear when compactor is nil")
		}
	}
}

// ============================================================
// Test 5: shouldAddAutoContinue — защита от дублирования при повторной компактизации
// ============================================================

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

// ============================================================
// Test 6: shouldAddAutoContinue guard prevents duplicates (reactive path only)
// ============================================================

func TestShouldAddAutoContinue_GuardWorks(t *testing.T) {
	// Симулируем reactive overflow recovery: auto-continue добавлен, потом снова compact + reactive.
	s := sess.NewSession(sess.DefaultConfig())
	s.AddUserMessage("original prompt")
	s.AddAssistantMessage("doing work...")

	// Первая реактивная компактизация добавила CompactionOverflowContinueText.
	s.AddUserMessage(tokenizers.CompactionOverflowContinueText)

	if shouldAddAutoContinue(s) {
		t.Error("expected false: auto-continue already present with no model response after it")
	}

	// Модель ответила на auto-continue — теперь можно добавить ещё раз.
	s.AddAssistantMessage("continuing...")
	if !shouldAddAutoContinue(s) {
		t.Error("expected true: model responded to previous auto-continue")
	}
}

// countUserMessages возвращает количество user-сообщений с указанным контентом.
func countUserMessages(msgs []sess.Message, content string) int {
	count := 0
	for _, msg := range msgs {
		if msg.Role == sess.UserRole && msg.Content == content {
			count++
		}
	}
	return count
}
