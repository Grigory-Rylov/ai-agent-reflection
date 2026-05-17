package context

import "testing"

func TestNewManager(t *testing.T) {
	t.Run("creates manager with default config", func(t *testing.T) {
		m := NewManager(DefaultConfig())
		if m == nil {
			t.Fatal("Manager should not be nil")
		}
		if m.messages == nil {
			t.Fatal("messages slice should not be nil")
		}
	})

	t.Run("adds system message by default", func(t *testing.T) {
		m := NewManager(DefaultConfig())
		if len(m.messages) != 1 {
			t.Errorf("expected 1 message (system), got %d", len(m.messages))
		}
		if m.messages[0].Role != SystemRole {
			t.Errorf("expected system role, got %s", m.messages[0].Role)
		}
	})

	t.Run("creates manager without system message", func(t *testing.T) {
		config := DefaultConfig()
		config.KeepSystemMessage = false
		m := NewManager(config)
		if len(m.messages) != 0 {
			t.Errorf("expected 0 messages, got %d", len(m.messages))
		}
	})
}

func TestAddUserMessage(t *testing.T) {
	m := NewManager(DefaultConfig())

	m.AddUserMessage("Hello")

	if len(m.messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(m.messages))
	}
	if m.messages[1].Role != UserRole {
		t.Errorf("expected user role, got %s", m.messages[1].Role)
	}
	if m.messages[1].Content != "Hello" {
		t.Errorf("expected content 'Hello', got '%s'", m.messages[1].Content)
	}
}

func TestAddAssistantMessage(t *testing.T) {
	m := NewManager(DefaultConfig())

	m.AddAssistantMessage("Hi there!")

	if len(m.messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(m.messages))
	}
	if m.messages[1].Role != AssistantRole {
		t.Errorf("expected assistant role, got %s", m.messages[1].Role)
	}
	if m.messages[1].Content != "Hi there!" {
		t.Errorf("expected content 'Hi there!', got '%s'", m.messages[1].Content)
	}
}

func TestGetMessages(t *testing.T) {
	m := NewManager(DefaultConfig())
	m.AddUserMessage("Test")
	m.AddAssistantMessage("Response")

	messages := m.GetMessages()

	if len(messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(messages))
	}

	// Verify returned slice is a copy
	messages[1].Content = "Modified"
	if m.GetMessages()[1].Content == "Modified" {
		t.Error("GetMessages should return a copy, not reference")
	}
}

func TestReset(t *testing.T) {
	t.Run("resets with system message preserved", func(t *testing.T) {
		m := NewManager(DefaultConfig())
		m.AddUserMessage("Test 1")
		m.AddAssistantMessage("Response 1")
		m.AddUserMessage("Test 2")

		m.Reset()

		if len(m.messages) != 1 {
			t.Errorf("expected 1 message after reset, got %d", len(m.messages))
		}
		if m.messages[0].Role != SystemRole {
			t.Error("system message should be preserved")
		}
	})

	t.Run("resets without system message", func(t *testing.T) {
		config := DefaultConfig()
		config.KeepSystemMessage = false
		m := NewManager(config)
		m.AddUserMessage("Test")

		m.Reset()

		if len(m.messages) != 0 {
			t.Errorf("expected 0 messages after reset, got %d", len(m.messages))
		}
	})
}

func TestHistoryLength(t *testing.T) {
	m := NewManager(DefaultConfig())

	if m.HistoryLength() != 0 {
		t.Error("initial history length should be 0")
	}

	m.AddUserMessage("Hello")
	if m.HistoryLength() != 1 {
		t.Error("history length should be 1 after adding user message")
	}

	m.AddAssistantMessage("Hi")
	if m.HistoryLength() != 2 {
		t.Error("history length should be 2 after adding assistant message")
	}
}

func TestHistoryText(t *testing.T) {
	m := NewManager(DefaultConfig())
	m.AddUserMessage("Test question")
	m.AddAssistantMessage("Test answer")

	history := m.HistoryText()

	if history == "" {
		t.Error("history text should not be empty")
	}
}

func TestMaxMessagesLimit(t *testing.T) {
	config := DefaultConfig()
	config.MaxMessages = 3
	m := NewManager(config)

	// Add 10 messages
	for i := 0; i < 10; i++ {
		m.AddUserMessage("User message")
		m.AddAssistantMessage("Assistant response")
	}

	// Should keep only MaxMessages pairs + system
	expectedMax := 1 + (config.MaxMessages * 2) // system + 3 pairs
	if len(m.messages) > expectedMax {
		t.Errorf("expected at most %d messages, got %d", expectedMax, len(m.messages))
	}
}
