package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/util/stringutil"
)

type MediaConfig struct {
	ModelHolder *modelsconfig.Holder
	MaxTokens   int
}

var globalMediaConfig MediaConfig

func SetMediaConfig(cfg MediaConfig) {
	globalMediaConfig = cfg
}

type Image2TextTool struct{}

func (t *Image2TextTool) Name() string {
	return "image2text"
}

func (t *Image2TextTool) Description() string {
	return "Load an image file and use the multimodal model to recognize and describe its content. Returns the text recognized in the image (objects, scenes, printed text, etc.)."
}

func (t *Image2TextTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path":   CreateStringParameter("path", "The image file path to recognize (absolute or relative)", true),
			"prompt": CreateStringParameter("prompt", "Optional instruction for what to look for in the image (default: describe the image content in detail)", false),
		},
		"required": []string{"path"},
	}
}

func (t *Image2TextTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	path, ok := inputs["path"]
	if !ok || path == "" {
		return ToolResult{Success: false, Error: "path parameter is required"}, nil
	}

	resolvedPath, err := resolveReadPath(path)
	if err != nil {
		return ToolResult{Success: false, Error: fmt.Sprintf("Invalid path: %v", err)}, nil
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return ToolResult{Success: false, Error: fmt.Sprintf("Failed to read image: %v", err)}, nil
	}

	prompt := inputs["prompt"]
	if prompt == "" {
		prompt = "Describe the image content in detail."
	}

	text, err := recognizeMedia(ctx, resolvedPath, data, imageMimeType(resolvedPath), prompt)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}, nil
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"path":    resolvedPath,
			"content": text,
		},
	}, nil
}

func recognizeMedia(ctx context.Context, filePath string, data []byte, mimeType, prompt string) (string, error) {
	model, host := currentMediaModel()
	if host == "" {
		host = "http://127.0.0.1:8081"
	}

	contentType := "image_url"
	if strings.HasPrefix(mimeType, "video/") {
		contentType = "video_url"
	}

	fileURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)

	mediaPart := map[string]interface{}{
		"type": contentType,
	}
	mediaPart[contentType] = map[string]string{"url": fileURL}

	reqBody := map[string]interface{}{
		"model": model,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					mediaPart,
					{"type": "text", "text": prompt},
				},
			},
		},
		"max_tokens": mediaMaxTokens(),
		"stream":     false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("media recognize: marshal request: %w", err)
	}

	return postChatCompletion(ctx, host, jsonData)
}

func videoMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	mimeMap := map[string]string{
		".mp4":  "video/mp4",
		".avi":  "video/x-msvideo",
		".mov":  "video/quicktime",
		".mkv":  "video/x-matroska",
		".webm": "video/webm",
		".flv":  "video/x-flv",
		".wmv":  "video/x-ms-wmv",
	}
	if mime, ok := mimeMap[ext]; ok {
		return mime
	}
	return "video/mp4"
}

func postChatCompletion(ctx context.Context, host string, jsonData []byte) (string, error) {
	url := strings.TrimSuffix(host, "/") + "/v1/chat/completions"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("media recognize: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("media recognize: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("media recognize: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("media recognize: server error %d: %s", resp.StatusCode, stringutil.Truncate(string(body), 300, "..."))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("media recognize: parse response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("media recognize: empty choices in response")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func currentMediaModel() (model, host string) {
	if globalMediaConfig.ModelHolder != nil {
		_, model, host = globalMediaConfig.ModelHolder.GetCurrent()
	}
	return model, host
}

func mediaMaxTokens() int {
	if globalMediaConfig.MaxTokens > 0 {
		return globalMediaConfig.MaxTokens
	}
	return 4096
}

func imageMimeType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	default:
		return "application/octet-stream"
	}
}