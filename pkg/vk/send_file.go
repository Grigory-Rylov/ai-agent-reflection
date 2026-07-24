package vk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// SendFile отправляет файл пользователю через VK (упрощённая версия)
func (c *BotClient) SendFile(filePath string, peerID int64, message string) (int64, error) {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return 0, fmt.Errorf("file not found: %s", filePath)
	}

	fname := filepath.Base(filePath)
	msgText := ""
	if message != "" {
		msgText = message + "\n"
	}
	msgText += fmt.Sprintf("📎 %s (%s)", fname, filePath)

	return c.sendSingleMessage(peerID, msgText, "", nil)
}

// UploadAndSendDocument загружает документ и отправляет его через VK API
func (c *BotClient) UploadAndSendDocument(filePath string, peerID int64, message string) (int64, error) {
	params := map[string]interface{}{
		"type":         "doc",
		"peer_id":      peerID,
		"v":            c.apiVersion,
		"access_token": c.token,
	}

	resp, err := c.doRequestPOST("docs.getUploadServer", params)
	if err != nil {
		return 0, fmt.Errorf("docs.getUploadServer: %w", err)
	}

	var uploadResp struct {
		Response map[string]interface{} `json:"response"`
	}
	if err := json.Unmarshal(resp, &uploadResp); err != nil {
		return 0, fmt.Errorf("parse upload response: %w", err)
	}

	uploadURL, ok := uploadResp.Response["upload_url"].(string)
	if !ok {
		return 0, fmt.Errorf("no upload_url in response")
	}

	fileData, err := os.ReadFile(filePath)
	if err != nil {
		return 0, fmt.Errorf("read file: %w", err)
	}

	resp2, err := http.PostForm(uploadURL, map[string][]string{
		"file": []string{string(fileData)},
	})
	if err != nil {
		return 0, fmt.Errorf("upload to VK: %w", err)
	}
	defer resp2.Body.Close()

	saveResp, err := c.doRequestPOST("docs.save", map[string]interface{}{
		"file_name": filepath.Base(filePath),
		"v":         c.apiVersion,
		"access_token": c.token,
	})
	if err != nil {
		return 0, fmt.Errorf("docs.save: %w", err)
	}

	var saveResult struct {
		Response []struct {
			ID int64 `json:"id"`
		} `json:"response"`
	}
	if err := json.Unmarshal(saveResp, &saveResult); err != nil {
		return 0, fmt.Errorf("parse save response: %w", err)
	}
	if len(saveResult.Response) == 0 {
		return 0, fmt.Errorf("empty save response")
	}

	attachment := fmt.Sprintf("doc0:%d", saveResult.Response[0].ID)

	params2 := map[string]interface{}{
		"peer_id":      peerID,
		"random_id":    time.Now().UnixMilli(),
		"v":            c.apiVersion,
		"access_token": c.token,
		"attachment":   attachment,
	}

	if message != "" {
		params2["message"] = message
	}

	msgResp, err := c.doRequestPOST("messages.send", params2)
	if err != nil {
		return 0, fmt.Errorf("messages.send: %w", err)
	}

	var finalResp struct {
		Response interface{} `json:"response"`
	}
	if err := json.Unmarshal(msgResp, &finalResp); err != nil {
		return 0, fmt.Errorf("parse send response: %w", err)
	}

	return extractMessageID(finalResp.Response)
}