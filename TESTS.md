# Testing Guide

## Overview

Проект использует три уровня тестирования:

| Уровень | Где | Что проверяет | Зависимости |
|---------|-----|---------------|-------------|
| **Unit** | `*_test.go` | Логика изолированных функций, парсинг, команды | Нет |
| **Integration (mocked LLM)** | `pkg/agent/*_test.go` | Agent loop с tool calling, пустые ответы, overflow | `httptest.Server` |
| **Scenario** | `pkg/agentloop/run_scenario_test.go` | Полный пайплайн coordinator→worker→reviewer | Файлы в `testdata/scenarios/` |
| **Handler** | `pkg/vk/handler_test.go` | Роутинг сообщений (#lead, /commands, plain) | Mock-интерфейсы |

## Быстрый запуск

```bash
# Все тесты
go test ./...

# Только unit + integration (без integration build tag)
go test -v ./pkg/agent/

# Сценарные тесты оркестратора
go test -v -run "TestScenario" ./pkg/agentloop/

# Handler tests
go test -v ./pkg/vk/
```

---

## 1. Mock-интерфейсы (handler tests)

Самый простой способ протестировать логику роутинга и обработки команд — реализовать mock для интерфейса.

### Пример: mockAgentLoop

Файл `pkg/vk/handler_test.go:24` — mock реализует `agentloop.AgentLoop`:

```go
type mockAgentLoop struct {
    lastMessage    string      // что было отправлено в LLM
    lastPeerID     int64
    lastExtraSystem string    // для проверки ProcessPromptWithExtraSystem
    sessions       map[int64]*session.Session
    returnErr      error
}

func (m *mockAgentLoop) ProcessPrompt(ctx context.Context, prompt string, peerID int64) (string, error) {
    m.lastMessage = prompt
    m.lastPeerID = peerID
    // Сохраняем в сессию для проверки контекста
    sess := m.getOrCreateSession(peerID)
    sess.AddUserMessage(prompt)
    sess.AddAssistantMessage("processed: " + prompt)
    return "processed: " + prompt, nil
}
```

### Когда использовать

- Тестирование команд (`/clear`, `/help`, `/status`) — не должны уходить в LLM
- Реакция на ошибки (`context.Canceled`, обёрнутые ошибки)
- Роутинг `#agent_name` (primary vs non-primary)
- Сохранение контекста в сессию

### Пример: mockOrchestrator

```go
type mockOrchestrator struct {
    lastTask      string
    fixedResponse string
    primaryAgents map[string]bool   // кто primary
    systemPrompts map[string]string // промпты агентов
    agentNames    []string
}
```

---

## 2. Мокирование LLM через httptest.Server

Самый мощный способ — подмена llama-server на тестовый HTTP-сервер, который отдаёт SSE (Server-Sent Events) ответы.

### Формат SSE-ответа

Каждый ответ LLM приходит в формате:

```
data: {"choices":[{"delta":{"content":"текст ответа"},"finish_reason":"stop"}]}

data: [DONE]
```

Для tool calling:

```
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"task","arguments":"{\"subagent_type\":\"worker\",\"prompt\":\"...\"}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]
```

### Простой сервер — один ответ

Файл `pkg/agent/agent_test.go:283`:

```go
func newMockSSEServer(responseText string) *httptest.Server {
    return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/event-stream")
        w.Write([]byte(`data: {"choices":[{"delta":{"content":"` + responseText + `"}}]}` + "\n\n"))
        w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
        w.Write([]byte("[DONE]\n"))
    }))
}
```

Использование:

```go
server := newMockSSEServer("Hello, world!")
defer server.Close()

config := DefaultConfig()
config.LlamaServerURL = server.URL
config.Model = "test-model"

a := NewAgent(config)
response, err := a.ProcessMessage(ctx, "Привет", peerID)
```

### Scripted-сервер — разные ответы по счётчику

Файл `pkg/agent/primary_context_test.go:63` — для многошаговых сценариев:

```go
type scriptedResponse struct {
    content      string // текст ответа
    toolCalls    string // JSON для tool_calls (пусто = нет)
    finishReason string
}

func newScriptedLLMServer(responses []scriptedResponse) (*httptest.Server, *atomic.Int32) {
    var callCount atomic.Int32
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        n := int(callCount.Add(1)) - 1
        w.Header().Set("Content-Type", "text/event-stream")
        if n >= len(responses) {
            // fallback
            return
        }
        resp := responses[n]
        if resp.toolCalls != "" {
            fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":%s},"finish_reason":"%s"}]}`+"\n\n",
                resp.toolCalls, resp.finishReason)
        } else {
            fmt.Fprintf(w, `data: {"choices":[{"delta":{"content":"%s"},"finish_reason":"%s"}]}`+"\n\n",
                resp.content, resp.finishReason)
        }
        fmt.Fprint(w, "data: [DONE]\n\n")
    }))
    return server, &callCount
}
```

Использование — полный сценарий lead→worker→lead→follow-up:

```go
responses := []scriptedResponse{
    {
        // LLM вызывает task-инструмент
        toolCalls:    `[{"index":0,"id":"call_1","type":"function","function":{"name":"task","arguments":"{\"subagent_type\":\"worker\",\"prompt\":\"сделай задачу\"}"}}]`,
        finishReason: "tool_calls",
    },
    {
        // LLM получает результат инструмента — финальный ответ
        content:      "Задача выполнена! Worker создал структуру.",
        finishReason: "stop",
    },
    {
        // follow-up сообщение
        content:      "Исходя из предыдущей работы, проект состоит из трёх пакетов.",
        finishReason: "stop",
    },
}
server, callCount := newScriptedLLMServer(responses)
defer server.Close()

// ... создаём агента, ProcessMessage, проверяем callCount и сессию
```

### Сервер с атомарным счётчиком для ретраев

Файл `pkg/agent/empty_response_retry_test.go:14` — первые N вызовов отдают пустой ответ:

```go
var callCount atomic.Int32
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    n := callCount.Add(1)
    if n <= 2 {
        // Пустой ответ — вызовет retry
        fmt.Fprint(w, `data: {"choices":[{"delta":{"content":""},"finish_reason":"stop"}]}`+"\n\n")
    } else {
        // Настоящий ответ
        fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"Real response"},"finish_reason":"stop"}]}`+"\n\n")
    }
    fmt.Fprint(w, "data: [DONE]\n\n")
}))
```

---

## 3. Mock-инструменты

Вместо реального `SubAgentTool` (который требует ModelHolder, SlotManager, Store) для тестов достаточно простого mock-а:

```go
type mockTaskTool struct {
    workerResult string
    callCount    *atomic.Int32
}

func (t *mockTaskTool) Name() string        { return "task" }
func (t *mockTaskTool) Description() string  { return "Launch a sub-agent" }

func (t *mockTaskTool) Schema() map[string]interface{} {
    return map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "subagent_type": tools.CreateStringParameter("subagent_type", "Agent type", true),
            "prompt":        tools.CreateStringParameter("prompt", "Task", true),
        },
        "required": []string{"subagent_type", "prompt"},
    }
}

func (t *mockTaskTool) Execute(ctx context.Context, inputs map[string]string) (tools.ToolResult, error) {
    t.callCount.Add(1)
    return tools.ToolResult{
        Success: true,
        Data:    map[string]interface{}{"response": t.workerResult},
    }, nil
}
```

Регистрация инструментов на агенте:

```go
reg := tools.NewRegistry()
reg.Register(mockTask)

a := NewAgent(config)
// agentImpl имеет метод RegisterTools — регистрируем через type assertion
if impl, ok := interface{}(a).(interface{ RegisterTools(*tools.Registry) }); ok {
    impl.RegisterTools(reg)
}
```

Доступные mock-объекты в кодовой базе:

| Mock | Файл | Для чего |
|------|------|----------|
| `mockAgentLoop` | `pkg/vk/handler_test.go` | Тесты handler-а |
| `mockOrchestrator` | `pkg/vk/handler_test.go` | Тесты handler-а |
| `mockTaskTool` | `pkg/agent/primary_context_test.go` | Интеграционные тесты tool calling |
| `StubToolExecutor` | `pkg/agent/tool_executor_stub.go` | Заглушка всех tool-вызовов |
| `mockAutoContinueCompressor` | `pkg/agent/tool_result_processor_test.go` | Тесты компактизации |
| `mockPermissionChecker` | `pkg/agent/tool_permission_test.go` | Тесты прав доступа |

---

## 4. Сценарные тесты оркестратора

Для тестирования полного цикла coordinator→worker→reviewer **на реальном LLM** без перезапуска сервера между шагами. Сценарий — это директория с файлами, каждый из которых содержит ответ LLM на соответствующем шаге.

### Структура сценария

```
pkg/agentloop/testdata/scenarios/<name>/
├── prompt.txt              # Задача пользователя
├── 000_coordinator.txt     # Ответ coordinator (tool_call для worker)
├── 001_developer.txt       # Ответ worker (tool_call для reviewer)
├── 002_reviewer.txt        # Ответ reviewer (approve / revise)
├── 003_reviewer_result.txt # Ответ reviewer после revise
└── assert.txt              # Проверки
```

### assert.txt

```
contains: <строка которая должна быть в финальном ответе>
not_contains: <строка которой быть НЕ должно>
```

### Добавление нового сценария

1. Создать директорию `pkg/agentloop/testdata/scenarios/<name>/`
2. Добавить `prompt.txt` и файлы ответов LLM
3. В `pkg/agentloop/run_scenario_test.go` добавить:

```go
func TestScenario_MyCase(t *testing.T) { runScenario(t, "my_case") }
```

### Запуск

```bash
go test -v -run "TestScenario" ./pkg/agentloop/
go test -v -run "TestScenario_RevisionCycle" ./pkg/agentloop/
```

---

## 5. Типичные сценарии тестирования

### Проверка сохранения контекста

На уровне handler-а (`pkg/vk/handler_test.go`):

```go
func TestPrimaryAgentSharesMainContext(t *testing.T) {
    mockOrch.primaryAgents = map[string]bool{"lead": true}
    mockOrch.systemPrompts = map[string]string{"lead": "You are a Lead Agent."}

    // 1. #lead → ProcessPromptWithExtraSystem (общий контекст)
    handler.ProcessMessage("#lead создай проект", peerID)

    // 2. Сессия содержит задачу #lead
    sess := mock.GetSession(peerID)
    // ... проверяем sess.GetHistory()

    // 3. Plain сообщение → ProcessMessage (тот же агент, та же сессия)
    handler.ProcessMessage("расскажи про проект", peerID)
    // mock.lastMessage содержит "расскажи про проект" — контекст общий
}
```

На уровне agent-а (`pkg/agent/primary_context_test.go`):

```go
func TestPrimaryAgentContextSharing(t *testing.T) {
    // 1. Scripted LLM: вызов 1 = tool_call, вызов 2 = финальный ответ
    // 2. ProcessMessage("#lead task") → tool calling → результат
    // 3. ProcessMessage("plain follow-up") → тот же агент/session
    // 4. Проверяем: сессия содержит оба user-сообщения
}
```

### Проверка изоляции non-primary агентов

```go
func TestSessionIsolation_NonPrimaryAgent(t *testing.T) {
    // Главный агент и worker — разные экземпляры, разные peerID
    mainAgent := NewAgent(mainConfig)
    workerAgent := NewAgent(workerConfig)

    mainAgent.ProcessMessage(ctx, "Привет", mainPeerID)   // сессия main
    workerAgent.ProcessMessage(ctx, "сделай задачу", workerPeerID) // сессия worker

    // mainAgent НЕ должен видеть задачу worker-а в своей истории
}
```

### Проверка ретраев пустого ответа

```go
func TestProcessToolResults_EmptyResponseRetries(t *testing.T) {
    // Сервер отдаёт пустой ответ первые 2 раза, реальный на 3-й
    // Проверяем: после ретраев ответ не пустой, LLM вызван ≥ 3 раз
}
```

---

## 6. Структура тестовых файлов

```
pkg/agent/
├── agent_test.go                       # Базовые тесты агента
├── primary_context_test.go             # Интеграционные тесты primary-агентов ← NEW
├── empty_response_retry_test.go        # Тесты ретраев пустых ответов
├── tool_result_processor_test.go       # Тесты processToolResults
├── tool_permission_test.go             # Тесты прав доступа
├── context_overflow_integration_test.go # Тесты переполнения контекста
├── llm_integration_test.go             # Тесты на реальном LLM (build tag: integration)
├── auto_continue_test.go               # Тесты auto-continue после компактизации
├── compaction_test.go                  # Тесты компактизации
├── pinned_compact_test.go              # Тесты pinned-промптов при компактизации
├── repeated_compaction_test.go         # Тесты повторной компактизации
├── function_call_test.go               # Тесты function calling
├── tool_executor_stub_test.go          # Тесты StubToolExecutor
├── tool_executor_stub.go               # StubToolExecutor (заглушка)
├── xml_fallback_test.go                # Тесты XML-парсинга tool calls
├── streaming_retry_test.go             # Тесты ретраев при обрыве стрима
└── render_truncation_test.go           # Тесты обрезки вывода

pkg/agentloop/
├── agentloop_test.go                   # Тесты AgentLoop
├── orchestrator_test.go                # Тесты Orchestrator
├── run_scenario_test.go                # Раннер сценарных тестов
└── testdata/scenarios/                 # Данные сценариев
    ├── full_pipeline/
    ├── full_pipeline_json/
    ├── simple_approve/
    └── worker_task/

pkg/vk/
└── handler_test.go                     # Тесты BotHandler (роутинг, команды)

session/
└── session_test.go                     # Тесты Session
```

---

## 7. Чек-лист при написании теста

- [ ] **Mock-интерфейсы** — для тестов handler-а (роутинг `#agent`, команды, ошибки)
- [ ] **httptest.Server** — для тестов agent-а (tool calling, поток LLM-ответов)
- [ ] **Atomic-счётчики** — для проверки количества вызовов LLM / инструментов
- [ ] **Проверка сессии** — `session.GetHistory()` для верификации контекста
- [ ] **defer server.Close()** — освобождение ресурсов
- [ ] **peerID** — всегда уникальный в тесте, чтобы сессии не пересекались
- [ ] **Scripted-сервер** — если сценарий многошаговый (вызов 1→tool, вызов 2→ответ)
- [ ] **Mock tools** — вместо реальных инструментов (SubAgentTool требует много зависимостей)
- [ ] **build tag `integration`** — только для тестов, требующих реальный LLM-сервер
