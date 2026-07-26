package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/pkg/vk"
)

// VKSendFileTool отправляет файл пользователю через VK
type VKSendFileTool struct {
	vkClient *vk.BotClient
	peerID   int64
}

// NewVKSendFileTool создаёт инструмент отправки файлов в VK
func NewVKSendFileTool(vkClient *vk.BotClient, peerID int64) *VKSendFileTool {
	return &VKSendFileTool{
		vkClient: vkClient,
		peerID:   peerID,
	}
}

func (t *VKSendFileTool) Name() string {
	return "vk_send_file"
}

func (t *VKSendFileTool) Description() string {
	return "Send a file to a VK user. Uploads the file and sends it as a document with an optional message."
}

func (t *VKSendFileTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"file_path": tools.CreateStringParameter("file_path", "The file path to send (absolute or relative)", true),
			"message":   tools.CreateStringParameter("message", "Optional message to send with the file", false),
			"peer_id":   tools.CreateStringParameter("peer_id", "Optional VK peer_id (chat or user ID). Defaults to main peer_id.", false),
		},
		"required": []string{"file_path"},
	}
}

func (t *VKSendFileTool) Execute(ctx context.Context, inputs map[string]string) (tools.ToolResult, error) {
	filePath, ok := inputs["file_path"]
	if !ok || filePath == "" {
		return tools.ToolResult{Success: false, Error: "file_path parameter is required"}, nil
	}

	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(tools.WorkingDir, filePath)
	}
	filePath = filepath.Clean(filePath)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("File not found: %s", filePath)}, nil
	}

	peerID := t.peerID
	if pidStr, ok := inputs["peer_id"]; ok && pidStr != "" {
		if _, err := fmt.Sscanf(pidStr, "%d", &peerID); err != nil || peerID <= 0 {
			return tools.ToolResult{Success: false, Error: "Invalid peer_id"}, nil
		}
	}

	message := ""
	if msg, ok := inputs["message"]; ok {
		message = msg
	}

	msgID, err := t.vkClient.SendFile(filePath, peerID, message)
	if err != nil {
		return tools.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to send file: %v", err),
		}, nil
	}

	return tools.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"file":      filePath,
			"filename":  filepath.Base(filePath),
			"peer_id":   peerID,
			"message":   message,
			"message_id": msgID,
		},
	}, nil
}
