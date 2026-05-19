package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

func assertJSONParse(t *testing.T, input string, expected XMLParseResult) {
	t.Helper()
	result := ParseJSONToolCalls(input)
	if !eq(result, expected) {
		t.Errorf("ParseJSONToolCalls(%q) = %+v, want %+v", input, result, expected)
	}
}

func TestParseJSONToolCalls_Empty(t *testing.T) {
	assertJSONParse(t, "", XMLParseResult{})
}

func TestParseJSONToolCalls_PlainText(t *testing.T) {
	assertJSONParse(t, "Just some text", XMLParseResult{Content: "Just some text"})
}

func TestParseJSONToolCalls_ShellExecute(t *testing.T) {
	input := `{"name": "shell_execute", "arguments": {"command": "msg test", "timeout": 30}}`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "shell_execute",
				Args: map[string]string{
					"command": "msg test",
					"timeout": "30",
				},
			},
		},
	}
	assertJSONParse(t, input, expected)
}

func TestParseJSONToolCalls_TimeGet(t *testing.T) {
	input := `{"name": "time_get", "arguments": {}}`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{Name: "time_get", Args: map[string]string{}},
		},
	}
	assertJSONParse(t, input, expected)
}

func TestParseJSONToolCalls_TextBeforeAndAfter(t *testing.T) {
	input := `Let me run this command: {"name": "shell_execute", "arguments": {"command": "ls -la", "timeout": 10}} Done!`
	expected := XMLParseResult{
		Content: "Let me run this command:  Done!",
		ToolCalls: []XMLToolCall{
			{
				Name: "shell_execute",
				Args: map[string]string{
					"command": "ls -la",
					"timeout": "10",
				},
			},
		},
	}
	assertJSONParse(t, input, expected)
}

func TestParseJSONToolCalls_MultipleJSON(t *testing.T) {
	input := `First: {"name": "time_get", "arguments": {}} Second: {"name": "calc", "arguments": {"expression": "2 + 2"}}`
	expected := XMLParseResult{
		Content: "First:  Second: ",
		ToolCalls: []XMLToolCall{
			{Name: "time_get", Args: map[string]string{}},
			{Name: "calc", Args: map[string]string{"expression": "2 + 2"}},
		},
	}
	assertJSONParse(t, input, expected)
}

func TestParseJSONToolCalls_NotAToolCall(t *testing.T) {
	// Plain JSON object that doesn't have name+arguments
	input := `Some text {"foo": "bar"} more text`
	expected := XMLParseResult{
		Content: `Some text {"foo": "bar"} more text`,
	}
	assertJSONParse(t, input, expected)
}

func TestParseJSONToolCalls_MixedContent(t *testing.T) {
	input := `Before. {"name": "web_search", "arguments": {"query": "test"}} After.`
	expected := XMLParseResult{
		Content: "Before.  After.",
		ToolCalls: []XMLToolCall{
			{Name: "web_search", Args: map[string]string{"query": "test"}},
		},
	}
	assertJSONParse(t, input, expected)
}

func TestParseJSONToolCalls_FileWrite(t *testing.T) {
	input := `{"name": "file_write", "arguments": {"path": "/tmp/test.txt", "content": "hello"}}`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "file_write",
				Args: map[string]string{
					"path":    "/tmp/test.txt",
					"content": "hello",
				},
			},
		},
	}
	assertJSONParse(t, input, expected)
}

func TestParseJSONToolCalls_WebFetch(t *testing.T) {
	input := `{"name": "web_fetch", "arguments": {"url": "https://example.com", "method": "GET"}}`
	expected := XMLParseResult{
		ToolCalls: []XMLToolCall{
			{
				Name: "web_fetch",
				Args: map[string]string{
					"url":    "https://example.com",
					"method": "GET",
				},
			},
		},
	}
	assertJSONParse(t, input, expected)
}

func TestParseJSONToolCalls_NestedJSONInArgs(t *testing.T) {
	input := `{"name": "file_write", "arguments": {"path": "/tmp/test.json", "content": "{\"key\": \"value\"}"}}`
	result := ParseJSONToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Name != "file_write" {
		t.Errorf("expected file_write, got %q", tc.Name)
	}
	if tc.Args["path"] != "/tmp/test.json" {
		t.Errorf("expected /tmp/test.json, got %q", tc.Args["path"])
	}
	if tc.Args["content"] != `{"key": "value"}` {
		t.Errorf("expected content with JSON, got %q", tc.Args["content"])
	}
}

func TestParseJSONToolCalls_NumberArgs(t *testing.T) {
	input := `{"name": "shell_execute", "arguments": {"command": "sleep 5", "timeout": 30}}`
	result := ParseJSONToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Name != "shell_execute" {
		t.Errorf("expected shell_execute, got %q", tc.Name)
	}
	if tc.Args["command"] != "sleep 5" {
		t.Errorf("expected 'sleep 5', got %q", tc.Args["command"])
	}
	if tc.Args["timeout"] != "30" {
		t.Errorf("expected '30', got %q", tc.Args["timeout"])
	}
}

func TestParseJSONToolCalls_NoToolCallsPartialJSON(t *testing.T) {
	input := `This is not complete: {"name": "shell_execute", "arguments": {"command": "test"`
	expected := XMLParseResult{
		Content: input,
	}
	assertJSONParse(t, input, expected)
}

func TestParseJSONToolCalls_JsonInCodeBlock(t *testing.T) {
	input := "```json\n{\"name\": \"shell_execute\", \"arguments\": {\"command\": \"ls\"}}\n```"
	expected := XMLParseResult{
		Content: "```json\n\n```",
		ToolCalls: []XMLToolCall{
			{
				Name: "shell_execute",
				Args: map[string]string{"command": "ls"},
			},
		},
	}
	assertJSONParse(t, input, expected)
}

func TestParseJSONToolCalls_BoolArg(t *testing.T) {
	input := `{"name": "web_fetch", "arguments": {"url": "https://example.com", "follow_redirects": true}}`
	result := ParseJSONToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Args["follow_redirects"] != "true" {
		t.Errorf("expected 'true', got %q", tc.Args["follow_redirects"])
	}
}

func TestJSONFallback_Integration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Executed successfully.\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	reg := tools.NewRegistry()
	reg.Register(&tools.ShellExecuteTool{})

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
		EnableTools:    true,
		MaxToolCalls:   5,
	}

	a := NewAgent(config)
	a.RegisterTools(reg)

	responseText := `{"name": "shell_execute", "arguments": {"command": "echo hello", "timeout": 10}}`

	messages := []Message{
		{Role: "user", Content: "run echo hello"},
	}
	s := a.GetSession(99920)

	result, used, err := a.jsonFallback(context.Background(), responseText, messages, s)
	if err != nil {
		t.Fatalf("jsonFallback failed: %v", err)
	}
	if !used {
		t.Fatal("expected jsonFallback to be used")
	}
	if !result.Success {
		t.Fatal("expected success")
	}
	if result.Response == "" {
		t.Fatal("expected non-empty response")
	}
	t.Logf("Response: %s", result.Response)
}
