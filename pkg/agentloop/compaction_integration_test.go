//go:build integration

package agentloop

import (
	"os"
	"testing"

	"github.com/opencode/llama-client/pkg/modelsconfig"
	"github.com/opencode/llama-client/pkg/tokenizers"
	"github.com/opencode/llama-client/session"
)

func getServerURL() string {
	url := os.Getenv("LLAMA_SERVER_URL")
	if url == "" {
		return "http://localhost:8081"
	}
	return url
}

func skipIfNoServer(t *testing.T, serverURL string) {
	t.Helper()

	tokenizer := tokenizers.NewLlamaServerTokenizer(serverURL, "", 1000)

	_, err := tokenizer.CountTokens("test")
	if err != nil {
		t.Skipf("llama-server not available at %s: %v", serverURL, err)
	}
}

func testModelHolder(serverURL string) *modelsconfig.Holder {
	return modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: serverURL},
		},
	})
}

func TestIntegration_HistoryConversion(t *testing.T) {
	config := DefaultLoopConfig()
	config.ModelHolder = testModelHolder("http://localhost:8081")
	loop, _ := NewAgentLoop(config, nil, nil)
	al := loop.(*agentLoop)

	history := []session.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
		{Role: "user", Content: "How are you?"},
		{Role: "assistant", Content: "I'm doing well, thanks!"},
	}

	messages := al.convertHistoryToMessages(history)

	if len(messages) != len(history) {
		t.Fatalf("Message count mismatch: %d != %d", len(messages), len(history))
	}

	for i, msg := range messages {
		if msg.Role != string(history[i].Role) {
			t.Errorf("Role mismatch at %d", i)
		}
		if msg.Content != history[i].Content {
			t.Errorf("Content mismatch at %d", i)
		}
	}

	t.Logf("Converted %d messages successfully", len(messages))
}

func TestIntegration_FullPromptCycle(t *testing.T) {
	serverURL := getServerURL()
	skipIfNoServer(t, serverURL)

	config := DefaultLoopConfig()
	config.ModelHolder = testModelHolder(serverURL)
	config.MaxTokens = 1000
	config.EnableCompression = true
	config.EnableLogging = true

	loop, err := NewAgentLoop(config, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create agentloop: %v", err)
	}

	peerID := int64(1)
	sess := loop.EnsureSession(peerID)
	if sess == nil {
		t.Fatal("Session should be created")
	}

	t.Logf("Session created for peer %d", peerID)

	charCount, tokenCount, err := loop.GetContextStats(peerID)
	if err != nil {
		t.Logf("Context stats: chars=%d, tokens=%d", charCount, tokenCount)
	}
}
