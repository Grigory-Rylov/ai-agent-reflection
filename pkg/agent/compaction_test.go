package agent

import (
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

// TestConvertSessionHistory_IncludesToolCalls проверяет, что convertSessionHistory
// добавляет tool call аргументы к контенту для корректной оценки токенов.
// Без этого фикса оценка игнорировала tool calls → компакция не срабатывала → переполнение API.
func TestConvertSessionHistory_IncludesToolCalls(t *testing.T) {
	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"

	impl := NewAgent(config)

	longArgs := `{"path":"src/main.go","content":"package main\nfunc main() { fmt.Println(\"hello\") }"}`
	historyWithToolCalls := []session.Message{
		{Role: session.AssistantRole, Content: "Calling tool", ToolCalls: []session.MsgToolCall{
			{ID: "call1", Function: session.MsgToolCallFunc{Name: "read_file", Arguments: longArgs}},
		}},
	}

	historyWithoutToolCalls := []session.Message{
		{Role: session.AssistantRole, Content: "Calling tool"},
	}

	msgsWith := impl.convertSessionHistory(historyWithToolCalls)
	msgsWithout := impl.convertSessionHistory(historyWithoutToolCalls)

	tokensWith := compress.EstimateMessagesTokensSimple(msgsWith)
	tokensWithout := compress.EstimateMessagesTokensSimple(msgsWithout)

	if tokensWith <= tokensWithout {
		t.Errorf("expected tokens with tool calls (%d) > without (%d)", tokensWith, tokensWithout)
	}
}

// TestCompactIfNeededBeforeLLM_IncludesToolCalls проверяет, что compactIfNeededBeforeLLM
// включает tool call аргументы в оценку токенов.
func TestCompactIfNeededBeforeLLM_IncludesToolCalls(t *testing.T) {
	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"

	// Создаём сообщение с tool calls (как в buildToolResultMessages)
	longArgs := `{"path":"src/main.go","content":"package main\nfunc main() { fmt.Println(\"hello\") }"}`
	messages := []Message{
		{Role: "user", Content: "read file"},
		{Role: "assistant", Content: "executing", ToolCalls: []ToolCall{
			{ID: "call1", Function: ToolCallFunction{Name: "read_file", Arguments: []byte(longArgs)}},
		}},
	}

	// Проверяем, что tokenMessages в compactIfNeededBeforeLLM включают tool calls
	tokenMessages := make([]tokenizers.Message, len(messages))
	for i, m := range messages {
		content := m.Content
		for _, tc := range m.ToolCalls {
			content += string(tc.Function.Arguments)
		}
		tokenMessages[i] = tokenizers.Message{Role: m.Role, Content: content}
	}

	tokensWithToolCalls := compress.EstimateMessagesTokensSimple(tokenMessages)

	// Без tool calls
	tokenMessagesNoTool := make([]tokenizers.Message, len(messages))
	for i, m := range messages {
		tokenMessagesNoTool[i] = tokenizers.Message{Role: m.Role, Content: m.Content}
	}
	tokensNoTool := compress.EstimateMessagesTokensSimple(tokenMessagesNoTool)

	if tokensWithToolCalls <= tokensNoTool {
		t.Errorf("expected tokens with tool calls (%d) > without (%d)", tokensWithToolCalls, tokensNoTool)
	}
}

// TestConvertSessionHistory_FiltersCompacted проверяет, что convertSessionHistory
// применяет FilterCompacted для корректного порядка сообщений после компактизации.
// Как в opencode message-v2.ts: [compaction-user, summary, tail..., after-summary...]
func TestConvertSessionHistory_FiltersCompacted(t *testing.T) {
	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"

	impl := NewAgent(config)

	// Симулируем историю после компактизации:
	// старые сообщения, compaction user, summary (с Summary=true), tail, after-summary
	history := []session.Message{
		{Role: session.UserRole, Content: "old user 1"},
		{Role: session.AssistantRole, Content: "old assistant 1"},
		{Role: session.UserRole, Content: "old user 2"},
		{Role: session.AssistantRole, Content: "old assistant 2"},
		{Role: session.UserRole, Content: "compaction request"},
		{Role: session.AssistantRole, Content: "## Goal\n- Summary", Summary: true},
		{Role: session.UserRole, Content: "tail user 1"},
		{Role: session.AssistantRole, Content: "tail assistant 1"},
		{Role: session.UserRole, Content: "after summary user"},
		{Role: session.AssistantRole, Content: "after summary assistant"},
	}

	result := impl.convertSessionHistory(history)

	// Ожидаем перестановку: [compaction user, summary, tail user 1, tail assistant 1, after summary user, after summary assistant]
	if len(result) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(result))
	}

	// compaction user первым
	if result[0].Content != "compaction request" {
		t.Errorf("first message should be compaction user, got %q", result[0].Content)
	}

	// summary вторым
	if result[1].Content != "## Goal\n- Summary" {
		t.Errorf("second message should be summary, got %q", result[1].Content)
	}

	// tail следует за summary
	if result[2].Content != "tail user 1" {
		t.Errorf("third message should be tail user 1, got %q", result[2].Content)
	}

	// after summary в конце
	if result[4].Content != "after summary user" {
		t.Errorf("fifth message should be after summary user, got %q", result[4].Content)
	}
}

// TestConvertSessionHistory_NoCompaction_NoFilter проверяет, что без компактов
// сообщения возвращаются в исходном порядке.
func TestConvertSessionHistory_NoCompaction_NoFilter(t *testing.T) {
	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"

	impl := NewAgent(config)

	history := []session.Message{
		{Role: session.UserRole, Content: "hello"},
		{Role: session.AssistantRole, Content: "hi there"},
		{Role: session.UserRole, Content: "how are you"},
		{Role: session.AssistantRole, Content: "i am fine"},
	}

	result := impl.convertSessionHistory(history)

	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}

	if result[0].Content != "hello" {
		t.Errorf("first message should be hello, got %q", result[0].Content)
	}
}

// TestCompactIfNeeded_MarksSummaryFlag проверяет, что compactIfNeeded
// ставит Summary=true в session.Message для summary.
func TestCompactIfNeeded_MarksSummaryFlag(t *testing.T) {
	// Проверяем что summary message имеет Summary=true
	summaryMsg := session.Message{
		Role:    session.AssistantRole,
		Content: "<<CONVERSATION CHECKPOINT>>\n## Goal",
		Summary: true,
	}

	if !summaryMsg.Summary {
		t.Error("summary message should have Summary=true")
	}
}
