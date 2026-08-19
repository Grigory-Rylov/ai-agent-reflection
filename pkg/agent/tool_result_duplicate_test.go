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


func TestProcessToolResults_AllDuplicates_ReturnsContent(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if llmCallCount == 1 {
			
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Let me list files.\\n\\n<function=file_list>\\n<parameter=path>\\n/home/orangepi/data/projects/android\\n</parameter>\\n</function>\\n\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		} else if llmCallCount == 2 {
			
			
			
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Отлично, проект собран! Теперь создам Espresso тест.\\n\\n<function=file_list>\\n<parameter=path>\\n/home/orangepi/data/projects/android\\n</parameter>\\n</function>\\n\"}}]}\n\n"))
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
	config.SessionConfig.PeerID = 99921

	a, executor := newTestAgentWithStub(t, config)

	
	a.toolsRegistry.Register(&tools.DirListTool{})

	ctx := context.Background()
	response, err := a.ProcessMessage(ctx, "Test", 99921)
	if err != nil {
		t.Fatalf("ProcessMessage failed: %v", err)
	}

	t.Logf("LLM calls: %d", llmCallCount)
	t.Logf("Response: %s", response)

	
	if llmCallCount != 2 {
		t.Errorf("Expected 2 LLM calls, got %d", llmCallCount)
	}

	
	
	if response == "" {
		t.Error("Expected non-empty response when all tool calls are duplicates")
	}

	
	expectedContent := "Отлично, проект собран"
	if !strings.Contains(response, expectedContent) {
		t.Errorf("Response should contain %q, got: %s", expectedContent, response)
	}

	
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


func TestProcessToolResults_JSON_AllDuplicates_ReturnsContent(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if llmCallCount == 1 {
			
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Let me check files. {\\\"name\\\": \\\"file_list\\\", \\\"arguments\\\": {\\\"path\\\": \\\"/tmp\\\"}}\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
			w.Write([]byte("[DONE]\n"))
		} else if llmCallCount == 2 {
			
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

	
	if executor.Count("[TOOL] Call: file_list") != 1 {
		t.Errorf("Expected file_list to be called once (deduplicated), got %d calls", executor.Count("[TOOL] Call: file_list"))
	}
}


func TestReasoningLeakInToolResults(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		if llmCallCount == 1 {
			
			w.Write([]byte(`data: {"choices":[{"delta":{"content":"Let me check the time.\n\n<function=time_get>\n</function>\n"}}]}` + "\n\n"))
			w.Write([]byte(`data: {"choices":[{"delta":{"reasoning_content":"I need to check the current time for the user."}}]}` + "\n\n"))
			w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
			w.Write([]byte("[DONE]\n"))
		} else if llmCallCount == 2 {
			
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

	
	if !executor.Contains("time_get") {
		t.Error("time_get tool was NOT called")
	}

	
	if len(thinkingReceived) == 0 {
		t.Error("No thinking messages were received")
	}

	
	for _, thinking := range thinkingReceived {
		if response != "" && strings.Contains(response, thinking) {
			t.Errorf("BUG: response contains thinking text — reasoning leaked into regular chat. Response: %q, Thinking: %q", response, thinking)
		}
	}

	
	if strings.Contains(response, "retrieved successfully") {
		t.Error("BUG: response contains reasoning text from tool results response")
	}
}


func TestReasoningOnlyResponse(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		
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

	
	if len(thinkingReceived) == 0 {
		t.Error("No thinking messages were received")
	}

	
	if response != "" {
		t.Errorf("BUG: response should be empty when only reasoning was returned, got: %q", response)
	}

	
	if strings.Contains(response, "reasoning that should stay") {
		t.Error("BUG: response contains reasoning text")
	}
}


func TestReasoningLeakInProcessStreaming(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		
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
		EnableTools:    false,  
	}
	config.SessionConfig.PeerID = 99982

	a := NewAgent(config)

	
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

	
	if len(thinkingReceived) == 0 {
		t.Error("No thinking messages were received")
	}

	
	if response != "Here is my answer." {
		t.Errorf("Expected response to be 'Here is my answer.', got: %q", response)
	}

	
	if strings.Contains(response, "thinking that should stay") {
		t.Error("BUG: response contains reasoning text from thinking channel")
	}
}


func TestReasoningNotAddedToSession(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		
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

	
	s := a.GetSession(99983)
	history := s.GetHistory()

	t.Logf("Response: %q", response)
	t.Logf("Thinking received: %v", thinkingReceived)
	t.Logf("Session history length: %d", len(history))

	
	for i, msg := range history {
		t.Logf("  [%d] Role=%s, Content=%q", i, msg.Role, msg.Content)
	}

	
	if len(thinkingReceived) == 0 {
		t.Error("No thinking messages were received via callback")
	}

	
	for _, msg := range history {
		if msg.Role == "assistant" && strings.Contains(msg.Content, "internal reasoning") {
			t.Error("BUG: reasoning text was added to session as assistant message")
		}
	}

	
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
	
	if hasAssistant {
		t.Error("Session should NOT have assistant message when only reasoning was returned")
	}
}


func TestThinkingTagsNotLeaked(t *testing.T) {
	llmCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCallCount++
		w.Header().Set("Content-Type", "text/event-stream")

		
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

	
	if strings.Contains(response, "<thinking>") || strings.Contains(response, "</thinking>") {
		t.Error("BUG: response contains <thinking> tags — they should be stripped")
	}

	
	if strings.Contains(response, "I need to analyze this request") || strings.Contains(response, "The user wants to know") {
		t.Error("BUG: response contains thinking content inside tags — it should only go to thinking_peer_id")
	}

	
	if len(thinkingReceived) == 0 {
		t.Error("No thinking messages were received via callback")
	}

	
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

	
	expectedAnswer := "The system is running fine"
	if !strings.Contains(response, expectedAnswer) {
		t.Errorf("Expected response to contain %q, got: %q", expectedAnswer, response)
	}
}
