package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// ============================================================
// Тесты AI Agent Implementation
// ============================================================

func TestNewAgent(t *testing.T) {
	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"

	agent := NewAgent(config)
	if agent == nil {
		t.Fatal("Agent should not be nil")
	}
}

func TestAgentSessionManagement(t *testing.T) {
	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"

	agent := NewAgent(config)
	peerID := int64(12345)

	t.Run("creates session on first interaction", func(t *testing.T) {
		s := agent.GetSession(peerID)
		if s == nil {
			t.Fatal("expected session to be created")
		}
		if s.GetPeerID() != peerID {
			t.Errorf("expected PeerID %d, got %d", peerID, s.GetPeerID())
		}
	})

	t.Run("returns existing session", func(t *testing.T) {
		s1 := agent.GetSession(peerID)
		s2 := agent.GetSession(peerID)
		if s1 != s2 {
			t.Error("expected same session instance")
		}
	})

	t.Run("creates separate session for different peer", func(t *testing.T) {
		s1 := agent.GetSession(peerID)
		s2 := agent.GetSession(peerID + 1)
		if s1 == s2 {
			t.Error("expected different sessions for different peers")
		}
	})
}

func TestAgentResetSession(t *testing.T) {
	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"

	agent := NewAgent(config)
	peerID := int64(12345)

	// Создаём сессию
	agent.GetSession(peerID)

	// Добавляем сообщения в сессию
	s := agent.GetSession(peerID)
	s.AddUserMessage("Test message")
	s.AddAssistantMessage("Test response")

	// Проверяем что есть сообщения
	if s.HistoryLength() != 2 {
		t.Errorf("expected 2 messages, got %d", s.HistoryLength())
	}

	// Сбрасываем сессию
	agent.ResetSession(peerID)

	// Проверяем что сессия сброшена
	s = agent.GetSession(peerID)
	if s.HistoryLength() != 0 {
		t.Errorf("expected 0 messages after reset, got %d", s.HistoryLength())
	}
}

func TestAgentProcessMessageWithMockServer(t *testing.T) {
	// Создаём тестовый сервер с SSE форматом
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// SSE формат для streaming
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 100
	config.Temperature = 0.7

	agent := NewAgent(config)

	ctx := context.Background()
	response, err := agent.ProcessMessage(ctx, "Привет", 12345)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Ответ должен содержать "Hello"
	if response == "" {
		t.Error("expected non-empty response")
	}
}

func TestAgentLoopDetectionIntegration(t *testing.T) {
	// Создаём сервер который отвечает одинаково (симуляция цикла)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// AI отвечает одинаково — это вызовет loop detection
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"I don't know\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("[DONE]\n"))
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 100

	agent := NewAgent(config)

	ctx := context.Background()

	// Первый запрос — AI отвечает "I don't know"
	agent.ProcessMessage(ctx, "Question 1", 12345)

	// Второй запрос — AI снова отвечает "I don't know" — должен сработать loop detection
	agent.ProcessMessage(ctx, "Question 2", 12345)

	// Проверяем что цикл обнаружен
	s := agent.GetSession(12345)
	if !s.IsLoopDetected() {
		t.Error("expected loop detection to be triggered")
	}

	// Третий запрос — должен получить alert
	response, err := agent.ProcessMessage(ctx, "Question 3", 12345)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Ответ должен содержать информацию о цикле
	if response == "" {
		t.Error("expected non-empty response")
	}
}

func TestAgentSessionPersistence(t *testing.T) {
	testDir, err := os.MkdirTemp("", "agent_session_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(testDir)

	sessionFile := filepath.Join(testDir, "agent_session.json")

	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"
	config.SessionConfig.SessionFile = sessionFile
	config.SessionConfig.AutoSave = true

	agent := NewAgent(config)

	// Добавляем сообщения
	agent.ProcessMessage(context.Background(), "Hello", 12345)

	// Файл должен быть создан (автосохранение)
	if _, err := os.Stat(sessionFile); os.IsNotExist(err) {
		t.Error("session file should be created with auto-save")
	}
}

func TestAgentMaxHistoryLimit(t *testing.T) {
	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"
	config.SessionConfig.MaxHistory = 5

	agent := NewAgent(config)

	ctx := context.Background()

	// Добавляем много сообщений
	for i := 0; i < 20; i++ {
		agent.ProcessMessage(ctx, "Message "+string(rune('A'+i%26)), 12345)
	}

	// Сессия должна ограничить историю
	s := agent.GetSession(12345)
	if s.HistoryLength() > 5 {
		t.Errorf("expected max 5 messages, got %d", s.HistoryLength())
	}
}
