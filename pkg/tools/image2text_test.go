package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
)


func mockChatCompletionServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"role": "assistant", "content": content}},
			},
		})
	}))
	return server
}

func newImage2TextConfig(serverURL string) {
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

func TestImage2TextToolMissingPath(t *testing.T) {
	tool := &Image2TextTool{}
	result, err := tool.Execute(context.Background(), map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for missing path")
	}
}

func TestImage2TextToolFileNotFound(t *testing.T) {
	server := mockChatCompletionServer(t, "cat")
	defer server.Close()
	newImage2TextConfig(server.URL)

	tool := &Image2TextTool{}
	result, err := tool.Execute(context.Background(), map[string]string{
		"path": "/nonexistent/image.jpg",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure for nonexistent file")
	}
}

func TestImage2TextToolRecognizesImage(t *testing.T) {
	var lastBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&lastBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"role": "assistant", "content": "A cat sitting on a sofa"}},
			},
		})
	}))
	defer server.Close()
	newImage2TextConfig(server.URL)

	imgPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(imgPath, []byte("fake-jpeg-bytes"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &Image2TextTool{}
	result, err := tool.Execute(context.Background(), map[string]string{"path": imgPath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got: %s", result.Error)
	}

	data := result.Data.(map[string]interface{})
	content := data["content"].(string)
	if !strings.Contains(content, "cat") {
		t.Errorf("expected recognized text to contain 'cat', got: %q", content)
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
	if !ok || len(contentParts) != 2 {
		t.Fatalf("expected 2 content parts (image + text), got %v", userMsg["content"])
	}
	imgPart, ok := contentParts[0].(map[string]interface{})
	if !ok || imgPart["type"] != "image_url" {
		t.Fatalf("expected first part to be image_url, got %v", contentParts[0])
	}
	imgURL, ok := imgPart["image_url"].(map[string]interface{})
	if !ok {
		t.Fatal("expected image_url object")
	}
	dataURL, ok := imgURL["url"].(string)
	if !ok || !strings.HasPrefix(dataURL, "data:image/jpeg;base64,") {
		t.Errorf("expected data URL with jpeg mime, got %q", dataURL)
	}
	encoded := strings.TrimPrefix(dataURL, "data:image/jpeg;base64,")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode image base64: %v", err)
	}
	if string(decoded) != "fake-jpeg-bytes" {
		t.Errorf("image payload mismatch, got %q", decoded)
	}
}

func TestImage2TextToolServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	newImage2TextConfig(server.URL)

	imgPath := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(imgPath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &Image2TextTool{}
	result, err := tool.Execute(context.Background(), map[string]string{"path": imgPath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure on server error")
	}
}
