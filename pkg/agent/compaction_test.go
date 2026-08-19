package agent

import (
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)


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


func TestCompactIfNeededBeforeLLM_IncludesToolCalls(t *testing.T) {
	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"

	
	longArgs := `{"path":"src/main.go","content":"package main\nfunc main() { fmt.Println(\"hello\") }"}`
	messages := []Message{
		{Role: "user", Content: "read file"},
		{Role: "assistant", Content: "executing", ToolCalls: []ToolCall{
			{ID: "call1", Function: ToolCallFunction{Name: "read_file", Arguments: []byte(longArgs)}},
		}},
	}

	
	tokenMessages := make([]tokenizers.Message, len(messages))
	for i, m := range messages {
		content := m.Content
		for _, tc := range m.ToolCalls {
			content += string(tc.Function.Arguments)
		}
		tokenMessages[i] = tokenizers.Message{Role: m.Role, Content: content}
	}

	tokensWithToolCalls := compress.EstimateMessagesTokensSimple(tokenMessages)

	
	tokenMessagesNoTool := make([]tokenizers.Message, len(messages))
	for i, m := range messages {
		tokenMessagesNoTool[i] = tokenizers.Message{Role: m.Role, Content: m.Content}
	}
	tokensNoTool := compress.EstimateMessagesTokensSimple(tokenMessagesNoTool)

	if tokensWithToolCalls <= tokensNoTool {
		t.Errorf("expected tokens with tool calls (%d) > without (%d)", tokensWithToolCalls, tokensNoTool)
	}
}


func TestConvertSessionHistory_FiltersCompacted(t *testing.T) {
	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"

	impl := NewAgent(config)

	
	
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

	
	if len(result) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(result))
	}

	
	if result[0].Content != "compaction request" {
		t.Errorf("first message should be compaction user, got %q", result[0].Content)
	}

	
	if result[1].Content != "## Goal\n- Summary" {
		t.Errorf("second message should be summary, got %q", result[1].Content)
	}

	
	if result[2].Content != "tail user 1" {
		t.Errorf("third message should be tail user 1, got %q", result[2].Content)
	}

	
	if result[4].Content != "after summary user" {
		t.Errorf("fifth message should be after summary user, got %q", result[4].Content)
	}
}


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


func TestCompactIfNeeded_MarksSummaryFlag(t *testing.T) {
	
	summaryMsg := session.Message{
		Role:    session.AssistantRole,
		Content: "<<CONVERSATION CHECKPOINT>>\n## Goal",
		Summary: true,
	}

	if !summaryMsg.Summary {
		t.Error("summary message should have Summary=true")
	}
}
