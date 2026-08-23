package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
)

func newVideo2TextConfig(serverURL string) {
	SetMediaConfig(MediaConfig{
		ModelHolder: modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
			Default: "test",
			Models: map[string]modelsconfig.ModelEntry{
				"test": {Name: "test-model", Host: serverURL},
			},
		}),
		MaxTokens: 512,
	})
}

func TestVideo2TextToolMissingPath(t *testing.T) {
	tool := &Video2TextTool{}
	result, err := tool.Execute(context.Background(), map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for missing path")
	}
}

func TestVideo2TextToolEmptyPath(t *testing.T) {
	tool := &Video2TextTool{}
	result, err := tool.Execute(context.Background(), map[string]string{"path": ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for empty path")
	}
}

func TestVideo2TextToolFileNotFound(t *testing.T) {
	server := mockChatCompletionServer(t, "test scene")
	defer server.Close()
	newVideo2TextConfig(server.URL)

	tool := &Video2TextTool{}
	result, err := tool.Execute(context.Background(), map[string]string{
		"path": "/nonexistent/video.mp4",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for nonexistent file")
	}
}

func TestVideo2TextToolRecognizesVideo(t *testing.T) {
	var lastBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&lastBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"role": "assistant", "content": "A person walking through a park with trees and a blue sky"}},
			},
		})
	}))
	defer server.Close()
	newVideo2TextConfig(server.URL)

	videoPath := createDummyVideo(t)

	tool := &Video2TextTool{}
	result, err := tool.Execute(context.Background(), map[string]string{
		"path":   videoPath,
		"prompt": "What is happening?",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got: %s", result.Error)
	}

	data := result.Data.(map[string]interface{})
	content := data["content"].(string)
	if !strings.Contains(content, "person") {
		t.Errorf("expected content to contain 'person', got: %q", content)
	}

	if lastBody == nil {
		t.Fatal("request body was not captured")
	}
	messages, ok := lastBody["messages"].([]interface{})
	if !ok || len(messages) != 1 {
		t.Fatalf("expected 1 message, got %v", lastBody["messages"])
	}
	userMsg, ok := messages[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected user message object")
	}
	contentParts, ok := userMsg["content"].([]interface{})
	if !ok {
		t.Fatalf("expected content array, got %T", userMsg["content"])
	}
	if len(contentParts) != 2 {
		t.Fatalf("expected 2 content parts (video + text), got %d", len(contentParts))
	}

	firstPart, ok := contentParts[0].(map[string]interface{})
	if !ok || firstPart["type"] != "video_url" {
		t.Fatalf("expected first part to be video_url, got %v", contentParts[0])
	}
	vidURL, ok := firstPart["video_url"].(map[string]interface{})
	if !ok {
		t.Fatal("expected video_url object")
	}
	urlStr, ok := vidURL["url"].(string)
	if !ok || !strings.HasPrefix(urlStr, "data:video/mp4;base64,") {
		t.Errorf("expected video/mp4 data URL, got prefix %q", urlStr[:min(len(urlStr), 40)])
	}

	lastPart, ok := contentParts[1].(map[string]interface{})
	if !ok || lastPart["type"] != "text" {
		t.Fatalf("expected last part to be text, got %v", contentParts[1])
	}
	if lastPart["text"] != "What is happening?" {
		t.Errorf("expected prompt 'What is happening?', got %q", lastPart["text"])
	}
}

func TestVideo2TextToolServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer server.Close()
	newVideo2TextConfig(server.URL)

	videoPath := createDummyVideo(t)

	tool := &Video2TextTool{}
	result, err := tool.Execute(context.Background(), map[string]string{
		"path": videoPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure on server error")
	}
}

func TestVideo2TextDifferentFormats(t *testing.T) {
	formats := map[string]string{
		".mp4":  "video/mp4",
		".avi":  "video/x-msvideo",
		".mov":  "video/quicktime",
		".mkv":  "video/x-matroska",
		".webm": "video/webm",
		".wmv":  "video/x-ms-wmv",
	}

	for ext, expectedMime := range formats {
		t.Run(ext, func(t *testing.T) {
			var lastBody map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewDecoder(r.Body).Decode(&lastBody)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"choices": []map[string]interface{}{
						{"message": map[string]interface{}{"role": "assistant", "content": "ok"}},
					},
				})
			}))
			defer server.Close()
			newVideo2TextConfig(server.URL)

			tmp := filepath.Join(t.TempDir(), "test"+ext)
			if err := os.WriteFile(tmp, []byte{0x00, 0x01, 0x02}, 0644); err != nil {
				t.Fatalf("failed to create file: %v", err)
			}

			tool := &Video2TextTool{}
			result, err := tool.Execute(context.Background(), map[string]string{"path": tmp})
			if err != nil || !result.Success {
				t.Fatalf("expected success, err=%v, result=%v", err, result)
			}

			contentParts := lastBody["messages"].([]interface{})[0].(map[string]interface{})["content"].([]interface{})
			firstPart := contentParts[0].(map[string]interface{})
			vidURL := firstPart["video_url"].(map[string]interface{})
			urlStr := vidURL["url"].(string)
			expectedPrefix := "data:" + expectedMime + ";base64,"
			if !strings.HasPrefix(urlStr, expectedPrefix) {
				t.Errorf("expected prefix %q, got %q", expectedPrefix, urlStr[:min(len(urlStr), 50)])
			}
		})
	}
}

func createDummyVideo(t *testing.T) string {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "test.mp4")
	dummy := []byte{0x00, 0x00, 0x00, 0x1C, 0x66, 0x74, 0x79, 0x70, 0x69, 0x73, 0x6F, 0x6D}
	if err := os.WriteFile(tmp, dummy, 0644); err != nil {
		t.Fatalf("failed to create dummy video: %v", err)
	}
	return tmp
}
