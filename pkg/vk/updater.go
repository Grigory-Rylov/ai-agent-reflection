package vk

import (
	"context"
)


type VKUpdater interface {
	
	GetLongPollServer() (server, key string, ts int64, err error)

	
	CheckUpdates(ctx context.Context, server, key string, ts int64) ([]VKMessage, int64, error)

	
	GetMessagesByID(messageIDs []int64) ([]VKMessage, error)

	
	SendMessageEventAnswer(eventID string, userID int64, peerID int64, text string) error
}
