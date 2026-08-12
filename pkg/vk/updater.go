package vk

import (
	"context"
)

// VKUpdater — интерфейс для получения входящих сообщений из VK Long Poll.
type VKUpdater interface {
	// GetLongPollServer возвращает параметры VK Bots Long Poll сервера.
	GetLongPollServer() (server, key string, ts int64, err error)

	// CheckUpdates опрашивает Long Poll на новые сообщения.
	CheckUpdates(ctx context.Context, server, key string, ts int64) ([]VKMessage, int64, error)

	// GetMessagesByID возвращает полные сообщения с аттачами.
	GetMessagesByID(messageIDs []int64) ([]VKMessage, error)

	// SendMessageEventAnswer отвечает на callback-кнопку.
	SendMessageEventAnswer(eventID string, userID int64, peerID int64, text string) error
}
