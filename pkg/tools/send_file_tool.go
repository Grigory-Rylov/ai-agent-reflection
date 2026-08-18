package tools

import (
	"context"
	"fmt"
	"strconv"
	"sync"
)

// ============================================================
// send-files — отправляет файл пользователю через VK (документ)
// ============================================================

// FileSender отправляет файл peer'у и возвращает ID сообщения.
// Реализация — vk.BotClient.UploadAndSendDocument.
type FileSender interface {
	UploadAndSendDocument(filePath string, peerID int64, message string) (int64, error)
}

var (
	sendFileMu    sync.RWMutex
	sendFileDep   FileSender
	sendFilePeer  int64
)

// SetSendFileDependencies устанавливает зависимости для send-files инструмента.
// sender — реализация отправки (vk.BotClient), defaultPeerID — peer по умолчанию.
func SetSendFileDependencies(sender FileSender, defaultPeerID int64) {
	sendFileMu.Lock()
	defer sendFileMu.Unlock()
	sendFileDep = sender
	sendFilePeer = defaultPeerID
}

func getSendFileDep() (FileSender, int64) {
	sendFileMu.RLock()
	defer sendFileMu.RUnlock()
	return sendFileDep, sendFilePeer
}

// SendFileTool отправляет файл пользователю через VK.
type SendFileTool struct{}

func (t *SendFileTool) Name() string {
	return "send-files"
}

func (t *SendFileTool) Description() string {
	return "Отправляет файл пользователю в VK как документ. " +
		"Принимает абсолютный путь к файлу на диске и необязательный текст-подпись. " +
		"Файлы с расширениями, которые VK блокирует (html, svg, js и др.), " +
		"отправляются автоматически как .txt."
}

func (t *SendFileTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path": CreateStringParameter("file_path", "Абсолютный путь к файлу на диске", true),
			"caption":   CreateStringParameter("caption", "Необязательный текст, который придёт вместе с файлом", false),
		},
		"required": []string{"file_path"},
	}
}

func (t *SendFileTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	filePath, _ := inputs["file_path"]
	if filePath == "" {
		return ToolResult{Success: false, Error: "file_path required"}, nil
	}
	caption, _ := inputs["caption"]

	sender, defaultPeer := getSendFileDep()
	if sender == nil {
		return ToolResult{Success: false, Error: "send-files not configured"}, nil
	}

	peerID := defaultPeer
	if p, ok := inputs["peer_id"]; ok && p != "" {
		if v, err := strconv.ParseInt(p, 10, 64); err == nil {
			peerID = v
		}
	}
	if peerID <= 0 {
		return ToolResult{Success: false, Error: "peer_id not set"}, nil
	}

	msgID, err := sender.UploadAndSendDocument(filePath, peerID, caption)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}, nil
	}

	return ToolResult{
		Success: true,
		Data:    fmt.Sprintf("File sent! Message ID: %d\nFile: %s", msgID, filePath),
	}, nil
}
