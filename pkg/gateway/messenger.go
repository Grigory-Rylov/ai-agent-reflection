package gateway

import "context"

// IncomingMessage — входящее сообщение от платформы, независимо от реализации.
type IncomingMessage struct {
	// ID уникального сообщения (для получения аттачей)
	ID int64
	// PeerID чата / пользователя
	PeerID int64
	// FromID отправителя
	FromID int64
	// Дата создания
	Date int64
	// Текст сообщения
	Text string
	// Payload callback-данные (inline клавиатура)
	Payload string
	// EventID для answer на callback
	EventID string
}

// Messenger — интерфейс для отправки сообщений платформе.
type Messenger interface {
	// SendMessage отправляет текстовое сообщение.
	SendMessage(peerID int64, text string) (int64, error)

	// SendThinking отправляет thinking-индикатор.
	SendThinking(peerID int64, content string) (int64, error)

	// SendMessageWithKeyboard отправляет сообщение с клавиатурой.
	SendMessageWithKeyboard(peerID int64, text string, keyboard map[string]interface{}) (int64, error)

	// AnswerCallback отвечает на callback-кнопку.
	AnswerCallback(eventID string, userID int64, peerID int64, text string) error
}

// Updater — интерфейс для получения входящих сообщений из платформы.
type Updater interface {
	// Start запускает цикл polling/webhook и вызывает handler для каждого сообщения.
	Start(ctx context.Context, handler HandlerFunc) error
}

// HandlerFunc — функция-обработчик входящего сообщения.
type HandlerFunc func(msg IncomingMessage, replyTo int64) string
