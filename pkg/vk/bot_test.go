package vk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ============================================================
// Mock AI Agent для тестов
// ============================================================

type mockAgent struct {
	response string
	error    error
	called   bool
}

func (m *mockAgent) ProcessMessage(ctx context.Context, message string, peerID int64) (string, error) {
	m.called = true
	return m.response, m.error
}

func (m *mockAgent) ResetSession(peerID int64) {}
func (m *mockAgent) GetSession(peerID int64) interface{} { return nil }

// ============================================================
// Тесты VKBotClient
// ============================================================

func TestNewBotClient(t *testing.T) {
	t.Run("creates client with valid token", func(t *testing.T) {
		client := NewBotClient("test_token")

		if client == nil {
			t.Fatal("BotClient should not be nil")
		}
		if client.token != "test_token" {
			t.Errorf("expected token 'test_token', got '%s'", client.token)
		}
		if client.apiVersion != "5.200" {
			t.Errorf("expected API version '5.200', got '%s'", client.apiVersion)
		}
	})
}

func TestSendTextMessage(t *testing.T) {
	t.Run("sends message successfully", func(t *testing.T) {
		var receivedRequest map[string]interface{}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&receivedRequest)

			// Проверяем что запрос правильный
			if peerID, ok := receivedRequest["peer_id"].(float64); ok {
				if int64(peerID) != 12345 {
					t.Errorf("expected peer_id 12345, got %d", int64(peerID))
				}
			}

			// Возвращаем успешный ответ (массив с message_id)
			response := []map[string]interface{}{
				{"message_id": float64(1)},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		// Переопределяем baseURL для тестового сервера
		client := NewBotClient("test_token")
		client.baseURL = server.URL + "/method/"

		result, err := client.SendMessage(12345, "Hello, world!")

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if result != 1 {
			t.Errorf("expected message ID 1, got %d", result)
		}
	})

	t.Run("returns error on API error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response := map[string]interface{}{
				"error": map[string]interface{}{
					"error_code":    9,
					"error_message": "User not found",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		client := NewBotClient("test_token")
		client.baseURL = server.URL + "/method/"

		_, err := client.SendMessage(12345, "Hello")

		if err == nil {
			t.Fatal("expected error for invalid user, got nil")
		}
	})
}

func TestSendMessageWithSplitting(t *testing.T) {
	t.Run("splits long messages", func(t *testing.T) {
		requestCount := 0

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount++
			if requestCount >= 2 {
				// Возвращаем успешный ответ для всех частей
				response := []map[string]interface{}{
					{"message_id": float64(requestCount)},
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(response)
				return
			}
			// Первый запрос — пустой ответ
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewBotClient("test_token")
		client.baseURL = server.URL + "/method/"

		// Длинное сообщение (> 2000 символов)
		longText := ""
		for i := 0; i < 100; i++ {
			longText += "This is a test line for splitting.\n"
		}

		_, _ = client.SendMessage(12345, longText)

		// Должно быть отправлено несколько сообщений
		if requestCount < 2 {
			t.Errorf("expected multiple requests for long message, got %d", requestCount)
		}
	})
}

func TestSplitText(t *testing.T) {
	t.Run("splits text into parts", func(t *testing.T) {
		client := NewBotClient("test_token")

		// Создаём текст длиной > maxLength
		longText := ""
		for i := 0; i < 50; i++ {
			longText += "Test line " + string(rune('a'+i%26)) + "\n"
		}

		parts := client.splitText(longText, 200)

		if len(parts) == 0 {
			t.Fatal("expected at least one part")
		}
		if len(parts) == 1 {
			t.Error("expected multiple parts for long text")
		}
	})

	t.Run("returns single part for short text", func(t *testing.T) {
		client := NewBotClient("test_token")

		shortText := "Short text"
		parts := client.splitText(shortText, 200)

		if len(parts) != 1 {
			t.Errorf("expected 1 part for short text, got %d", len(parts))
		}
	})
}

func TestCreateQuestionKeyboard(t *testing.T) {
	t.Run("creates keyboard with options", func(t *testing.T) {
		options := []map[string]string{
			{"label": "Option 1"},
			{"label": "Option 2"},
			{"label": "Option 3"},
		}

		keyboard := CreateQuestionKeyboard("Question:", "Choose an option:", options)

		if keyboard == nil {
			t.Fatal("keyboard should not be nil")
		}

		// Проверяем что inline = false
		inline, ok := keyboard["inline"].(bool)
		if !ok || inline {
			t.Error("expected inline to be false")
		}

		// Проверяем кнопки
		buttons, ok := keyboard["buttons"].([][]map[string]interface{})
		if !ok {
			t.Fatal("expected buttons to be [][]map[string]interface{}")
		}
		if len(buttons) != 3 {
			t.Errorf("expected 3 button rows, got %d", len(buttons))
		}
	})
}

func TestCreateKeyboard(t *testing.T) {
	t.Run("creates keyboard with buttons", func(t *testing.T) {
		buttons := [][]map[string]interface{}{
			{
				{
					"action": map[string]interface{}{"type": "text", "label": "Button 1"},
					"color":  "primary",
				},
			},
		}

		keyboard := CreateKeyboard(buttons)

		if keyboard == nil {
			t.Fatal("keyboard should not be nil")
		}

		keyboardButtons, ok := keyboard["buttons"].([][]map[string]interface{})
		if !ok || len(keyboardButtons) != 1 {
			t.Error("expected 1 button row")
		}
	})
}
