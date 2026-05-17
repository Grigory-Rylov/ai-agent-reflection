package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// Тесты Session — сущность для хранения истории сессии
// ============================================================

func TestNewSession(t *testing.T) {
	testDir, err := os.MkdirTemp("", "session_new_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(testDir)

	sessionFile := filepath.Join(testDir, "session.json")

	t.Run("creates session with config", func(t *testing.T) {
		config := DefaultConfig()
		config.PeerID = 12345
		config.SessionFile = sessionFile
		config.MaxHistory = 100
		config.SystemPrompt = "You are helpful."

		s := NewSession(config)
		if s == nil {
			t.Fatal("Session should not be nil")
		}
		if s.GetPeerID() != 12345 {
			t.Errorf("expected PeerID 12345, got %d", s.GetPeerID())
		}
		// HistoryLength не считает системное сообщение
		if s.HistoryLength() != 0 {
			t.Errorf("expected 0 user messages (system only), got %d", s.HistoryLength())
		}
	})
}

func TestAddMessages(t *testing.T) {
	config := DefaultConfig()
	config.PeerID = 12345
	s := NewSession(config)

	t.Run("adds user message", func(t *testing.T) {
		s.AddUserMessage("Hello")
		// HistoryLength не считает системное сообщение
		if s.HistoryLength() != 1 {
			t.Errorf("expected 1 user message, got %d", s.HistoryLength())
		}
	})

	t.Run("adds assistant message", func(t *testing.T) {
		s.AddAssistantMessage("Hi there!")
		// HistoryLength не считает системное сообщение
		if s.HistoryLength() != 2 {
			t.Errorf("expected 2 user messages, got %d", s.HistoryLength())
		}
	})

	t.Run("tracks last assistant message", func(t *testing.T) {
		last := s.GetLastAssistantMessage()
		if last == nil {
			t.Fatal("expected last assistant message")
		}
		if last.Content != "Hi there!" {
			t.Errorf("expected 'Hi there!', got '%s'", last.Content)
		}
	})
}

func TestGetHistory(t *testing.T) {
	config := DefaultConfig()
	config.PeerID = 12345
	s := NewSession(config)

	s.AddUserMessage("Question 1")
	s.AddAssistantMessage("Answer 1")
	s.AddUserMessage("Question 2")
	s.AddAssistantMessage("Answer 2")

	history := s.GetHistory()
	if len(history) != 5 { // system + 4 messages
		t.Errorf("expected 5 messages in history, got %d", len(history))
	}
}

// ============================================================
// Тесты Loop Detection — обнаружение зацикливания
// ============================================================

func TestLoopDetection(t *testing.T) {
	config := DefaultConfig()
	config.PeerID = 12345
	config.MaxLoopHistory = 5 // храним последние 5 ответов AI
	s := NewSession(config)

	t.Run("no loop detected initially", func(t *testing.T) {
		s.AddAssistantMessage("First response")
		if s.IsLoopDetected() {
			t.Error("should not detect loop on first response")
		}
	})

	t.Run("detects exact duplicate", func(t *testing.T) {
		// Добавляем одинаковые ответы
		s.AddAssistantMessage("I don't know")
		s.AddAssistantMessage("I don't know")

		if !s.IsLoopDetected() {
			t.Error("should detect loop on exact duplicate")
		}
	})

	t.Run("tracks loop count", func(t *testing.T) {
		// Добавляем ещё один дубликат
		s.AddAssistantMessage("I don't know")

		// Loop count должен увеличиваться
		loops := s.GetLoopCount()
		if loops < 1 {
			t.Errorf("expected loop count >= 1, got %d", loops)
		}
	})

	t.Run("reset clears loop detection", func(t *testing.T) {
		s.ResetLoopDetection()
		if s.IsLoopDetected() {
			t.Error("loop detection should be cleared after reset")
		}
	})
}

func TestLoopDetectionWithSimilarity(t *testing.T) {
	config := DefaultConfig()
	config.PeerID = 12345
	config.MaxLoopHistory = 3
	config.LoopSimilarityThreshold = 0.8 // 80% схожесть
	s := NewSession(config)

	t.Run("detects similar responses", func(t *testing.T) {
		// Добавляем похожие ответы
		s.AddAssistantMessage("I'm sorry, I cannot help with that.")
		s.AddAssistantMessage("I'm sorry, I can't help with that.")

		// Должно обнаружить схожесть
		if !s.IsLoopDetected() {
			t.Error("should detect similar responses as loop")
		}
	})

	t.Run("does not detect different responses", func(t *testing.T) {
		// Создаём новую сессию с пустым SessionFile (не загружать из файла)
		s2 := NewSession(Config{
			PeerID:                  12345,
			SessionFile:             "", // не загружать из файла
			MaxLoopHistory:          3,
			LoopSimilarityThreshold: 0.8,
		})
		s2.AddAssistantMessage("The answer is 42.")
		s2.AddAssistantMessage("Hello, how are you today?")

		// Проверка: разные ответы не должны вызывать loop detection
		if s2.IsLoopDetected() {
			t.Error("should not detect loop for different responses")
		}
		if s2.GetLoopCount() != 0 {
			t.Errorf("expected loop count 0, got %d", s2.GetLoopCount())
		}
	})
}

func TestLoopAlertMessage(t *testing.T) {
	config := DefaultConfig()
	config.PeerID = 12345
	config.MaxLoopHistory = 3
	s := NewSession(config)

	// Создаём цикл
	s.AddAssistantMessage("Repeated message A")
	s.AddAssistantMessage("Repeated message A")

	alert := s.GetLoopAlertMessage()
	if alert == "" {
		t.Error("expected non-empty loop alert message")
	}
	if !strings.Contains(alert, "loop") && !strings.Contains(alert, "repeat") {
		t.Error("alert should mention 'loop' or 'repeat'")
	}
}

// ============================================================
// Тесты Session Persistence — сохранение в файл
// ============================================================

func TestSessionPersistence(t *testing.T) {
	testDir, err := os.MkdirTemp("", "session_persist_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(testDir)

	sessionFile := filepath.Join(testDir, "session.json")

	t.Run("saves session to file", func(t *testing.T) {
		config := DefaultConfig()
		config.PeerID = 12345
		config.SessionFile = sessionFile
		config.AutoSave = true

		s := NewSession(config)
		s.AddUserMessage("User message")
		s.AddAssistantMessage("Assistant reply")

		// Файл должен быть создан (автосохранение)
		if _, err := os.Stat(sessionFile); os.IsNotExist(err) {
			t.Error("session file should be created with auto-save")
		}
	})

	t.Run("loads session from file", func(t *testing.T) {
		// Сначала создаём сессию
		config1 := DefaultConfig()
		config1.PeerID = 12345
		config1.SessionFile = sessionFile
		s1 := NewSession(config1)
		s1.AddUserMessage("Original message")
		s1.AddAssistantMessage("Original reply")
		s1.Save()

		// Загружаем в новую сессию
		config2 := DefaultConfig()
		config2.PeerID = 12345
		config2.SessionFile = sessionFile
		s2 := NewSession(config2)

		// Должно загрузить историю
		if s2.HistoryLength() < 3 { // system + user + assistant
			t.Errorf("expected at least 3 messages, got %d", s2.HistoryLength())
		}
	})

	t.Run("persists loop detection state", func(t *testing.T) {
		// Создаём сессию с циклом
		config1 := DefaultConfig()
		config1.PeerID = 12345
		config1.SessionFile = sessionFile
		config1.MaxLoopHistory = 3
		s1 := NewSession(config1)

		s1.AddAssistantMessage("Loop message")
		s1.AddAssistantMessage("Loop message")

		// Сохраняем
		s1.Save()

		// Загружаем
		config2 := DefaultConfig()
		config2.PeerID = 12345
		config2.SessionFile = sessionFile
		config2.MaxLoopHistory = 3
		s2 := NewSession(config2)

		// Должно загрузить состояние цикла
		if !s2.IsLoopDetected() {
			t.Error("loop detection state should be persisted")
		}
	})
}

// ============================================================
// Тесты Session Reset и Clear
// ============================================================

func TestSessionReset(t *testing.T) {
	config := DefaultConfig()
	config.PeerID = 12345
	s := NewSession(config)

	s.AddUserMessage("Message 1")
	s.AddAssistantMessage("Reply 1")
	s.AddUserMessage("Message 2")
	s.AddAssistantMessage("Reply 2")

	// Сбрасываем сессию
	s.Reset()

	// Должно остаться только системное сообщение (HistoryLength не считает системное)
	if s.HistoryLength() != 0 {
		t.Errorf("expected 0 user messages after reset, got %d", s.HistoryLength())
	}

	// Loop detection должен быть очищен
	if s.IsLoopDetected() {
		t.Error("loop detection should be reset")
	}

	// Last assistant message должен быть nil
	if s.GetLastAssistantMessage() != nil {
		t.Error("last assistant message should be nil after reset")
	}
}

// ============================================================
// Тесты Session Config
// ============================================================

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.MaxHistory != 100 {
		t.Errorf("expected MaxHistory 100, got %d", config.MaxHistory)
	}
	if config.MaxLoopHistory != 5 {
		t.Errorf("expected MaxLoopHistory 5, got %d", config.MaxLoopHistory)
	}
	if config.LoopSimilarityThreshold != 0.85 {
		t.Errorf("expected LoopSimilarityThreshold 0.85, got %f", config.LoopSimilarityThreshold)
	}
	if config.AutoSave {
		t.Error("expected AutoSave false by default")
	}
}
