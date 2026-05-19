package vk

import (
	"context"
	"strings"
	"testing"

	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/session"
)

// ============================================================
// Mock agentloop для тестов
// ============================================================

type mockAgentLoop struct {
	lastMessage string
	lastPeerID  int64
	sessions    map[int64]*session.Session
}

func newMockAgentLoop() *mockAgentLoop {
	return &mockAgentLoop{
		sessions: make(map[int64]*session.Session),
	}
}

func (m *mockAgentLoop) ProcessPrompt(ctx context.Context, prompt string, peerID int64) (string, error) {
	m.lastMessage = prompt
	m.lastPeerID = peerID

	// Создаём или получаем сессию и добавляем сообщение
	sess := m.getOrCreateSession(peerID)
	sess.AddUserMessage(prompt)
	sess.AddAssistantMessage("processed: " + prompt)

	return "processed: " + prompt, nil
}

func (m *mockAgentLoop) ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error) {
	return m.ProcessPrompt(ctx, prompt, peerID)
}

func (m *mockAgentLoop) Start(ctx context.Context)           {}
func (m *mockAgentLoop) Stop()                               {}
func (m *mockAgentLoop) ResetSession(peerID int64)           {}
func (m *mockAgentLoop) SetThinkingCallback(cb func(peerID int64, content string) error) {}

func (m *mockAgentLoop) GetSession(peerID int64) *session.Session {
	return m.sessions[peerID]
}

func (m *mockAgentLoop) EnsureSession(peerID int64) *session.Session {
	return m.getOrCreateSession(peerID)
}

func (m *mockAgentLoop) GetContextStats(peerID int64) (int, int, error) {
	sess := m.sessions[peerID]
	if sess == nil {
		return 0, 0, nil
	}

	history := sess.GetHistory()
	charCount := 0
	for _, msg := range history {
		charCount += len(msg.Content)
	}

	// Для тестов просто возвращаем charCount как приблизительное количество токенов
	tokenCount := charCount / 4

	return charCount, tokenCount, nil
}

func (m *mockAgentLoop) getOrCreateSession(peerID int64) *session.Session {
	if sess, ok := m.sessions[peerID]; ok {
		return sess
	}
	config := session.DefaultConfig()
	config.PeerID = peerID
	sess := session.NewSession(config)
	m.sessions[peerID] = sess
	return sess
}

// ============================================================
// Тесты обработки команд
// ============================================================

func TestCommandsDoNotReachModel(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	tests := []struct {
		name    string
		message string
	}{
		{"reset command", "/reset"},
		{"clear command", "/clear"},
		{"help command", "/help"},
		{"status command", "/status"},
		{"newsession command", "/newsession /tmp"},
		{"unknown command", "/unknownxyz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock.lastMessage = ""
			_ = handler.ProcessMessage(tt.message, 12345)
			if mock.lastMessage != "" {
				t.Errorf("Command %q was sent to AI model: lastMessage=%q", tt.message, mock.lastMessage)
			}
		})
	}
}

func TestNormalMessagesReachModel(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	mock.lastMessage = ""
	response := handler.ProcessMessage("Hello, how are you?", 12345)

	if mock.lastMessage == "" {
		t.Error("Normal message was NOT sent to AI model")
	}
	if response != "processed: Hello, how are you?" {
		t.Errorf("Expected 'processed: Hello, how are you?', got %q", response)
	}
}

func TestCommandResponseFormats(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	tests := []struct {
		name       string
		message    string
		expectResp bool
	}{
		{"reset", "/reset", true},
		{"clear", "/clear", true},
		{"help", "/help", true},
		{"status", "/status", true},
		{"unknown", "/blabla", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := handler.ProcessMessage(tt.message, 12345)
			if tt.expectResp && response == "" {
				t.Errorf("Expected non-empty response for %q", tt.message)
			}
			t.Logf("Response for %q: %s", tt.message, response)
		})
	}
}

// ============================================================
// Тесты для /status - проверка бага с сообщениями и токенами
// ============================================================

func TestStatusShowsCorrectMessageCount(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	peerID := int64(12345)

	// Отправляем сообщение в AI (это создаст сессию в AgentLoop)
	_ = handler.ProcessMessage("Привет, как дела?", peerID)
	_ = handler.ProcessMessage("Расскажи анекдот", peerID)

	// Запрашиваем статус
	status := handler.ProcessMessage("/status", peerID)

	t.Logf("Status output:\n%s", status)

	// Проверяем, что сообщений > 0
	// После 2 сообщений должно быть 4 записи в истории (2 user + 2 assistant)
	// HistoryLength возвращает len(messages) - 1 (без системного)
	if strings.Contains(status, "Сообщений: 0") {
		t.Error("BUG: Status shows 0 messages but should show > 0 after processing messages")
	}
}

func TestStatusShowsCorrectTokenCount(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	peerID := int64(12345)

	// Отправляем сообщение в AI
	_ = handler.ProcessMessage("Привет, это тестовое сообщение для проверки подсчёта токенов", peerID)

	// Запрашиваем статус
	status := handler.ProcessMessage("/status", peerID)

	t.Logf("Status output:\n%s", status)

	// Проверяем, что токенов > 0
	if strings.Contains(status, "Токенов в контексте: 0") {
		t.Error("BUG: Status shows 0 tokens but should show > 0 after processing messages")
	}
}

func TestStatusShowsCorrectCharCount(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandler(nil, mock, log)

	peerID := int64(12345)

	// Отправляем сообщение в AI
	_ = handler.ProcessMessage("Тестовое сообщение", peerID)

	// Запрашиваем статус
	status := handler.ProcessMessage("/status", peerID)

	t.Logf("Status output:\n%s", status)

	// Проверяем, что символов > 0
	if strings.Contains(status, "Символов в контексте: 0") {
		t.Error("BUG: Status shows 0 chars but should show > 0 after processing messages")
	}
}
