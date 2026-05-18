package vk

import (
	"context"
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
}

func (m *mockAgentLoop) ProcessPrompt(ctx context.Context, prompt string, peerID int64) (string, error) {
	m.lastMessage = prompt
	m.lastPeerID = peerID
	return "processed: " + prompt, nil
}

func (m *mockAgentLoop) ProcessMessage(ctx context.Context, prompt string, peerID int64) (string, error) {
	return m.ProcessPrompt(ctx, prompt, peerID)
}

func (m *mockAgentLoop) Start(ctx context.Context)           {}
func (m *mockAgentLoop) Stop()                               {}
func (m *mockAgentLoop) ResetSession(peerID int64)           {}
func (m *mockAgentLoop) GetSession(peerID int64) *session.Session { return nil }
func (m *mockAgentLoop) SetThinkingCallback(cb func(peerID int64, content string) error) {}
func (m *mockAgentLoop) GetContextStats(peerID int64) (int, int, error) { return 0, 0, nil }

// ============================================================
// Тесты обработки команд
// ============================================================

func TestCommandsDoNotReachModel(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := &mockAgentLoop{}
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
	mock := &mockAgentLoop{}
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
	mock := &mockAgentLoop{}
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
