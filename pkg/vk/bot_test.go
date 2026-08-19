package vk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)


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

			
			if peerID, ok := receivedRequest["peer_id"].(float64); ok {
				if int64(peerID) != 12345 {
					t.Errorf("expected peer_id 12345, got %d", int64(peerID))
				}
			}

			
			response := map[string]interface{}{
				"response": []map[string]interface{}{
					{"message_id": float64(1)},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		
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
				
				response := map[string]interface{}{
					"response": []map[string]interface{}{
						{"message_id": float64(requestCount)},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(response)
				return
			}
			
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := NewBotClient("test_token")
		client.baseURL = server.URL + "/method/"

		
		longText := ""
		for i := 0; i < 100; i++ {
			longText += "This is a test line for splitting.\n"
		}

		_, _ = client.SendMessage(12345, longText)

		
		if requestCount < 2 {
			t.Errorf("expected multiple requests for long message, got %d", requestCount)
		}
	})
}

func TestSplitText(t *testing.T) {
	t.Run("splits text into parts", func(t *testing.T) {
		client := NewBotClient("test_token")

		
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

		
		inline, ok := keyboard["inline"].(bool)
		if !ok || inline {
			t.Error("expected inline to be false")
		}

		
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

func TestParseMessageNewUpdate(t *testing.T) {
	t.Run("extracts incoming message fields", func(t *testing.T) {
		object := map[string]interface{}{
			"message": map[string]interface{}{
				"id":      float64(42),
				"peer_id": float64(2000000001),
				"from_id": float64(123),
				"date":    float64(1700000000),
				"out":     float64(0),
				"text":    "/m",
			},
		}

		msg := parseMessageNewUpdate(object)

		if msg.ID != 42 {
			t.Errorf("expected ID 42, got %d", msg.ID)
		}
		if msg.PeerID != 2000000001 {
			t.Errorf("expected PeerID 2000000001, got %d", msg.PeerID)
		}
		if msg.FromID != 123 {
			t.Errorf("expected FromID 123, got %d", msg.FromID)
		}
		if msg.Text != "/m" {
			t.Errorf("expected text /m, got %q", msg.Text)
		}
	})

	t.Run("skips outgoing messages", func(t *testing.T) {
		object := map[string]interface{}{
			"message": map[string]interface{}{
				"id":      float64(42),
				"peer_id": float64(2000000001),
				"out":     float64(1),
				"text":    "reply",
			},
		}

		msg := parseMessageNewUpdate(object)
		if msg.ID != 0 {
			t.Errorf("expected empty message for outgoing, got ID %d", msg.ID)
		}
	})

	t.Run("falls back to top-level message_id when message.id missing", func(t *testing.T) {
		
		
		
		object := map[string]interface{}{
			"message_id": float64(987654),
			"message": map[string]interface{}{
				"peer_id":   float64(2000000001),
				"from_id":   float64(123),
				"out":       float64(0),
				"text":      "смотри фото",
				"attachments": []interface{}{
					map[string]interface{}{
						"type":  "photo",
						"photo": map[string]interface{}{"id": float64(1)},
					},
				},
			},
		}

		msg := parseMessageNewUpdate(object)
		if msg.ID != 987654 {
			t.Errorf("expected ID 987654 from message_id fallback, got %d", msg.ID)
		}
		if len(msg.Attachments) != 1 {
			t.Errorf("expected 1 attachment, got %d", len(msg.Attachments))
		}
	})

	t.Run("message.id takes precedence over message_id", func(t *testing.T) {
		object := map[string]interface{}{
			"message_id": float64(111),
			"message": map[string]interface{}{
				"id":      float64(222),
				"peer_id": float64(2000000001),
				"out":     float64(0),
				"text":    "hi",
			},
		}

		msg := parseMessageNewUpdate(object)
		if msg.ID != 222 {
			t.Errorf("expected ID 222 from message.id, got %d", msg.ID)
		}
	})
}

func TestParseMessageEventUpdate(t *testing.T) {
	t.Run("extracts event fields and payload", func(t *testing.T) {
		object := map[string]interface{}{
			"user_id": float64(123),
			"peer_id": float64(2000000001),
			"event_id": "abc-def-123",
			"payload":  `{"command":"model_switch","alias":"gemma"}`,
		}

		msg := parseMessageEventUpdate(object)

		if msg.EventID != "abc-def-123" {
			t.Errorf("expected EventID abc-def-123, got %q", msg.EventID)
		}
		if msg.PeerID != 2000000001 {
			t.Errorf("expected PeerID 2000000001, got %d", msg.PeerID)
		}
		if msg.FromID != 123 {
			t.Errorf("expected FromID 123, got %d", msg.FromID)
		}
		if msg.Payload != `{"command":"model_switch","alias":"gemma"}` {
			t.Errorf("unexpected payload %q", msg.Payload)
		}
	})
}
