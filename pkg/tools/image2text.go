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

// ============================================================
// Image2Text Tool — распознавание изображения мультимодальной моделью
// ============================================================

// Image2TextConfig — конфигурация инструмента image2text.
type Image2TextConfig struct {
	ModelHolder *modelsconfig.Holder
	MaxTokens   int
}

var globalImage2Text Image2TextConfig

// SetImage2TextConfig задаёт глобальную конфигурацию для image2text.
func SetImage2TextConfig(cfg Image2TextConfig) {
	globalImage2Text = cfg
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

	resolvedPath, err := resolvePath(path)
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

	text, err := t.recognize(ctx, resolvedPath, data, prompt)
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

// recognize отправляет изображение в мультимодальную модель и возвращает
// распознанный текст.
func (t *Image2TextTool) recognize(ctx context.Context, imagePath string, data []byte, prompt string) (string, error) {
	model, host := currentImage2TextModel()
	if host == "" {
		host = "http://127.0.0.1:8081"
	}

	imageURL := "data:" + imageMimeType(imagePath) + ";base64," + base64.StdEncoding.EncodeToString(data)

	reqBody := map[string]interface{}{
		"model": model,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "image_url", "image_url": map[string]string{"url": imageURL}},
					{"type": "text", "text": prompt},
				},
			},
		},
		"max_tokens": image2TextMaxTokens(),
		"stream":     false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("image2text: marshal request: %w", err)
	}

	return postChatCompletion(ctx, host, jsonData)
}

// postChatCompletion отправляет запрос на OpenAI-совместимый /v1/chat/completions
// и возвращает content из первого choice.
func postChatCompletion(ctx context.Context, host string, jsonData []byte) (string, error) {
	url := strings.TrimSuffix(host, "/") + "/v1/chat/completions"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("image2text: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("image2text: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("image2text: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("image2text: server error %d: %s", resp.StatusCode, stringutil.Truncate(string(body), 300, "..."))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("image2text: parse response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("image2text: empty choices in response")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

// currentImage2TextModel возвращает имя модели и URL сервера из конфигурации.
func currentImage2TextModel() (model, host string) {
	if globalImage2Text.ModelHolder != nil {
		_, model, host = globalImage2Text.ModelHolder.GetCurrent()
	}
	return model, host
}

// image2TextMaxTokens возвращает лимит токенов ответа (по умолчанию 4096).
// Это лимит OUTPUT (описания), не контекста модели — передавать весь
// контекст сюда нельзя (vLLM отдаст 400).
func image2TextMaxTokens() int {
	if globalImage2Text.MaxTokens > 0 {
		return globalImage2Text.MaxTokens
	}
	return 4096
}

// imageMimeType определяет MIME-тип изображения по расширению файла.
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



