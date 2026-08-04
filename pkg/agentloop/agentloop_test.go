package agentloop

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/opencode/llama-client/pkg/modelsconfig"
	"github.com/opencode/llama-client/pkg/tools"
)

// ============================================================
// Mock implementations для тестов
// ============================================================

// mockVKClient — mock для VKClient
type mockVKClient struct {
	mu             sync.Mutex
	messages       []string
	thinking       []string
	SendError      error
	SendErrorCount int
}

func (m *mockVKClient) SendMessage(peerID int64, text string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, text)
	if m.SendErrorCount > 0 {
		m.SendErrorCount--
		return 0, m.SendError
	}
	return 1, nil
}

func (m *mockVKClient) SendThinking(peerID int64, content string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.thinking = append(m.thinking, content)
	return 1, nil
}

func (m *mockVKClient) GetMessages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.messages
}

func (m *mockVKClient) GetThinking() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.thinking
}

// mockToolRegistry — mock для ToolRegistry
type mockToolRegistry struct {
	mu     sync.Mutex
	tools  map[string]tools.Tool
	schema []map[string]interface{}
}

func newMockToolRegistry() *mockToolRegistry {
	return &mockToolRegistry{
		tools: make(map[string]tools.Tool),
	}
}

func (m *mockToolRegistry) Get(name string) (tools.Tool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tool, ok := m.tools[name]
	return tool, ok
}

func (m *mockToolRegistry) ToOpenAISchema() []map[string]interface{} {
	return m.schema
}

func (m *mockToolRegistry) Register(name string, tool tools.Tool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tools[name] = tool
}

// ============================================================
// Тесты AgentLoop
// ============================================================

func testHolder() *modelsconfig.Holder {
	return modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: "http://localhost:8081"},
		},
	})
}

func TestNewAgentLoop(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()

	loop, err := NewAgentLoop(config, vk, reg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if loop == nil {
		t.Fatal("expected non-nil AgentLoop")
	}
}

func TestNewAgentLoopEmptyConfig(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := LoopConfig{}
	config.ModelHolder = testHolder()

	loop, err := NewAgentLoop(config, vk, reg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Должно использовать значения по умолчанию
	if loop != nil {
		// Проверяем что цикл создан
	}
}

func TestAgentLoopSyncVisionTool(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	content := `{
		"default": "vision",
		"models": {
			"vision": {"name": "vision-model", "host": "127.0.0.1:8081", "vision": true},
			"plain":  {"name": "plain-model", "host": "127.0.0.1:8081"}
		}
	}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	holder, err := modelsconfig.NewHolder(path)
	if err != nil {
		t.Fatal(err)
	}

	reg := tools.NewRegistry()
	reg.Register(&tools.FileReadTool{})

	config := DefaultLoopConfig()
	config.ModelHolder = holder
	loop, err := NewAgentLoop(config, &mockVKClient{}, reg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	al := loop.(*agentLoop)

	if !al.syncVisionTool() {
		t.Fatal("expected syncVisionTool to register image2text for vision model")
	}
	if !reg.IsRegistered("image2text") {
		t.Error("expected image2text to be registered after switching to vision model")
	}
	if !reg.IsRegistered("file_read") {
		t.Error("expected existing tools to remain registered")
	}

	if err := holder.Switch("plain"); err != nil {
		t.Fatal(err)
	}
	if al.syncVisionTool() {
		t.Fatal("expected syncVisionTool to unregister image2text for non-vision model")
	}
	if reg.IsRegistered("image2text") {
		t.Error("expected image2text to be unregistered after switching to non-vision model")
	}
	if !reg.IsRegistered("file_read") {
		t.Error("expected non-vision tools to remain registered")
	}
}

func TestAgentLoopGetSession(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()

	loop, err := NewAgentLoop(config, vk, reg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Сначала сессии нет
	if loop.GetSession(123) != nil {
		t.Error("expected nil session before any operation")
	}

	// Создаём сессию через ProcessPrompt (но это требует LLM)
	// Для теста просто проверяем что метод существует и не паникует
}

func TestAgentLoopResetSession(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()

	loop, err := NewAgentLoop(config, vk, reg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Не должно паниковать даже если сессии нет
	loop.ResetSession(123)
}

func TestAgentLoopResetSessionClearsPinned(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()

	loop, err := NewAgentLoop(config, vk, reg)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	sess := loop.EnsureSession(123)
	sess.AddPinned("Always answer in Russian")
	sess.AddUserMessage("Hello")

	loop.ResetSession(123)

	got := loop.GetSession(123).GetPinned()
	if len(got) != 0 {
		t.Errorf("expected pinned cleared after ResetSession, got %v", got)
	}
}

func TestEventDispatcherIntegration(t *testing.T) {
	dispatcher := NewEventDispatcher()
	events := []Event{}

	dispatcher.Register(EventPromptReceived, func(event Event) {
		events = append(events, event)
	})
	dispatcher.Register(EventResponseDone, func(event Event) {
		events = append(events, event)
	})

	dispatcher.Emit(NewEvent(EventPromptReceived, 123))
	dispatcher.Emit(NewEvent(EventResponseDone, 123))

	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}

func TestDefaultLogger(t *testing.T) {
	l := NewDefaultLogger(false)
	if l == nil {
		t.Fatal("expected non-nil logger")
	}

	// Проверяем что все методы вызываются без ошибок
	l.DebugLog("test")
	l.InfoLog("test")
	l.WarnLog("test")
	l.ErrorLog("test")
	l.DebugLogf("test %s", "arg")
	l.InfoLogf("test %s", "arg")
	l.WarnLogf("test %s", "arg")
	l.ErrorLogf("test %s", "arg")
}

func TestTruncate(t *testing.T) {
	// Тестируем утилиту truncate
	short := "hello"
	long := "this is a very long string that should be truncated"

	if truncate(short, 100) != short {
		t.Error("short string should not be truncated")
	}
	// truncate возвращает s[:maxLen] + "...", поэтому длина будет maxLen + 3
	if len(truncate(long, 10)) != 13 {
		t.Errorf("expected length 13, got %d", len(truncate(long, 10)))
	}
	// Проверяем что заканчивается на "..."
	if truncate(long, 10)[10] != '.' || truncate(long, 10)[11] != '.' || truncate(long, 10)[12] != '.' {
		t.Error("truncated string should end with ...")
	}
}

// ============================================================
// Тесты Loop Detection
// ============================================================

func TestSimilarityExactMatch(t *testing.T) {
	sim := similarity("Hello world", "Hello world")
	if sim != 1.0 {
		t.Errorf("expected similarity 1.0 for identical strings, got %f", sim)
	}
}

func TestSimilarityDifferentStrings(t *testing.T) {
	sim := similarity("Hello world", "Goodbye world")
	if sim < 0.0 || sim > 1.0 {
		t.Errorf("similarity should be between 0.0 and 1.0, got %f", sim)
	}
	// "world" совпадает, поэтому similarity > 0
	if sim <= 0.0 {
		t.Errorf("expected some similarity due to common word 'world', got %f", sim)
	}
}

func TestSimilarityEmptyStrings(t *testing.T) {
	sim := similarity("", "anything")
	if sim != 0.0 {
		t.Errorf("expected similarity 0.0 for empty string, got %f", sim)
	}
}

func TestSimilarityBothEmpty(t *testing.T) {
	sim := similarity("", "")
	if sim != 1.0 {
		t.Errorf("expected similarity 1.0 for two empty strings, got %f", sim)
	}
}

func TestSimilarityCaseInsensitive(t *testing.T) {
	sim := similarity("HELLO WORLD", "hello world")
	if sim != 1.0 {
		t.Errorf("expected similarity 1.0 for case-insensitive match, got %f", sim)
	}
}

// ============================================================
// Тесты Tool Processing
// ============================================================

func TestGetStringField(t *testing.T) {
	m := map[string]interface{}{
		"name":    "test",
		"value":   42,
		"enabled": true,
	}

	if getStringField(m, "name") != "test" {
		t.Error("expected 'test'")
	}
	if getStringField(m, "value") != "" {
		t.Error("expected '' for non-string value")
	}
	if getStringField(m, "missing") != "" {
		t.Error("expected '' for missing key")
	}
}

func TestProcessToolCallsEmpty(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()
	config.EnableThinking = true

	loop, _ := NewAgentLoop(config, vk, reg)

	results, err := loop.(*agentLoop).processToolCalls(context.Background(), []map[string]interface{}{}, nil, 123)
	if err != nil {
		t.Errorf("expected no error for empty tool calls, got %v", err)
	}
	if results != nil {
		t.Error("expected nil results for empty tool calls")
	}
}

func TestProcessToolCallsNoRegistry(t *testing.T) {
	vk := &mockVKClient{}
	var reg ToolRegistry = nil
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()

	loop, _ := NewAgentLoop(config, vk, reg)

	toolCalls := []map[string]interface{}{
		{"name": "file_read", "arguments": `{"path": "/test"}`},
	}

	results, err := loop.(*agentLoop).processToolCalls(context.Background(), toolCalls, nil, 123)
	if err != nil {
		t.Error("expected no error (tool not found is acceptable)")
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

// ============================================================
// Тесты Thinking Messages
// ============================================================

func TestSendThinkingDisabled(t *testing.T) {
	vk := &mockVKClient{}
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()
	config.EnableThinking = false

	loop, _ := NewAgentLoop(config, vk, nil)

	// Не должно вызывать ошибку
	loop.(*agentLoop).sendThinking(123, "Thinking content")
}

func TestSendThinkingNoThinkingPeerID(t *testing.T) {
	vk := &mockVKClient{}
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()
	config.EnableThinking = true
	config.ThinkingPeerID = 0

	loop, _ := NewAgentLoop(config, vk, nil)

	// Не должно вызывать ошибку
	loop.(*agentLoop).sendThinking(123, "Thinking content")
}

func TestSendThinkingWithVK(t *testing.T) {
	vk := &mockVKClient{}
	config := DefaultLoopConfig()
	config.ModelHolder = testHolder()
	config.EnableThinking = true
	config.ThinkingPeerID = 456

	loop, _ := NewAgentLoop(config, vk, nil)

	// sendThinking в agentloop вызывает vk.SendThinking
	// SendThinking в bot.go уже добавляет префикс "[THINKING]"
	loop.(*agentLoop).sendThinking(123, "Reading file...")

	thinking := vk.GetThinking()
	if len(thinking) != 1 {
		t.Errorf("expected 1 thinking message, got %d", len(thinking))
	}
	// Проверяем что сообщение отправлено (префикс добавляется в bot.go)
	if thinking[0] == "" {
		t.Error("expected non-empty thinking message")
	}
}

// ============================================================
// Тесты Event Dispatcher с обработчиками
// ============================================================

func TestEventDispatcherMultipleHandlers(t *testing.T) {
	dispatcher := NewEventDispatcher()
	count := 0

	dispatcher.Register(EventPromptReceived, func(e Event) { count++ })
	dispatcher.Register(EventPromptReceived, func(e Event) { count++ })
	dispatcher.Register(EventResponseDone, func(e Event) { count++ })

	dispatcher.Emit(NewEvent(EventPromptReceived, 123))
	dispatcher.Emit(NewEvent(EventResponseDone, 123))

	if count != 3 {
		t.Errorf("expected 3 handlers called, got %d", count)
	}
}

func TestEventDispatcherHandlerOrder(t *testing.T) {
	dispatcher := NewEventDispatcher()
	order := []int{}

	dispatcher.Register(EventPromptReceived, func(e Event) { order = append(order, 1) })
	dispatcher.Register(EventPromptReceived, func(e Event) { order = append(order, 2) })

	dispatcher.Emit(NewEvent(EventPromptReceived, 123))

	// Обработчики вызываются в порядке регистрации
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Errorf("expected handlers in order [1, 2], got %v", order)
	}
}
