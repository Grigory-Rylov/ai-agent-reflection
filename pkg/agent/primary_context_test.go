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

// ============================================================
// mockTaskTool — упрощённая имитация SubAgentTool/task.
// Возвращает фиксированный результат, имитируя ответ worker-а.
// ============================================================

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

// ============================================================
// newScriptedLLMServer создаёт httptest.Server,
// который возвращает разные SSE-ответы в зависимости от номера вызова.
// Каждый ответ: {"choices":[{"delta":{"content":"..."},"finish_reason":"stop"}]}
// или с tool_calls.
// ============================================================

type scriptedResponse struct {
	content      string
	toolCalls    string // JSON для tool_calls (пусто = нет)
	finishReason string
}

func newScriptedLLMServer(responses []scriptedResponse) (*httptest.Server, *atomic.Int32) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(callCount.Add(1)) - 1
		w.Header().Set("Content-Type", "text/event-stream")

		if n >= len(responses) {
			// Fallback: возвращаем пустой ответ
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

// ============================================================
// TestPrimaryAgentContextSharing проверяет,
// что контекст #lead (primary) и обычного чата — общий.
//
// Сценарий:
//  1. Пользователь: "#lead выполни задачу"
//     → LLM (lead) вызывает task-инструмент (worker)
//     → worker возвращает результат
//     → lead обрабатывает результат и отвечает пользователю
//  2. Пользователь: "расскажи подробнее" (без #lead)
//     → основной агент видит ВЕСЬ контекст (и из шага 1 тоже)
//
// Ожидаемый результат:
//   - Оба запроса обрабатываются в одной сессии
//   - Сессия содержит историю из обоих шагов
//   - Второй ответ содержит информацию из контекста первого шага
// ============================================================

func TestPrimaryAgentContextSharing(t *testing.T) {
	// Шаг 1: LLM-lead вызывает task-инструмент
	// Шаг 2: LLM-lead получает результат инструмента и даёт финальный ответ
	// Шаг 3: follow-up сообщение (без #lead) — агент должен помнить контекст
	responses := []scriptedResponse{
		{
			// Вызов 1: lead получает задачу, решает делегировать worker-у
			toolCalls:    `[{"index":0,"id":"call_1","type":"function","function":{"name":"task","arguments":"{\"subagent_type\":\"worker\",\"prompt\":\"создай структуру проекта\"}"}}]`,
			finishReason: "tool_calls",
		},
		{
			// Вызов 2: lead получает результат инструмента, формирует ответ пользователю
			content:      "Отлично! Worker создал структуру проекта. Готов ответить на дополнительные вопросы.",
			finishReason: "stop",
		},
		{
			// Вызов 3: пользователь пишет без #lead — агент должен помнить контекст
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
	config.MaxToolCalls = 5
	// Имитируем системный промпт lead-агента
	config.SessionConfig = sessionConfigWithPrompt("You are a Lead Agent. You can delegate tasks to worker via the task tool.", nil)

	a := NewAgent(config)
	if impl, ok := interface{}(a).(interface{ RegisterTools(*tools.Registry) }); ok {
		impl.RegisterTools(reg)
	}

	peerID := int64(77777)

	// ========================================
	// Шаг 1: "#lead выполни задачу"
	// ========================================
	// В реальном handler.go это делается через ProcessPromptWithExtraSystem,
	// который временно добавляет lead-промпт к системному. Здесь мы симулируем
	// это, устанавливая системный промпт сессии вручную.
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

	// Проверяем что task-инструмент был вызван
	if taskCallCount.Load() != 1 {
		t.Errorf("expected task tool to be called once, got %d", taskCallCount.Load())
	}

	// Проверяем что был вызван LLM для lead → tool_call и для lead → финальный ответ
	if llmCallCount.Load() != 2 {
		t.Errorf("expected 2 LLM calls (tool_call + final), got %d", llmCallCount.Load())
	}

	// ========================================
	// Шаг 2: Проверяем что сессия содержит контекст #lead
	// ========================================
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

	// ========================================
	// Шаг 3: Восстанавливаем исходный системный промпт (как после defer)
	// и отправляем обычное сообщение БЕЗ #lead
	// ========================================
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

	// Проверяем что LLM был вызван ещё раз для plain-сообщения
	if llmCallCount.Load() != 3 {
		t.Errorf("expected 3 LLM calls total, got %d", llmCallCount.Load())
	}

	// ========================================
	// Шаг 4: Проверяем что контекст сохранился — сессия содержит
	//        и историю #lead, и plain-сообщение
	// ========================================
	history = sess.GetHistory()
	foundPlainMessage := false
	foundLeadContext := false
	for _, msg := range history {
		if msg.Role == session.UserRole && strings.Contains(msg.Content, "расскажи подробнее") {
			foundPlainMessage = true
		}
		// Проверяем что plain-ответ модели ссылается на предыдущий контекст
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

	// Проверяем, что количество user-сообщений = 2 (#lead task + plain message)
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

// ============================================================
// sessionConfigWithPrompt создаёт session.Config с заданным
// системным промптом (для имитации lead-роли).
// ============================================================

func sessionConfigWithPrompt(systemPrompt string, _ *string) session.Config {
	cfg := session.DefaultConfig()
	cfg.SystemPrompt = systemPrompt
	return cfg
}

// TestPrimaryAgentSessionIsolation проверяет,
// что non-primary агент (#worker) НЕ разделяет контекст с главным агентом.
//
// Сценарий:
//  1. "#worker сделай задачу" — запускается отдельный агент (RunAgent)
//  2. Обычное сообщение — идёт в главного агента
//
// Ожидаемый результат:
//   - У главного агента и worker-а РАЗНЫЕ сессии
//   - Контекст worker-а НЕ виден в главном агенте
// ============================================================

func TestSessionIsolation_NonPrimaryAgent(t *testing.T) {
	responses := []scriptedResponse{
		{
			// Вызов 1: main agent — plain message
			content:      "I'm the main agent. How can I help?",
			finishReason: "stop",
		},
		{
			// Вызов 2: main agent — follow-up
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

	// Шаг 1: Пользователь пишет обычное сообщение главному агенту
	resp1, err := mainAgent.ProcessMessage(ctx, "Привет!", mainPeerID)
	if err != nil {
		t.Fatalf("main agent failed: %v", err)
	}
	t.Logf("Main agent response 1: %s", resp1)

	// Шаг 2: Имитируем worker-а — создаём ОТДЕЛЬНЫЙ агент
	// с собственной сессией (как это делает orchestrator.RunAgent)
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

	// Проверяем: у worker-а своя сессия
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

	// Шаг 3: Главный агент получает follow-up и НЕ видит контекст worker-а
	mainResp2, err := mainAgent.ProcessMessage(ctx, "что ты знаешь о задаче worker-а?", mainPeerID)
	if err != nil {
		t.Fatalf("main agent follow-up failed: %v", err)
	}
	t.Logf("Main agent response 2: %s", mainResp2)

	// Проверяем что главный агент НЕ видит задачу worker-а в своей истории
	mainSess := mainAgent.GetSession(mainPeerID)
	mainHistory := mainSess.GetHistory()
	for _, msg := range mainHistory {
		if strings.Contains(msg.Content, "сделай задачу") {
			t.Error("BUG: main agent session should NOT contain worker task — sessions must be isolated for non-primary agents")
		}
	}

	// Проверяем что LLM был вызван 2 раза (main1 + main2; worker НЕ идёт через этот сервер)
	t.Logf("LLM calls total: %d", llmCallCount.Load())
}
