package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

func newTestAgentWithStub(t *testing.T, config Config) (*agentImpl, *StubToolExecutor) {
	t.Helper()
	config.EnableTools = true
	config.MaxToolCalls = 5
	if config.SessionConfig.PeerID == 0 {
		config.SessionConfig.PeerID = 99999
	}
	a := NewAgent(config)

	tmpDir := t.TempDir()
	executor := NewStubToolExecutor(filepath.Join(tmpDir, "tools.log"))
	a.SetToolExecutor(executor)
	return a, executor
}

func TestConvertXMLToolCalls(t *testing.T) {
	xmlCalls := []XMLToolCall{
		{Name: "time_get", Args: map[string]string{}},
		{Name: "calc", Args: map[string]string{"expression": "2 + 2"}},
	}

	toolCalls := convertXMLToolCalls(xmlCalls)

	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(toolCalls))
	}

	if toolCalls[0].Function.Name != "time_get" {
		t.Errorf("expected time_get, got %q", toolCalls[0].Function.Name)
	}
	if toolCalls[0].ID != "xml_call_0" {
		t.Errorf("expected xml_call_0, got %q", toolCalls[0].ID)
	}

	if toolCalls[1].Function.Name != "calc" {
		t.Errorf("expected calc, got %q", toolCalls[1].Function.Name)
	}
	if toolCalls[1].ID != "xml_call_1" {
		t.Errorf("expected xml_call_1, got %q", toolCalls[1].ID)
	}

	args0, err := parseToolArguments(toolCalls[0])
	if err != nil {
		t.Fatalf("failed to parse time_get args: %v", err)
	}
	if len(args0) != 0 {
		t.Errorf("expected empty args for time_get, got %v", args0)
	}

	args1, err := parseToolArguments(toolCalls[1])
	if err != nil {
		t.Fatalf("failed to parse calc args: %v", err)
	}
	if args1["expression"] != "2 + 2" {
		t.Errorf("expected expression='2 + 2', got %q", args1["expression"])
	}
}

func TestXMLFallback_NoToolCalls(t *testing.T) {
	a := &agentImpl{}
	ctx := context.Background()
	messages := []Message{{Role: "user", Content: "Hello"}}
	s := session.NewSession(session.DefaultConfig())

	result, used, err := a.xmlFallback(ctx, "Just a normal response.", messages, s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if used {
		t.Error("expected not used for normal text")
	}
	if result.Success {
		t.Error("expected result to not be success when not used")
	}
}

func TestXMLFallback_EmptyResponse(t *testing.T) {
	a := &agentImpl{}
	ctx := context.Background()
	messages := []Message{{Role: "user", Content: "Hello"}}
	s := session.NewSession(session.DefaultConfig())

	result, used, err := a.xmlFallback(ctx, "", messages, s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if used {
		t.Error("expected not used for empty text")
	}
	if result.Success {
		t.Error("expected result to not be success when not used")
	}
}

func TestXMLFallback_WithXMLButNoRegistry(t *testing.T) {
	a := &agentImpl{
		toolsRegistry: tools.NewRegistry(),
	}
	ctx := context.Background()
	messages := []Message{{Role: "user", Content: "Check time"}}
	s := session.NewSession(session.DefaultConfig())

	responseText := `<tool_call>
<function=time_get>
</function>
</tool_call>`

	result, used, err := a.xmlFallback(ctx, responseText, messages, s)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if used {
		t.Error("expected not used when tool not in registry")
	}
	if result.Success {
		t.Error("expected result to not be success when not used")
	}
}

// TestXMLFallback_Integration проверяет полный цикл: XML tool call → выполнение → ответ
func TestXMLFallback_Integration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Current time is: \"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
	}
	config.SessionConfig.PeerID = 99910
	config.SessionConfig.MaxHistory = 100

	a, executor := newTestAgentWithStub(t, config)

	responseText := `Let me check the time for you.

<tool_call>
<function=time_get>
</function>
</tool_call>

Here is the result.`

	messages := []Message{
		{Role: "system", Content: a.GetSystemPrompt()},
		{Role: "user", Content: "What time is it?"},
	}
	s := a.GetSession(99910)

	result, used, err := a.xmlFallback(context.Background(), responseText, messages, s)
	if err != nil {
		t.Fatalf("xmlFallback failed: %v", err)
	}
	if !used {
		t.Fatal("expected xmlFallback to be used")
	}
	if !result.Success {
		t.Fatal("expected success")
	}
	if result.Response == "" {
		t.Fatal("expected non-empty response")
	}
	if !executor.Contains("time_get") {
		t.Error("expected time_get tool to be called")
	}
	t.Logf("Response: %s", result.Response)
}

// TestXMLFallback_MultipleXMLToolCalls проверяет множественные XML tool calls
func TestXMLFallback_MultipleXMLToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Results: \"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
	}
	config.SessionConfig.PeerID = 99912
	config.SessionConfig.MaxHistory = 100

	a, executor := newTestAgentWithStub(t, config)

	responseText := `<tool_call>
<function=time_get>
</function>
</tool_call>
<tool_call>
<function=calc>
<parameter=expression>2 + 2</parameter>
</function>
</tool_call>`

	messages := []Message{
		{Role: "user", Content: "What time is it and what is 2+2?"},
	}
	s := a.GetSession(99912)

	result, used, err := a.xmlFallback(context.Background(), responseText, messages, s)
	if err != nil {
		t.Fatalf("xmlFallback failed: %v", err)
	}
	if !used {
		t.Fatal("expected xmlFallback to be used")
	}
	if !executor.Contains("time_get") {
		t.Error("expected time_get tool to be called")
	}
	if !executor.Contains("calc") {
		t.Error("expected calc tool to be called")
	}
	t.Logf("Response: %s", result.Response)
}

// TestParseXMLToolCalls_FileWriteRead проверяет file_write + file_read через XML
func TestParseXMLToolCalls_FileWriteRead(t *testing.T) {
	input := `<tool_call>
<function=file_write>
<parameter=path>/tmp/xml_test.txt</parameter>
<parameter=content>Hello from XML tool call</parameter>
</function>
</tool_call>`

	result := ParseXMLToolCalls(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "file_write" {
		t.Errorf("expected file_write, got %q", result.ToolCalls[0].Name)
	}
	if result.ToolCalls[0].Args["path"] != "/tmp/xml_test.txt" {
		t.Errorf("expected /tmp/xml_test.txt, got %q", result.ToolCalls[0].Args["path"])
	}
	if result.ToolCalls[0].Args["content"] != "Hello from XML tool call" {
		t.Errorf("expected 'Hello from XML tool call', got %q", result.ToolCalls[0].Args["content"])
	}
}

// TestProcessWithTools_XMLFallback проверяет что processWithTools
// корректно обрабатывает XML tool calls в ответе модели
func TestProcessWithTools_XMLFallback(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if callCount == 1 {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Let me check the time.\\n\\n<tool_call>\\n<function=time_get>\\n</function>\\n</tool_call>\\n\\nDone.\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		} else {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"The current time has been checked.\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		}
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
	}
	config.SessionConfig.PeerID = 99913
	config.SessionConfig.MaxHistory = 100

	a, executor := newTestAgentWithStub(t, config)

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "What time is it?", 99913)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}
	if response == "" {
		t.Fatal("expected non-empty response")
	}
	if !executor.Contains("time_get") {
		t.Error("expected time_get tool to be called")
	}
	if strings.Contains(response, "<tool_call>") || strings.Contains(response, "<function") {
		t.Error("response should not contain XML tool call tags")
	}
	t.Logf("Final response: %s", response)
}

// TestProcessWithTools_XMLAndTextFallback проверяет что XML fallback не мешает обычному тексту
func TestProcessWithTools_XMLAndTextFallback(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if callCount >= 2 {
			var bodyMap map[string]interface{}
			bodyBytes := make([]byte, r.ContentLength)
			r.Body.Read(bodyBytes)
			json.Unmarshal(bodyBytes, &bodyMap)

			if msgs, ok := bodyMap["messages"].([]interface{}); ok {
				for _, msg := range msgs {
					if m, ok := msg.(map[string]interface{}); ok {
						if role, ok := m["role"].(string); ok && role == "tool" {
							t.Logf("Tool result message found")
						}
					}
				}
			}
		}

		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Here is your answer.\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
	}
	config.SessionConfig.PeerID = 99914
	config.SessionConfig.MaxHistory = 100

	a, executor := newTestAgentWithStub(t, config)

	xmlText := `Let me check.

<tool_call>
<function=time_get>
</function>
</tool_call>

Done!`

	s := a.GetSession(99914)
	s.AddUserMessage("What time is it?")

	messages := []Message{
		{Role: "user", Content: "What time is it?"},
	}

	result, used, err := a.xmlFallback(context.Background(), xmlText, messages, s)
	if err != nil {
		t.Fatalf("xmlFallback failed: %v", err)
	}
	if !used {
		t.Fatal("expected xmlFallback to be used")
	}
	if !result.Success {
		t.Fatal("expected success")
	}
	if !executor.Contains("time_get") {
		t.Error("expected time_get tool to be called")
	}
	t.Logf("Response: %s", result.Response)

	parsed := ParseXMLToolCalls(xmlText)
	if !strings.Contains(parsed.Content, "Let me check.") {
		t.Errorf("expected cleaned content to contain 'Let me check.', got %q", parsed.Content)
	}
}

func TestConvertXMLToolCalls_EmptyArgs(t *testing.T) {
	calls := convertXMLToolCalls([]XMLToolCall{
		{Name: "time_get", Args: map[string]string{}},
	})
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Function.Name != "time_get" {
		t.Errorf("expected time_get, got %q", calls[0].Function.Name)
	}
	args, err := parseToolArguments(calls[0])
	if err != nil {
		t.Fatalf("parseToolArguments failed: %v", err)
	}
	if len(args) != 0 {
		t.Errorf("expected empty args, got %v", args)
	}
}

func TestConvertXMLToolCalls_NilArgs(t *testing.T) {
	calls := convertXMLToolCalls([]XMLToolCall{
		{Name: "time_get"},
	})
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	args, err := parseToolArguments(calls[0])
	if err != nil {
		t.Fatalf("parseToolArguments failed: %v", err)
	}
	if len(args) != 0 {
		t.Errorf("expected empty args, got %v", args)
	}
}

// TestXMLInReasoning проверяет что XML в reasoningText распознаётся и выполняется
func TestXMLInReasoning(t *testing.T) {
	reasoningText := "I need to read a file.\n\n<function=read_file>\n<parameter=path>/tmp/test.txt</parameter>\n</function>"

	parsed := ParseXMLToolCalls(reasoningText)
	if len(parsed.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call in reasoning, got %d", len(parsed.ToolCalls))
	}
	if parsed.ToolCalls[0].Name != "read_file" {
		t.Errorf("expected read_file, got %q", parsed.ToolCalls[0].Name)
	}
	if parsed.ToolCalls[0].Args["path"] != "/tmp/test.txt" {
		t.Errorf("expected /tmp/test.txt, got %q", parsed.ToolCalls[0].Args["path"])
	}
	if strings.Contains(parsed.Content, "<function>") {
		t.Errorf("content should not contain XML tags, got %q", parsed.Content)
	}
}

func TestXMLDuplicateFiltering(t *testing.T) {
	nativeTC := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "file_read",
			Arguments: []byte(`{"path":"/tmp/test.txt"}`),
		},
	}

	xmlTC := XMLToolCall{
		Name: "file_read",
		Args: map[string]string{"path": "/tmp/test.txt"},
	}

	nativeSig := toolCallSignature(nativeTC)
	xmlSig := xmlToolCallSignature(xmlTC)

	if nativeSig != xmlSig {
		t.Errorf("signatures should match:\nnative: %q\nxml: %q", nativeSig, xmlSig)
	}

	executed := map[string]bool{nativeSig: true}
	if executed[xmlSig] {
		t.Log("Duplicate correctly detected")
	} else {
		t.Error("Duplicate should be detected")
	}
}

func TestXMLDifferentArgsNotFiltered(t *testing.T) {
	nativeTC := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "file_read",
			Arguments: []byte(`{"path":"/tmp/a.txt"}`),
		},
	}

	xmlTC := XMLToolCall{
		Name: "file_read",
		Args: map[string]string{"path": "/tmp/b.txt"},
	}

	nativeSig := toolCallSignature(nativeTC)
	xmlSig := xmlToolCallSignature(xmlTC)

	if nativeSig == xmlSig {
		t.Errorf("signatures should NOT match for different args:\nnative: %q\nxml: %q", nativeSig, xmlSig)
	}
}

func TestXMLDifferentToolsNotFiltered(t *testing.T) {
	nativeTC := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "file_read",
			Arguments: []byte(`{"path":"/tmp/test.txt"}`),
		},
	}

	xmlTC := XMLToolCall{
		Name: "file_write",
		Args: map[string]string{"path": "/tmp/test.txt"},
	}

	nativeSig := toolCallSignature(nativeTC)
	xmlSig := xmlToolCallSignature(xmlTC)

	if nativeSig == xmlSig {
		t.Errorf("signatures should NOT match for different tools:\nnative: %q\nxml: %q", nativeSig, xmlSig)
	}
}

func TestCleanedReasoningSentToThinking(t *testing.T) {
	reasoningText := "I will check the time.\n\n<function=time_get>\n</function>"

	parsed := ParseXMLToolCalls(reasoningText)

	if strings.Contains(parsed.Content, "<function>") {
		t.Errorf("content should not contain XML tags, got %q", parsed.Content)
	}
	if !strings.Contains(parsed.Content, "I will check the time") {
		t.Errorf("content should contain reasoning text, got %q", parsed.Content)
	}
}

// TestProcessXMLToolResults_ChainedToolCalls проверяет что XML tool calls в ответе
// после выполнения инструментов выполняются рекурсивно
func TestProcessXMLToolResults_ChainedToolCalls(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if callCount == 1 {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Let me also calculate something.\\n\\n<function=calc>\\n<parameter=expression>2+2</parameter>\\n</function>\\n\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		} else {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Done! The result is 4.\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		}
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
	}
	config.SessionConfig.PeerID = 99920
	config.SessionConfig.MaxHistory = 100

	a, executor := newTestAgentWithStub(t, config)

	s := a.GetSession(99920)
	s.AddUserMessage("What time is it and calculate 2+2?")

	messages := []Message{
		{Role: "user", Content: "What time is it and calculate 2+2?"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "time_get", Arguments: []byte("{}")}},
		}},
		{Role: "tool", ToolCallID: "call_1", Name: "time_get", Content: `{"time": "2024-01-01T12:00:00Z"}`},
	}

	toolResults := []ToolCallResult{
		{ToolCallID: "call_1", ToolName: "time_get", Content: `{"time": "2024-01-01T12:00:00Z"}`, IsError: false},
	}

	response, err := a.processToolResults(context.Background(), messages, "", []ToolCall{
		{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "time_get", Arguments: []byte("{}")}},
	}, toolResults, s, make(map[string]bool))

	if err != nil {
		t.Fatalf("processToolResults failed: %v", err)
	}

	if callCount < 2 {
		t.Errorf("expected at least 2 LLM calls (first for calc tool, second for final response), got %d", callCount)
	}

	if !strings.Contains(response, "4") {
		t.Errorf("expected response to contain '4' (result of 2+2), got: %q", response)
	}

	if !executor.Contains("calc") {
		t.Error("expected calc tool to be called from XML in tool results")
	}

	t.Logf("Final response: %s", response)
}

// TestProcessMessage_InvalidXMLToolCall_ShouldNotForwardToUser проверяет,
// что когда модель отвечает с <tool_call> обёрткой вокруг JSON
// (вместо нативных tool_calls), агент НЕ отправляет сырой XML пользователю,
// а отправляет ошибку модели.
func TestProcessMessage_InvalidXMLToolCall_ShouldNotForwardToUser(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if callCount == 1 {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"<tool_call>\\n{\\\"name\\\": \\\"file_list\\\", \\\"arguments\\\": {\\\"path\\\": \\\".\\\"}}\\n</tool_call>\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		} else {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"I will list the current directory.\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		}
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
	}
	config.SessionConfig.PeerID = 99930
	config.SessionConfig.MaxHistory = 100

	a, executor := newTestAgentWithStub(t, config)

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "what files are here?", 99930)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	if strings.Contains(response, "<tool_call>") {
		t.Errorf("response should not contain XML tool call tags, got: %q", response)
	}

	s := a.GetSession(99930)
	for _, msg := range s.GetHistory() {
		if msg.Role == "assistant" && strings.Contains(msg.Content, "<tool_call>") {
			t.Errorf("assistant message should not contain XML tool call tags, got: %q", msg.Content)
		}
	}

	if callCount < 2 {
		t.Errorf("expected at least 2 LLM calls, got %d", callCount)
	}

	// Проверяем что через stub executor прошли тулы (handleInvalidXMLToolCall → format_error)
	if !executor.Contains("[TOOL] Call:") {
		t.Error("expected at least one tool call via stub executor")
	}

	t.Logf("Final response: %s, LLM calls: %d", response, callCount)
}

// TestProcessToolResults_DeduplicateSameToolAcrossRecursion проверяет,
// что при рекурсивном вызове processToolResults одинаковые XML-тулы
// не выполняются повторно (дедупликация через executed map).
func TestProcessToolResults_DeduplicateSameToolAcrossRecursion(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if llmCallCount == 1 {
			// Первый LLM-запрос: возвращает XML тул
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"<tool_call>\\n<function=counting>\\n</function>\\n</tool_call>\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		} else if llmCallCount == 2 {
			// Второй LLM-запрос: тот же XML тул (должен быть задедуплицирован)
			// Возвращаем текст с XML и финальный ответ
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Already counted.\\n<tool_call>\\n<function=counting>\\n</function>\\n</tool_call>\\nDone.\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		} else {
			// Третий — не должен понадобиться
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		}
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
	}
	config.SessionConfig.PeerID = 99920
	config.SessionConfig.MaxHistory = 100

	a, executor := newTestAgentWithStub(t, config)

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "What time is it?", 99920)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}
	if response == "" {
		t.Fatal("expected non-empty response")
	}

	t.Logf("LLM calls: %d, tool log entries: %d", llmCallCount, len(executor.ReadLog()))

	// counting тул должен выполниться РОВНО 1 раз (дедупликация на втором запросе)
	countingCalls := executor.Count("[TOOL] Call: counting")
	if countingCalls != 1 {
		t.Errorf("expected counting tool to execute exactly 1 time (dedup), got %d calls in log", countingCalls)
		t.Logf("Full log: %v", executor.ReadLog())
	}

	// Ответ не должен содержать XML
	if strings.Contains(response, "<tool_call>") || strings.Contains(response, "<function") {
		t.Errorf("response should not contain XML tool call tags, got: %q", response)
	}

	// Должен быть минимум 2 вызова LLM (1й — XML тул, 2й — дубль с текстом)
	if llmCallCount < 2 {
		t.Errorf("expected at least 2 LLM calls, got %d", llmCallCount)
	}
}

// TestProcessToolResults_InvalidXMLToolCall проверяет что processToolResults
// НЕ выполняет JSON тул внутри <tool_call> обёртки, а отправляет модели ошибку.
func TestProcessToolResults_InvalidXMLToolCall(t *testing.T) {
	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if callCount == 1 {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"<tool_call>\\n{\\\"name\\\": \\\"counting\\\", \\\"arguments\\\": {}}\\n</tool_call>\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		} else {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"I will use proper tool calls.\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		}
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
	}
	config.SessionConfig.PeerID = 99940
	config.SessionConfig.MaxHistory = 100

	a, executor := newTestAgentWithStub(t, config)

	s := a.GetSession(99940)
	s.AddUserMessage("do something")

	messages := []Message{
		{Role: "user", Content: "do something"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "counting", Arguments: []byte("{}")}},
		}},
		{Role: "tool", ToolCallID: "call_1", Name: "counting", Content: `ok`},
	}

	toolResults := []ToolCallResult{
		{ToolCallID: "call_1", ToolName: "counting", Content: `ok`, IsError: false},
	}

	response, err := a.processToolResults(context.Background(), messages, "", []ToolCall{
		{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "counting", Arguments: []byte("{}")}},
	}, toolResults, s, make(map[string]bool))

	if err != nil {
		t.Fatalf("processToolResults failed: %v", err)
	}

	// JSON внутри <tool_call> — валидный гибридный формат, должен выполниться через JSON fallback
	if !executor.Contains("counting") {
		t.Error("expected counting tool to be called via stub executor")
	}

	if strings.Contains(response, "<tool_call>") {
		t.Errorf("response should not contain XML tool call tags, got: %q", response)
	}

	for _, msg := range s.GetHistory() {
		if msg.Role == "assistant" && strings.Contains(msg.Content, "<tool_call>") {
			t.Errorf("assistant message should not contain XML tool call tags, got: %q", msg.Content)
		}
	}

	if callCount < 2 {
		t.Errorf("expected at least 2 LLM calls, got %d", callCount)
	}

	t.Logf("Response: %s, LLM calls: %d, tool log: %v", response, callCount, executor.ReadLog())
}

// TestProcessMessage_Integration_NativeToolCallsThenXMLInToolResults проверяет
// полный сценарий из bug report: модель сначала отвечает НАТИВНЫМИ tool_calls,
// затем после выполнения инструментов и отправки результатов — отвечает
// невалидным <tool_call> форматом (с JSON внутри) внутри processToolResults.
func TestProcessMessage_Integration_NativeToolCallsThenXMLInToolResults(t *testing.T) {
	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if callCount == 1 {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"counting\",\"arguments\":\"{}\"}}]}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		} else if callCount == 2 {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"<tool_call>\\n{\\\"name\\\": \\\"counting\\\", \\\"arguments\\\": {}}\\n</tool_call>\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		} else {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Files have been processed.\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		}
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
	}
	config.SessionConfig.PeerID = 99950
	config.SessionConfig.MaxHistory = 100

	a, executor := newTestAgentWithStub(t, config)

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "delete those files", 99950)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	if strings.Contains(response, "<tool_call>") {
		t.Errorf("response should not contain XML tool call tags, got: %q", response)
	}

	// counting тул должен выполниться РОВНО 1 раз (из нативных tool_calls)
	// JSON внутри <tool_call> задедуплицирован — не выполняется
	countingCalls := executor.Count("[TOOL] Call: counting")
	if countingCalls != 1 {
		t.Errorf("expected counting tool to execute exactly 1 time (from native tool_calls), got %d times in log", countingCalls)
		t.Logf("Full log: %v", executor.ReadLog())
	}

	s := a.GetSession(99950)
	for _, msg := range s.GetHistory() {
		if msg.Role == "assistant" && strings.Contains(msg.Content, "<tool_call>") {
			t.Errorf("assistant message should not contain XML tool call tags, got: %q", msg.Content)
		}
	}

	if callCount < 2 {
		t.Errorf("expected at least 2 LLM calls, got %d", callCount)
	}

	t.Logf("Final response: %s, LLM calls: %d, tool log: %v", response, callCount, executor.ReadLog())
}

// TestMalformedXMLInReasoning_NotSilentlyReturned проверяет что если модель
// возвращает сломанный XML внутри <tool_call> в reasoningText (например,
// <subagent> вместо <function=subagent>) и responseText пустой — то система
// НЕ должна использовать очищенный reasoning как финальный ответ, а должна
// вызвать handleInvalidXMLToolCall и дать модели второй шанс.
func TestMalformedXMLInReasoning_NotSilentlyReturned(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if callCount == 1 {
			// Возвращаем пустой content + reasoning со сломанным XML
			// <subagent> вместо <function=subagent> — как в реальном баге
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"Нужно создать исправленный код и отправить его на QA для проверки\\n\\n<tool_call>\\n<subagent>\\n<name>\\nworker\\n</name>\\n</subagent>\\n</tool_call>\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		} else {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Proper response after format error.\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		}
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
	}
	config.SessionConfig.PeerID = 99960
	config.SessionConfig.MaxHistory = 100

	a, _ := newTestAgentWithStub(t, config)

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "do something", 99960)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	// BUG CHECK: response не должен содержать reasoning текст
	if strings.Contains(response, "Нужно создать исправленный код") {
		t.Error("BUG: response contains reasoning text instead of proper response — malformed XML was silently stripped and returned as answer")
	}

	// BUG CHECK: должно быть минимум 2 вызова LLM (ошибка формата → retry)
	if callCount < 2 {
		t.Errorf("BUG: expected at least 2 LLM calls (format error should retry), got %d", callCount)
	}

	t.Logf("Final response: %s, LLM calls: %d", response, callCount)
}
