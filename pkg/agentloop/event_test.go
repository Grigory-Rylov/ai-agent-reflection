package agentloop

import (
	"testing"
	"time"
)

// ============================================================
// Тесты событий
// ============================================================

func TestNewEventDispatcher(t *testing.T) {
	dispatcher := NewEventDispatcher()
	if dispatcher == nil {
		t.Fatal("expected non-nil dispatcher")
	}
	if dispatcher.handlers == nil {
		t.Fatal("expected non-nil handlers map")
	}
	if len(dispatcher.handlers) != 0 {
		t.Errorf("expected empty handlers map, got %d entries", len(dispatcher.handlers))
	}
}

func TestEventDispatcherRegister(t *testing.T) {
	dispatcher := NewEventDispatcher()

	dispatcher.Register(EventPromptReceived, func(event Event) {
		// Handler registered
	})

	if len(dispatcher.handlers[EventPromptReceived]) != 1 {
		t.Fatal("expected 1 handler registered")
	}
}

func TestEventDispatcherEmit(t *testing.T) {
	dispatcher := NewEventDispatcher()
	var receivedEvent Event

	dispatcher.Register(EventPromptReceived, func(event Event) {
		receivedEvent = event
	})

	event := NewEvent(EventPromptReceived, 123)
	dispatcher.Emit(event)

	if receivedEvent.Type != EventPromptReceived {
		t.Errorf("expected event type EventPromptReceived, got %s", receivedEvent.Type)
	}
	if receivedEvent.PeerID != 123 {
		t.Errorf("expected peerID 123, got %d", receivedEvent.PeerID)
	}
}

func TestEventDispatcherEmitNoHandler(t *testing.T) {
	dispatcher := NewEventDispatcher()
	// Не должно паниковать если нет обработчика
	dispatcher.Emit(NewEvent(EventError, 123))
}

func TestNewEvent(t *testing.T) {
	event := NewEvent(EventPromptReceived, 456)

	if event.Type != EventPromptReceived {
		t.Errorf("expected type EventPromptReceived, got %s", event.Type)
	}
	if event.PeerID != 456 {
		t.Errorf("expected peerID 456, got %d", event.PeerID)
	}
	if event.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
	if event.Data == nil {
		t.Error("expected non-nil Data map")
	}
}

func TestSetEventStringData(t *testing.T) {
	event := NewEvent(EventPromptReceived, 123)
	event = SetEventStringData(event, "key", "value")

	if event.Data["key"] != "value" {
		t.Errorf("expected 'value', got %v", event.Data["key"])
	}
}

func TestSetEventStringDataNilMap(t *testing.T) {
	event := Event{
		Type:      EventPromptReceived,
		PeerID:    123,
		Timestamp: time.Now(),
		Data:      nil,
	}
	event = SetEventStringData(event, "key", "value")

	if event.Data == nil {
		t.Fatal("expected non-nil Data map after SetEventStringData")
	}
	if event.Data["key"] != "value" {
		t.Errorf("expected 'value', got %v", event.Data["key"])
	}
}

func TestEventTypeConstants(t *testing.T) {
	expectedTypes := []EventType{
		EventPromptReceived,
		EventRequestSent,
		EventResponseChunk,
		EventResponseDone,
		EventToolCall,
		EventToolResult,
		EventLoopDetected,
		EventThinking,
		EventError,
	}

	for _, et := range expectedTypes {
		if et == "" {
			t.Errorf("expected non-empty EventType, got empty string")
		}
	}
}
