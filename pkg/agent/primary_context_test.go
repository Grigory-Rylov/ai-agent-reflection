package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)


type mockTaskTool struct {
	workerResult string
	callCount    *atomic.Int32
}

func (t *mockTaskTool) Name() string        { return "task" }
func (t *mockTaskTool) Description() string  { return "Launch a sub-agent to handle a task" }

func (t *mockTaskTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"subagent_type": tools.CreateStringParameter("subagent_type", "The type of agent to use", true),
			"prompt":        tools.CreateStringParameter("prompt", "The task", true),
		},
		"required": []string{"subagent_type", "prompt"},
	}
}

func (t *mockTaskTool) Execute(ctx context.Context, inputs map[string]string) (tools.ToolResult, error) {
	t.callCount.Add(1)
	return tools.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"response": t.workerResult,
		},
	}, nil
}


type scriptedResponse struct {
	content      string
	toolCalls    string 
	finishReason string
}

func newScriptedLLMServer(responses []scriptedResponse) (*httptest.Server, *atomic.Int32) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(callCount.Add(1)) - 1
		w.Header().Set("Content-Type", "text/event-stream")

		if n >= len(responses) {
			
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"fallback response"},"finish_reason":"stop"}]}`+"\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}

		resp := responses[n]
		fr := resp.finishReason
		if fr == "" {
			fr = "stop"
		}

		if resp.toolCalls != "" {
			fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":%s},"finish_reason":"%s"}]}`+"\n\n", resp.toolCalls, fr)
		} else {
			fmt.Fprintf(w, `data: {"choices":[{"delta":{"content":"%s"},"finish_reason":"%s"}]}`+"\n\n", resp.content, fr)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	return server, &callCount
}


func TestPrimaryAgentContextSharing(t *testing.T) {
	
	
	
	responses := []scriptedResponse{
		{
			
			toolCalls:    `[{"index":0,"id":"call_1","type":"function","function":{"name":"task","arguments":"{\"subagent_type\":\"worker\",\"prompt\":\"создай структуру проекта\"}"}}]`,
			finishReason: "tool_calls",
		},
		{
			
			content:      "Отлично! Worker создал структуру проекта. Готов ответить на дополнительные вопросы.",
			finishReason: "stop",
		},
		{
			
			content:      "Исходя из нашей предыдущей работы над структурой проекта, могу рассказать подробнее: проект состоит из трёх основных пакетов.",
			finishReason: "stop",
		},
	}
	server, llmCallCount := newScriptedLLMServer(responses)
	defer server.Close()

	var taskCallCount atomic.Int32
	mockTask := &mockTaskTool{
		workerResult: "Worker completed: создана структура проекта с пакетами handler, agent, tools.",
		callCount:    &taskCallCount,
	}

	reg := tools.NewRegistry()
	reg.Register(mockTask)

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 4096
	config.EnableTools = true
	
	config.SessionConfig = sessionConfigWithPrompt("You are a Lead Agent. You can delegate tasks to worker via the task tool.", nil)

	a := NewAgent(config)
	if impl, ok := interface{}(a).(interface{ RegisterTools(*tools.Registry) }); ok {
		impl.RegisterTools(reg)
	}

	peerID := int64(77777)

	
	
	
	
	
	
	sess := a.GetSession(peerID)
	originalPrompt := sess.GetSystemPrompt()
	sess.UpdateSystemPrompt(originalPrompt + "\n\nYou are a Lead Agent. Coordinate the work and delegate to worker when needed.")
	defer sess.UpdateSystemPrompt(originalPrompt)

	leadTask := "выполни задачу: создай структуру проекта"
	ctx := context.Background()
	leadResponse, err := a.ProcessMessage(ctx, leadTask, peerID)
	if err != nil {
		t.Fatalf("Step 1 (#lead): unexpected error: %v", err)
	}
	if leadResponse == "" {
		t.Fatal("Step 1 (#lead): expected non-empty response")
	}
	t.Logf("Step 1 — Lead response: %s", leadResponse)

	
	if taskCallCount.Load() != 1 {
		t.Errorf("expected task tool to be called once, got %d", taskCallCount.Load())
	}

	
	if llmCallCount.Load() != 2 {
		t.Errorf("expected 2 LLM calls (tool_call + final), got %d", llmCallCount.Load())
	}

	
	
	
	history := sess.GetHistory()
	foundLeadTask := false
	foundToolResult := false
	for _, msg := range history {
		if msg.Role == session.UserRole && strings.Contains(msg.Content, "выполни задачу") {
			foundLeadTask = true
		}
		if msg.Role == session.ToolRole && strings.Contains(msg.Content, "Worker completed") {
			foundToolResult = true
		}
	}
	if !foundLeadTask {
		t.Error("Step 2: session should contain #lead task in history")
	}
	if !foundToolResult {
		t.Error("Step 2: session should contain worker tool result in history")
	}

	
	
	
	
	sess.UpdateSystemPrompt(originalPrompt)

	plainMessage := "расскажи подробнее про структуру"
	plainResponse, err := a.ProcessMessage(ctx, plainMessage, peerID)
	if err != nil {
		t.Fatalf("Step 3 (plain message): unexpected error: %v", err)
	}
	if plainResponse == "" {
		t.Fatal("Step 3 (plain message): expected non-empty response")
	}
	t.Logf("Step 3 — Plain message response: %s", plainResponse)

	
	if llmCallCount.Load() != 3 {
		t.Errorf("expected 3 LLM calls total, got %d", llmCallCount.Load())
	}

	
	
	
	
	history = sess.GetHistory()
	foundPlainMessage := false
	foundLeadContext := false
	for _, msg := range history {
		if msg.Role == session.UserRole && strings.Contains(msg.Content, "расскажи подробнее") {
			foundPlainMessage = true
		}
		
		if msg.Role == session.AssistantRole && strings.Contains(msg.Content, "предыдущей") {
			foundLeadContext = true
		}
	}
	if !foundPlainMessage {
		t.Error("Step 4: session should contain plain follow-up message")
	}
	if !foundLeadContext {
		t.Log("Step 4: plain response did not explicitly reference previous context (may be model-dependent)")
	}

	
	userMsgCount := 0
	for _, msg := range history {
		if msg.Role == session.UserRole {
			userMsgCount++
		}
	}
	if userMsgCount < 2 {
		t.Errorf("expected at least 2 user messages in session, got %d", userMsgCount)
	}

	t.Logf("Session history length: %d messages", len(history))
	t.Logf("LLM calls: %d, Task tool calls: %d", llmCallCount.Load(), taskCallCount.Load())
}


func sessionConfigWithPrompt(systemPrompt string, _ *string) session.Config {
	cfg := session.DefaultConfig()
	cfg.SystemPrompt = systemPrompt
	return cfg
}


func TestSessionIsolation_NonPrimaryAgent(t *testing.T) {
	responses := []scriptedResponse{
		{
			
			content:      "I'm the main agent. How can I help?",
			finishReason: "stop",
		},
		{
			
			content:      "I don't know about any worker task — I have my own context.",
			finishReason: "stop",
		},
	}
	server, llmCallCount := newScriptedLLMServer(responses)
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 4096
	config.SessionConfig = sessionConfigWithPrompt("You are a helpful assistant.", nil)

	mainAgent := NewAgent(config)
	ctx := context.Background()
	mainPeerID := int64(11111)

	
	resp1, err := mainAgent.ProcessMessage(ctx, "Привет!", mainPeerID)
	if err != nil {
		t.Fatalf("main agent failed: %v", err)
	}
	t.Logf("Main agent response 1: %s", resp1)

	
	
	workerConfig := DefaultConfig()
	workerConfig.LlamaServerURL = server.URL
	workerConfig.Model = "test-model"
	workerConfig.MaxTokens = 4096
	workerConfig.EnableTools = true
	workerConfig.SessionConfig = sessionConfigWithPrompt("You are a Worker agent.", nil)

	workerAgent := NewAgent(workerConfig)
	workerPeerID := int64(22222)

	workerResp, err := workerAgent.ProcessMessage(ctx, "сделай задачу", workerPeerID)
	if err != nil {
		t.Fatalf("worker agent failed: %v", err)
	}
	t.Logf("Worker response: %s", workerResp)

	
	workerSess := workerAgent.GetSession(workerPeerID)
	workerHistory := workerSess.GetHistory()
	foundWorkerTask := false
	for _, msg := range workerHistory {
		if msg.Role == session.UserRole && strings.Contains(msg.Content, "сделай задачу") {
			foundWorkerTask = true
		}
	}
	if !foundWorkerTask {
		t.Error("worker session should contain worker task")
	}

	
	mainResp2, err := mainAgent.ProcessMessage(ctx, "что ты знаешь о задаче worker-а?", mainPeerID)
	if err != nil {
		t.Fatalf("main agent follow-up failed: %v", err)
	}
	t.Logf("Main agent response 2: %s", mainResp2)

	
	mainSess := mainAgent.GetSession(mainPeerID)
	mainHistory := mainSess.GetHistory()
	for _, msg := range mainHistory {
		if strings.Contains(msg.Content, "сделай задачу") {
			t.Error("BUG: main agent session should NOT contain worker task — sessions must be isolated for non-primary agents")
		}
	}

	
	t.Logf("LLM calls total: %d", llmCallCount.Load())
}
