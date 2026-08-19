package gateway

import "context"


type IncomingMessage struct {
	
	ID int64
	
	PeerID int64
	
	FromID int64
	
	Date int64
	
	Text string
	
	Payload string
	
	EventID string
}


type Messenger interface {
	
	SendMessage(peerID int64, text string) (int64, error)

	
	SendThinking(peerID int64, content string) (int64, error)

	
	SendMessageWithKeyboard(peerID int64, text string, keyboard map[string]interface{}) (int64, error)

	
	AnswerCallback(eventID string, userID int64, peerID int64, text string) error
}


type Updater interface {
	
	Start(ctx context.Context, handler HandlerFunc) error
}


type HandlerFunc func(msg IncomingMessage, replyTo int64) string
