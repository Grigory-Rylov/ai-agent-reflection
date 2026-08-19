package agentloop

import (
	"time"
)


type EventType string

const (
	
	EventPromptReceived EventType = "prompt_received"

	
	EventRequestSent EventType = "request_sent"

	
	EventResponseChunk EventType = "response_chunk"

	
	EventResponseDone EventType = "response_done"

	
	EventToolCall EventType = "tool_call"

	
	EventToolResult EventType = "tool_result"

	
	EventLoopDetected EventType = "loop_detected"

	
	EventThinking EventType = "thinking"

	
	EventError EventType = "error"
)


type Event struct {
	Type      EventType
	PeerID    int64
	Timestamp time.Time
	Data      map[string]interface{}
}


type EventHandler func(event Event)


type EventDispatcher struct {
	handlers map[EventType][]EventHandler
}


func NewEventDispatcher() *EventDispatcher {
	return &EventDispatcher{
		handlers: make(map[EventType][]EventHandler),
	}
}


func (d *EventDispatcher) Register(eventType EventType, handler EventHandler) {
	d.handlers[eventType] = append(d.handlers[eventType], handler)
}


func (d *EventDispatcher) Emit(event Event) {
	if handlers, ok := d.handlers[event.Type]; ok {
		for _, handler := range handlers {
			handler(event)
		}
	}
}


func NewEvent(eventType EventType, peerID int64) Event {
	return Event{
		Type:      eventType,
		PeerID:    peerID,
		Timestamp: time.Now(),
		Data:      make(map[string]interface{}),
	}
}


func SetEventStringData(event Event, key, value string) Event {
	if event.Data == nil {
		event.Data = make(map[string]interface{})
	}
	event.Data[key] = value
	return event
}
