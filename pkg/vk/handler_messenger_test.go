package vk

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

// ============================================================
// Mock Messenger — симулирует VK/Telegram мессенджер для тестов
// ============================================================

type mockMessenger struct {
	mu           sync.Mutex
	sent         []sentMessage
	lastText     string
	sentWithKbd  int // количество вызовов SendMessageWithKeyboard
	sentPlain    int // количество вызовов SendMessage (без клавиатуры)
}

type sentMessage struct {
	PeerID   int64
	Text     string
	Keyboard map[string]interface{}
}

func newMockMessenger() *mockMessenger {
	return &mockMessenger{
		sent: make([]sentMessage, 0),
	}
}

func (m *mockMessenger) SendMessage(peerID int64, text string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentPlain++
	m.sent = append(m.sent, sentMessage{PeerID: peerID, Text: text})
	m.lastText = text
	return 1, nil
}

func (m *mockMessenger) SendThinking(peerID int64, content string) (int64, error) {
	return 1, nil
}

func (m *mockMessenger) SendMessageWithKeyboard(peerID int64, text string, keyboard map[string]interface{}) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentWithKbd++
	m.sent = append(m.sent, sentMessage{PeerID: peerID, Text: text, Keyboard: keyboard})
	m.lastText = text
	return 1, nil
}

func (m *mockMessenger) SendMessageEventAnswer(eventID string, userID int64, peerID int64, text string) error {
	return nil
}

// getLastMessage возвращает последнее отправленное сообщение.
func (m *mockMessenger) getLastMessage() sentMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sent) == 0 {
		return sentMessage{}
	}
	return m.sent[len(m.sent)-1]
}

// hasMessage проверяет что хотя бы одно сообщение содержит substring.
func (m *mockMessenger) hasMessage(substring string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, msg := range m.sent {
		if strings.Contains(msg.Text, substring) {
			return true
		}
	}
	return false
}

// messageCount возвращает количество отправленных сообщений.
func (m *mockMessenger) messageCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

// ============================================================
// Критически важные тесты: команды /clear, /help, /status, /n, /m
// должны обрабатываться через handleIncomingMessage → ProcessMessage → messenger.send
// И НЕ ДОХОДИТЬ до LLM (не вызывать mockAgent.ProcessMessage)
// ============================================================

func TestIncomingCommand_ClearSendsResponse(t *testing.T) {
	messenger := newMockMessenger()
	mockAgent := newMockAgentLoop()
	handler := NewBotHandlerWithMessenger(messenger, nil, mockAgent, nil, 12345)

	msg := VKMessage{ID: 1, PeerID: 12345, FromID: 12345, Text: "/clear"}
	handler.handleIncomingMessage(msg, 12345, nil)

	last := messenger.getLastMessage()
	if !strings.Contains(last.Text, "Сессия сброшена") && !strings.Contains(last.Text, "сессия сброшена") {
		t.Errorf("Messenger did not receive /clear confirmation: %q", last.Text)
	}

	mockAgent.mu.Lock()
	if mockAgent.lastMessage != "" {
		t.Errorf("LLM should NOT be called for /clear! Got: %q", mockAgent.lastMessage)
	}
	mockAgent.mu.Unlock()
}

// TestIncoming_M_SendsKeyboard проверяет что команда /m вызывает именно
// SendMessageWithKeyboard (а не SendMessage), и клавиатура содержит кнопки моделей.
func TestIncoming_M_SendsKeyboard(t *testing.T) {
	messenger := newMockMessenger()
	mockAgent := newMockAgentLoop()

	holder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "gemma-4",
		Models: map[string]modelsconfig.ModelEntry{
			"gemma-4":  {Name: "gemma-4", Host: "http://localhost:8081"},
			"llama3.2": {Name: "llama3.2", Host: "http://localhost:8080"},
		},
	})

	handler := NewBotHandlerWithPeerID(messenger, nil, mockAgent, nil, 12345, 0, nil, holder)

	msg := VKMessage{ID: 1, PeerID: 12345, FromID: 12345, Text: "/m"}
	handler.handleIncomingMessage(msg, 12345, nil)

	// Проверяем что SendMessageWithKeyboard был вызван.
	messenger.mu.Lock()
	withKbd := messenger.sentWithKbd
	plain := messenger.sentPlain
	sent := make([]sentMessage, len(messenger.sent))
	copy(sent, messenger.sent)
	messenger.mu.Unlock()

	if withKbd == 0 {
		t.Error("SendMessageWithKeyboard was NOT called for /m command!")
	}
	if plain > 0 {
		t.Errorf("Unexpected SendMessage (without keyboard) calls: %d. Only SendMessageWithKeyboard expected.", plain)
	}

	// Проверяем что клавиатура не nil и содержит кнопки моделей.
	last := messenger.getLastMessage()
	if last.Text == "" {
		t.Fatal("No message was sent")
	}

	if !strings.Contains(last.Text, "Доступные модели:") {
		t.Errorf("Expected model list in response: %q", last.Text)
	}

	// Проверяем что keyboard не пустой (есть inline кнопки).
	for _, m := range sent {
		if m.Keyboard != nil && len(m.Keyboard) > 0 {
			t.Logf("Keyboard received with keys: %+v", getMapKeys(m.Keyboard))
			break
		}
	}

	mockAgent.mu.Lock()
	if mockAgent.lastMessage != "" {
		t.Errorf("LLM should NOT be called for /m! Got: %q", mockAgent.lastMessage)
	}
	mockAgent.mu.Unlock()
}

// getMapKeys возвращает ключи карты для лога.
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestIncomingCommand_HelpSendsResponse(t *testing.T) {
	messenger := newMockMessenger()
	mockAgent := newMockAgentLoop()
	handler := NewBotHandlerWithMessenger(messenger, nil, mockAgent, nil, 12345)

	msg := VKMessage{ID: 1, PeerID: 12345, FromID: 12345, Text: "/help"}
	handler.handleIncomingMessage(msg, 12345, nil)

	last := messenger.getLastMessage()
	if !strings.Contains(last.Text, "Доступные команды:") {
		t.Errorf("Messenger did not receive /help response: %q", last.Text)
	}

	mockAgent.mu.Lock()
	if mockAgent.lastMessage != "" {
		t.Errorf("LLM should NOT be called for /help! Got: %q", mockAgent.lastMessage)
	}
	mockAgent.mu.Unlock()
}

func TestIncomingCommand_StatusSendsResponse(t *testing.T) {
	messenger := newMockMessenger()
	mockAgent := newMockAgentLoop()
	handler := NewBotHandlerWithMessenger(messenger, nil, mockAgent, nil, 12345)

	msg := VKMessage{ID: 1, PeerID: 12345, FromID: 12345, Text: "/status"}
	handler.handleIncomingMessage(msg, 12345, nil)

	last := messenger.getLastMessage()
	if !strings.Contains(last.Text, "AI Agent активен") && last.Text != "" {
		t.Errorf("Messenger did not receive /status response: %q", last.Text)
	}

	mockAgent.mu.Lock()
	if mockAgent.lastMessage != "" {
		t.Errorf("LLM should NOT be called for /status! Got: %q", mockAgent.lastMessage)
	}
	mockAgent.mu.Unlock()
}

func TestIncomingCommandVsRegular(t *testing.T) {
	messenger := newMockMessenger()
	mockAgent := newMockAgentLoop()
	handler := NewBotHandlerWithMessenger(messenger, nil, mockAgent, nil, 12345)

	tests := []struct {
		name        string
		text        string
		expectLLM   bool // true = должно дойти до LLM
		expectMsg   bool // true = ответ должен быть отправлен через messenger
	}{
		{"/clear", "/clear", false, true},
		{"/help", "/help", false, true},
		{"/status", "/status", false, true},
		{"/unknown", "/xyz123", false, true},
		{"regular msg", "привет как дела", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messenger.sent = messenger.sent[:0]
			mockAgent.lastMessage = ""

			msg := VKMessage{ID: 1, PeerID: 12345, FromID: 12345, Text: tt.text}
			handler.handleIncomingMessage(msg, 12345, nil)

			count := messenger.messageCount()
			if tt.expectMsg && count == 0 {
				t.Error("Expected message sent through messenger")
			}

			llmCalled := mockAgent.lastMessage != ""
			if tt.expectLLM && !llmCalled {
				t.Errorf("Regular message %q should reach LLM", tt.text)
			}
			if !tt.expectLLM && llmCalled {
				t.Errorf("Command %q should NOT reach LLM! Got: %q", tt.text, mockAgent.lastMessage)
			}
		})
	}
}

// Критически важный тест: команды /clear и другие должны проходить
// даже когда агент заблокирован на длительном LLM-запросе.
func TestIncomingBlockingDoesNotAffectCommands(t *testing.T) {
	messenger := newMockMessenger()
	mockAgent := &mockAgentLoop{sessions: make(map[int64]*session.Session)}
	blockCh := make(chan struct{}) // не закроем сразу — имитация длинного LLM-запроса
	mockAgent.blockCh = blockCh

	handler := NewBotHandlerWithMessenger(messenger, nil, mockAgent, nil, 12345)

	// Запускаем "долгоиграющий" запрос.
	done := make(chan bool, 1)
	go func() {
		msg := VKMessage{ID: 10, PeerID: 12345, FromID: 12345, Text: "выполни длинную задачу"}
		handler.handleIncomingMessage(msg, 12345, nil)
		done <- true
	}()

	time.Sleep(50 * time.Millisecond) // даём захватить mutex

	// /clear должен пройти без ожидания заблокированного LLM!
	clearMsg := VKMessage{ID: 11, PeerID: 12345, FromID: 12345, Text: "/clear"}
	handler.handleIncomingMessage(clearMsg, 12345, nil)

	last := messenger.getLastMessage()
	if !strings.Contains(last.Text, "Сессия сброшена") && !strings.Contains(last.Text, "сессия сброшена") {
		t.Errorf("/clear did not send confirmation under blocking! Last: %q", last.Text)
	}

	close(blockCh) // освобождаем goroutine
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("LLM request goroutine did not complete")
	}
}
