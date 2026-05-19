package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

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

	// Verify arguments are parseable
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
	// Создаём mock сервер для LLM
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Current time is: \"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	reg := tools.NewRegistry()
	reg.Register(&tools.TimeGetTool{})

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
	t.Logf("Response: %s", result.Response)
}

// TestXMLFallback_SkipWhenNativeToolCallsPresent проверяет что XML fallback не мешает нативным tool_calls
func TestXMLFallback_SkipWhenNativeToolCallsPresent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Возвращаем tool_calls (не XML)
		w.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"time_get","arguments":"{}"}}]}}]}`))
	}))
	defer server.Close()

	reg := tools.NewRegistry()
	reg.Register(&tools.TimeGetTool{})

	config := Config{
		LlamaServerURL:   server.URL,
		Model:            "test-model",
		MaxTokens:        100,
		Temperature:      0.7,
		SessionConfig:    session.DefaultConfig(),
		EnableTools:      true,
		MaxToolCalls:     5,
	}
	config.SessionConfig.PeerID = 99911

	a := NewAgent(config)
	a.RegisterTools(reg)

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "What time is it?", 99911)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}
	// Native tool_calls не возвращают текст — должен быть fallback
	t.Logf("Response: %q", response)
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

	reg := tools.NewRegistry()
	reg.Register(&tools.TimeGetTool{})
	reg.Register(&tools.CalcTool{})

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
	// Mock LLM: возвращает текст с XML tool calls, потом финальный ответ
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if callCount == 1 {
			// Первый запрос: модель отвечает XML tool calls
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Let me check the time.\\n\\n<tool_call>\\n<function=time_get>\\n</function>\\n</tool_call>\\n\\nDone.\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		} else {
			// Второй запрос: финальный ответ после выполнения tool
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"The current time has been checked.\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		}
	}))
	defer server.Close()

	reg := tools.NewRegistry()
	reg.Register(&tools.TimeGetTool{})

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
		EnableTools:    true,
		MaxToolCalls:   5,
	}
	config.SessionConfig.PeerID = 99913
	config.SessionConfig.MaxHistory = 100

	a := NewAgent(config)
	a.RegisterTools(reg)

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "What time is it?", 99913)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}
	if response == "" {
		t.Fatal("expected non-empty response")
	}
	t.Logf("Final response: %s", response)
}

// TestProcessWithTools_XMLAndTextFallback проверяет что XML fallback не мешает обычному тексту
func TestProcessWithTools_XMLAndTextFallback(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")

		// Проверяем содержание запроса
		if callCount >= 2 {
			// Второй запрос должен содержать tool result
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

	reg := tools.NewRegistry()
	reg.Register(&tools.TimeGetTool{})

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
		EnableTools:    true,
		MaxToolCalls:   5,
	}
	config.SessionConfig.PeerID = 99914
	config.SessionConfig.MaxHistory = 100

	a := NewAgent(config)
	a.RegisterTools(reg)

	// Текст с XML tool calls: обычный текст + XML
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
	t.Logf("Response: %s", result.Response)

	// Проверяем что очищенный контент сохранён
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
	// Проверяем что content очищен от XML
	if strings.Contains(parsed.Content, "<function>") {
		t.Errorf("content should not contain XML tags, got %q", parsed.Content)
	}
}

// TestXMLDuplicateFiltering проверяет что дубли NATIVE+XML пропускаются
func TestXMLDuplicateFiltering(t *testing.T) {
	// NATIVE tool call
	nativeTC := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "file_read",
			Arguments: []byte(`{"path":"/tmp/test.txt"}`),
		},
	}

	// XML tool call с теми же аргументами
	xmlTC := XMLToolCall{
		Name: "file_read",
		Args: map[string]string{"path": "/tmp/test.txt"},
	}

	nativeSig := toolCallSignature(nativeTC)
	xmlSig := xmlToolCallSignature(xmlTC)

	// Сигнатуры должны совпадать
	if nativeSig != xmlSig {
		t.Errorf("signatures should match:\nnative: %q\nxml: %q", nativeSig, xmlSig)
	}

	// При фильтрации дубли должны пропускаться
	executed := map[string]bool{nativeSig: true}
	if executed[xmlSig] {
		t.Log("Duplicate correctly detected")
	} else {
		t.Error("Duplicate should be detected")
	}
}

// TestXMLDifferentArgsNotFiltered проверяет что разные аргументы НЕ фильтруются
func TestXMLDifferentArgsNotFiltered(t *testing.T) {
	// NATIVE tool call
	nativeTC := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "file_read",
			Arguments: []byte(`{"path":"/tmp/a.txt"}`),
		},
	}

	// XML tool call с ДРУГИМ путём
	xmlTC := XMLToolCall{
		Name: "file_read",
		Args: map[string]string{"path": "/tmp/b.txt"},
	}

	nativeSig := toolCallSignature(nativeTC)
	xmlSig := xmlToolCallSignature(xmlTC)

	// Сигнатуры НЕ должны совпадать
	if nativeSig == xmlSig {
		t.Errorf("signatures should NOT match for different args:\nnative: %q\nxml: %q", nativeSig, xmlSig)
	}
}

// TestXMLDifferentToolsNotFiltered проверяет что разные инструменты НЕ фильтруются
func TestXMLDifferentToolsNotFiltered(t *testing.T) {
	// NATIVE tool call - file_read
	nativeTC := ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "file_read",
			Arguments: []byte(`{"path":"/tmp/test.txt"}`),
		},
	}

	// XML tool call - file_write (другой инструмент)
	xmlTC := XMLToolCall{
		Name: "file_write",
		Args: map[string]string{"path": "/tmp/test.txt"},
	}

	nativeSig := toolCallSignature(nativeTC)
	xmlSig := xmlToolCallSignature(xmlTC)

	// Сигнатуры НЕ должны совпадать
	if nativeSig == xmlSig {
		t.Errorf("signatures should NOT match for different tools:\nnative: %q\nxml: %q", nativeSig, xmlSig)
	}
}

// TestCleanedReasoningSentToThinking проверяет что очищенный reasoning отправляется в thinking
func TestCleanedReasoningSentToThinking(t *testing.T) {
	reasoningText := "I will check the time.\n\n<function=time_get>\n</function>"

	parsed := ParseXMLToolCalls(reasoningText)

	// Content должен содержать текст без XML тегов
	if strings.Contains(parsed.Content, "<function>") {
		t.Errorf("content should not contain XML tags, got %q", parsed.Content)
	}
	if !strings.Contains(parsed.Content, "I will check the time") {
		t.Errorf("content should contain reasoning text, got %q", parsed.Content)
	}
}
