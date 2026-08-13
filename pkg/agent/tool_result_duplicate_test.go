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

// TestProcessToolResults_AllDuplicates_ReturnsContent проверяет,
// что когда модель возвращает finish="stop" с XML tool calls,
// но все tool calls уже были выполнены (duplicates),
// агент возвращает контент ответа вместо пустого результата.
func TestProcessToolResults_AllDuplicates_ReturnsContent(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if llmCallCount == 1 {
			// Первый LLM-запрос: возвращает XML тул
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Let me list files.\\n\\n<function=file_list>\\n<parameter=path>\\n/home/orangepi/data/projects/android\\n</parameter>\\n</function>\\n\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		} else if llmCallCount == 2 {
			// Второй LLM-запрос после выполнения tool:
			// Модель возвращает тот же XML тул (duplicate) + текстовый контент
			// Ожидаем что агент вернет контент вместо пустого ответа
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Отлично, проект собран! Теперь создам Espresso тест.\\n\\n<function=file_list>\\n<parameter=path>\\n/home/orangepi/data/projects/android\\n</parameter>\\n</function>\\n\"}}]}\n\n"))
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
	config.SessionConfig.PeerID = 99921

	a, executor := newTestAgentWithStub(t, config)

	// Регистрируем tools
	a.toolsRegistry.Register(&tools.DirListTool{})

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "Test", 99921)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	t.Logf("LLM calls: %d", llmCallCount)
	t.Logf("Response: %s", response)

	// Ожидаем 2 вызова LLM (первый -> tools -> второй с duplicate)
	if llmCallCount != 2 {
		t.Errorf("Expected 2 LLM calls, got %d", llmCallCount)
	}

	// Ожидаем что ответ содержит контент "Отлично, проект собран!"
	// даже несмотря на duplicate tool call
	if response == "" {
		t.Error("Expected non-empty response when all tool calls are duplicates")
	}

	// Проверяем что в ответе есть текстовый контент
	expectedContent := "Отлично, проект собран"
	if !strings.Contains(response, expectedContent) {
		t.Errorf("Response should contain %q, got: %s", expectedContent, response)
	}

	// Проверяем что tool был выполнен только один раз (дедупликация работает)
	callCount := executor.Count("[TOOL] Call: file_list")
	t.Logf("file_list call count: %d", callCount)
	logLines := executor.ReadLog()
	t.Logf("Tool log lines: %d", len(logLines))
	for i, line := range logLines {
		t.Logf("  [%d] %s", i, line)
	}
	if callCount != 1 {
		t.Errorf("Expected file_list to be called once (deduplicated), got %d calls", callCount)
	}
}

// TestProcessToolResults_JSON_AllDuplicates_ReturnsContent проверяет
// то же самое но для JSON формата tool calls
func TestProcessToolResults_JSON_AllDuplicates_ReturnsContent(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if llmCallCount == 1 {
			// Первый LLM-запрос: возвращает JSON тул
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Let me check files. {\\\"name\\\": \\\"file_list\\\", \\\"arguments\\\": {\\\"path\\\": \\\"/tmp\\\"}}\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		} else if llmCallCount == 2 {
			// Второй LLM-запрос: тот же JSON тул (duplicate) + контент
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Project is ready! {\\\"name\\\": \\\"file_list\\\", \\\"arguments\\\": {\\\"path\\\": \\\"/tmp\\\"}} Done.\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		} else {
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
	config.SessionConfig.PeerID = 99922

	a, executor := newTestAgentWithStub(t, config)

	// Регистрируем tools
	a.toolsRegistry.Register(&tools.DirListTool{})

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "Test", 99922)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	t.Logf("LLM calls: %d", llmCallCount)
	t.Logf("Response: %s", response)

	if llmCallCount != 2 {
		t.Errorf("Expected 2 LLM calls, got %d", llmCallCount)
	}

	if response == "" {
		t.Error("Expected non-empty response when all JSON tool calls are duplicates")
	}

	// Проверяем что tool был выполнен только один раз
	if executor.Count("[TOOL] Call: file_list") != 1 {
		t.Errorf("Expected file_list to be called once (deduplicated), got %d calls", executor.Count("[TOOL] Call: file_list"))
	}
}

// TestReasoningLeakInToolResults проверяет что reasoning не попадает в основной ответ
// при выполнении tool calls. Сценарий:
// 1. Модель возвращает XML tool call и reasoning
// 2. Tool call выполняется
// 3. В ответ на tool results модель возвращает только reasoning (без content)
// 4. Reasoning должен остаться только в thinking_peer_id, не в основном ответе
func TestReasoningLeakInToolResults(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if llmCallCount == 1 {
			// Первый вызов: возвращаем XML tool call и reasoning
			w.Write([]byte(`data: {"choices":[{"delta":{"content":"Let me check the time.\n\n<function=time_get>\n</function>\n"}}]}` + "\n\n"))
			w.Write([]byte(`data: {"choices":[{"delta":{"reasoning_content":"I need to check the current time for the user."}}]}` + "\n\n"))
			w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
			w.Write([]byte("[DONE]\n"))
		} else if llmCallCount == 2 {
			// Второй вызов (на tool results): возвращаем только reasoning
			w.Write([]byte(`data: {"choices":[{"delta":{"reasoning_content":"The time has been retrieved successfully."}}]}` + "\n\n"))
			w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
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
		EnableTools:    true,
	}
	config.SessionConfig.PeerID = 99980

	a, executor := newTestAgentWithStub(t, config)

	// Set up thinking callback to capture thinking messages
	var thinkingReceived []string
	a.SetThinkingCallback(func(peerID int64, content string) error {
		thinkingReceived = append(thinkingReceived, content)
		return nil
	})

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "What time is it?", 99980)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	t.Logf("Final response: %q", response)
	t.Logf("Thinking received: %v", thinkingReceived)
	t.Logf("Tool calls: %v", executor.ReadLog())
	t.Logf("LLM calls: %d", llmCallCount)

	// Verify time_get was called
	if !executor.Contains("time_get") {
		t.Error("time_get tool was NOT called")
	}

	// Verify thinking was received
	if len(thinkingReceived) == 0 {
		t.Error("No thinking messages were received")
	}

	// BUG CHECK: response should NOT contain reasoning text
	for _, thinking := range thinkingReceived {
		if response != "" && strings.Contains(response, thinking) {
			t.Errorf("BUG: response contains thinking text — reasoning leaked into regular chat. Response: %q, Thinking: %q", response, thinking)
		}
	}

	// Verify reasoning from second call is not in response
	if strings.Contains(response, "retrieved successfully") {
		t.Error("BUG: response contains reasoning text from tool results response")
	}
}

// TestReasoningOnlyResponse проверяет что когда модель возвращает только reasoning
// (без content и без tool calls), reasoning не попадает в основной ответ
func TestReasoningOnlyResponse(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		// Возвращаем только reasoning, без content
		w.Write([]byte(`data: {"choices":[{"delta":{"reasoning_content":"This is reasoning that should stay in thinking channel only."}}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
		EnableTools:    true,
	}
	config.SessionConfig.PeerID = 99981

	a, _ := newTestAgentWithStub(t, config)

	// Set up thinking callback to capture thinking messages
	var thinkingReceived []string
	a.SetThinkingCallback(func(peerID int64, content string) error {
		thinkingReceived = append(thinkingReceived, content)
		return nil
	})

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "Hello", 99981)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	t.Logf("Final response: %q", response)
	t.Logf("Thinking received: %v", thinkingReceived)

	// Verify thinking was received
	if len(thinkingReceived) == 0 {
		t.Error("No thinking messages were received")
	}

	// BUG CHECK: response should be empty when only reasoning was returned
	if response != "" {
		t.Errorf("BUG: response should be empty when only reasoning was returned, got: %q", response)
	}

	// Verify reasoning text is not in response
	if strings.Contains(response, "reasoning that should stay") {
		t.Error("BUG: response contains reasoning text")
	}
}

// TestReasoningLeakInProcessStreaming проверяет что reasoning не попадает в основной ответ
// при processStreaming (без инструментов).
func TestReasoningLeakInProcessStreaming(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		// Возвращаем и reasoning, и content
		w.Write([]byte(`data: {"choices":[{"delta":{"reasoning_content":"This is thinking that should stay in thinking channel."}}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"Here is my answer."}}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
		EnableTools:    false,  // Без инструментов
	}
	config.SessionConfig.PeerID = 99982

	a := NewAgent(config)

	// Set up thinking callback to capture thinking messages
	var thinkingReceived []string
	a.SetThinkingCallback(func(peerID int64, content string) error {
		thinkingReceived = append(thinkingReceived, content)
		return nil
	})

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "Hello", 99982)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	t.Logf("Final response: %q", response)
	t.Logf("Thinking received: %v", thinkingReceived)

	// Verify thinking was received
	if len(thinkingReceived) == 0 {
		t.Error("No thinking messages were received")
	}

	// Verify response contains only the content, not reasoning
	if response != "Here is my answer." {
		t.Errorf("Expected response to be 'Here is my answer.', got: %q", response)
	}

	// Verify reasoning is not in response
	if strings.Contains(response, "thinking that should stay") {
		t.Error("BUG: response contains reasoning text from thinking channel")
	}
}

// TestReasoningNotAddedToSession проверяет что reasoning не добавляется в сессию
// как обычное сообщение assistant.
func TestReasoningNotAddedToSession(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		// Возвращаем только reasoning
		w.Write([]byte(`data: {"choices":[{"delta":{"reasoning_content":"This is internal reasoning."}}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
		EnableTools:    true,
	}
	config.SessionConfig.PeerID = 99983

	a, _ := newTestAgentWithStub(t, config)

	// Set up thinking callback
	var thinkingReceived []string
	a.SetThinkingCallback(func(peerID int64, content string) error {
		thinkingReceived = append(thinkingReceived, content)
		return nil
	})

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "Hello", 99983)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	// Check session history
	s := a.GetSession(99983)
	history := s.GetHistory()

	t.Logf("Response: %q", response)
	t.Logf("Thinking received: %v", thinkingReceived)
	t.Logf("Session history length: %d", len(history))

	// Print session history for debugging
	for i, msg := range history {
		t.Logf("  [%d] Role=%s, Content=%q", i, msg.Role, msg.Content)
	}

	// Verify thinking was sent to callback
	if len(thinkingReceived) == 0 {
		t.Error("No thinking messages were received via callback")
	}

	// BUG CHECK: reasoning should NOT be in session as assistant message
	for _, msg := range history {
		if msg.Role == "assistant" && strings.Contains(msg.Content, "internal reasoning") {
			t.Error("BUG: reasoning text was added to session as assistant message")
		}
	}

	// Session should have user message and optionally assistant message
	hasUser := false
	hasAssistant := false
	for _, msg := range history {
		if msg.Role == "user" {
			hasUser = true
		}
		if msg.Role == "assistant" && msg.Content != "" {
			hasAssistant = true
		}
	}
	if !hasUser {
		t.Error("Session should have user message")
	}
	// If reasoning was returned, it shouldn't create assistant message
	if hasAssistant {
		t.Error("Session should NOT have assistant message when only reasoning was returned")
	}
}

// TestThinkingTagsNotLeaked проверяет что <thinking>...</thinking> теги
// не попадают в основной ответ, а содержимое отправляется в thinking_peer_id
func TestThinkingTagsNotLeaked(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		// Модель возвращает thinking в тегах <thinking>...</thinking>
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"<thinking>I need to analyze this request carefully.\n\nThe user wants to know about the system status.</thinking>\n\nHere is my answer: The system is running fine."}}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := Config{
		LlamaServerURL: server.URL,
		Model:          "test-model",
		MaxTokens:      100,
		Temperature:    0.7,
		SessionConfig:  session.DefaultConfig(),
		EnableTools:    true,
	}
	config.SessionConfig.PeerID = 99990

	a, _ := newTestAgentWithStub(t, config)

	// Set up thinking callback
	var thinkingReceived []string
	a.SetThinkingCallback(func(peerID int64, content string) error {
		thinkingReceived = append(thinkingReceived, content)
		return nil
	})

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "What is the system status?", 99990)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	t.Logf("Final response: %q", response)
	t.Logf("Thinking received: %v", thinkingReceived)

	// BUG CHECK: response should NOT contain <thinking> tags
	if strings.Contains(response, "<thinking>") || strings.Contains(response, "</thinking>") {
		t.Error("BUG: response contains <thinking> tags — they should be stripped")
	}

	// BUG CHECK: response should NOT contain thinking content
	if strings.Contains(response, "I need to analyze this request") || strings.Contains(response, "The user wants to know") {
		t.Error("BUG: response contains thinking content inside tags — it should only go to thinking_peer_id")
	}

	// Verify thinking was sent to callback
	if len(thinkingReceived) == 0 {
		t.Error("No thinking messages were received via callback")
	}

	// Verify thinking callback received the content inside tags
	found := false
	for _, thinking := range thinkingReceived {
		if strings.Contains(thinking, "I need to analyze this request") {
			found = true
			break
		}
	}
	if !found {
		t.Error("Thinking callback did not receive the content inside <thinking> tags")
	}

	// Response should only contain the actual answer
	expectedAnswer := "The system is running fine"
	if !strings.Contains(response, expectedAnswer) {
		t.Errorf("Expected response to contain %q, got: %q", expectedAnswer, response)
	}
}
