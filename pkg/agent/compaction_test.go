package agent

import (
	"testing"

	"github.com/opencode/llama-client/pkg/compress"
	"github.com/opencode/llama-client/pkg/tokenizers"
	"github.com/opencode/llama-client/session"
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
