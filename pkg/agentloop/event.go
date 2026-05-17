package agentloop

import (
	"time"
)

// ============================================================
// Система событий AgentLoop
// ============================================================

// EventType определяет тип события в цикле
type EventType string

const (
	// EventPromptReceived — получен промпт от пользователя
	EventPromptReceived EventType = "prompt_received"

	// EventRequestSent — запрос отправлен в LLM
	EventRequestSent EventType = "request_sent"

	// EventResponseChunk — получен чанк ответа
	EventResponseChunk EventType = "response_chunk"

	// EventResponseDone — ответ завершён
	EventResponseDone EventType = "response_done"

	// EventToolCall — обнаружен вызов инструмента
	EventToolCall EventType = "tool_call"

	// EventToolResult — получен результат выполнения инструмента
	EventToolResult EventType = "tool_result"

	// EventLoopDetected — обнаружено зацикливание AI
	EventLoopDetected EventType = "loop_detected"

	// EventThinking — отправлено thinking сообщение
	EventThinking EventType = "thinking"

	// EventError — произошла ошибка
	EventError EventType = "error"
)

// Event представляет событие в цикле обработки
type Event struct {
	Type      EventType
	PeerID    int64
	Timestamp time.Time
	Data      map[string]interface{}
}

// EventHandler — функция-обработчик событий
type EventHandler func(event Event)

// EventDispatcher — диспетчер событий
type EventDispatcher struct {
	handlers map[EventType][]EventHandler
}

// NewEventDispatcher создаёт новый диспетчер событий
func NewEventDispatcher() *EventDispatcher {
	return &EventDispatcher{
		handlers: make(map[EventType][]EventHandler),
	}
}

// Register регистрирует обработчик для определённого типа событий
func (d *EventDispatcher) Register(eventType EventType, handler EventHandler) {
	d.handlers[eventType] = append(d.handlers[eventType], handler)
}

// Emit отправляет событие всем зарегистрированным обработчикам
func (d *EventDispatcher) Emit(event Event) {
	if handlers, ok := d.handlers[event.Type]; ok {
		for _, handler := range handlers {
			handler(event)
		}
	}
}

// ============================================================
// Утилиты для событий
// ============================================================

// NewEvent создаёт новое событие с заполненным временем
func NewEvent(eventType EventType, peerID int64) Event {
	return Event{
		Type:      eventType,
		PeerID:    peerID,
		Timestamp: time.Now(),
		Data:      make(map[string]interface{}),
	}
}

// SetEventStringData добавляет строковые данные в событие
func SetEventStringData(event Event, key, value string) Event {
	if event.Data == nil {
		event.Data = make(map[string]interface{})
	}
	event.Data[key] = value
	return event
}
