package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func TestProcessToolResults_EmptyResponseRetries(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if n <= 2 {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":""},"finish_reason":"stop"}]}`+"\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"File read complete. The build.sh script rebuilds the agent binary."},"finish_reason":"stop"}]}`+"\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 4096

	a := NewAgent(config)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("You are a helpful assistant")
	s.AddUserMessage("read the file")

	toolCalls := []ToolCall{
		{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "file_read", Arguments: []byte(`{"path":"build.sh"}`)}},
	}
	toolResults := []ToolCallResult{
		{ToolCallID: "call_1", ToolName: "file_read", Content: "#!/bin/bash\necho hello"},
	}

	result, err := a.processToolResults(context.Background(), []Message{{Role: "user", Content: "read the file"}}, "", toolCalls, toolResults, s, make(map[string]bool))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty response after retries, got empty")
	}

	total := callCount.Load()
	if total < 3 {
		t.Errorf("expected at least 3 LLM calls (2 empty + 1 content), got %d", total)
	}
}

func TestProcessToolResults_EmptyResponseExhaustsRetries(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":""},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 4096

	a := NewAgent(config)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("You are a helpful assistant")
	s.AddUserMessage("read the file")

	toolCalls := []ToolCall{
		{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "file_read", Arguments: []byte(`{"path":"test"}`)}},
	}
	toolResults := []ToolCallResult{
		{ToolCallID: "call_1", ToolName: "file_read", Content: "content"},
	}

	result, err := a.processToolResults(context.Background(), []Message{{Role: "user", Content: "read the file"}}, "", toolCalls, toolResults, s, make(map[string]bool))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty response after exhausting retries, got: %s", result)
	}

	total := callCount.Load()
	if total < 3 {
		t.Errorf("expected at least 3 LLM calls for retries, got %d", total)
	}
}

func TestIsTerminalResponse(t *testing.T) {
	tests := []struct {
		name         string
		responseText string
		hasToolCalls bool
		hasReasoning bool
		want         bool
	}{
		{"has content", "hello world", false, false, true},
		{"empty no tools", "", false, false, false},
		{"empty with tools", "", true, false, true},
		{"whitespace only", "   ", false, false, false},
		{"reasoning only", "", false, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTerminalResponse(tt.responseText, tt.hasToolCalls, tt.hasReasoning)
			if got != tt.want {
				t.Errorf("isTerminalResponse(%q, %v, %v) = %v, want %v", tt.responseText, tt.hasToolCalls, tt.hasReasoning, got, tt.want)
			}
		})
	}
}
