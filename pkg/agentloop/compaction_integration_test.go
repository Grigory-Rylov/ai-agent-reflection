//go:build integration

package agentloop

import (
	"context"
	"os"
	"testing"

	"github.com/opencode/llama-client/pkg/compress"
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

func TestIntegration_AgentLoopCompaction(t *testing.T) {
	serverURL := getServerURL()
	skipIfNoServer(t, serverURL)

	compactionConfig := compress.DefaultCompactionConfig()
	compactionConfig.Thresholds = compress.CompactionThresholds{
		WarnPercent:       0.10,
		NormalPercent:     0.20,
		AggressivePercent: 0.30,
	}
	compactionConfig.KeepLastMessages = 4

	config := LoopConfig{
		ModelHolder:       testModelHolder(serverURL),
		MaxTokens:         32000,
		Temperature:       0.7,
		EnableCompression: true,
		CompactionConfig:  compactionConfig,
		SessionConfig: session.Config{
			SessionFile: "./test_session.json",
			AutoSave:    false,
		},
		EnableLogging: true,
	}

	loop, err := NewAgentLoop(config, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create agentloop: %v", err)
	}

	al := loop.(*agentLoop)
	if al.compactor == nil {
		t.Fatal("Compactor should be initialized")
	}

	t.Log("AgentLoop created with compactor")
}

func TestIntegration_ContextStateManagement(t *testing.T) {
	config := DefaultLoopConfig()
	config.EnableCompression = true
	config.ModelHolder = testModelHolder("http://localhost:8081")

	loop, err := NewAgentLoop(config, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create agentloop: %v", err)
	}

	al := loop.(*agentLoop)

	state := &compress.ContextState{
		Goal:          "Test goal",
		Decisions:     []string{"Decision 1", "Decision 2"},
		WorkingMemory: []string{"Fact 1"},
	}

	peerID := int64(12345)

	al.saveContextState(peerID, state)

	loaded := al.getContextState(peerID)
	if loaded == nil {
		t.Fatal("State should be saved")
	}

	if loaded.Goal != state.Goal {
		t.Errorf("Goal mismatch: %s != %s", loaded.Goal, state.Goal)
	}

	if len(loaded.Decisions) != len(state.Decisions) {
		t.Errorf("Decisions count mismatch")
	}

	t.Logf("State saved and loaded: goal=%s, decisions=%d", loaded.Goal, len(loaded.Decisions))
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

func TestIntegration_CheckAndCompactNew(t *testing.T) {
	serverURL := getServerURL()
	skipIfNoServer(t, serverURL)

	compactionConfig := compress.DefaultCompactionConfig()
	compactionConfig.Thresholds = compress.CompactionThresholds{
		WarnPercent:       0.01,
		NormalPercent:     0.02,
		AggressivePercent: 0.05,
	}
	compactionConfig.KeepLastMessages = 4

	config := LoopConfig{
		ModelHolder:       testModelHolder(serverURL),
		MaxTokens:         1000,
		EnableCompression: true,
		CompactionConfig:  compactionConfig,
		EnableLogging:     true,
	}

	loop, err := NewAgentLoop(config, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create agentloop: %v", err)
	}

	al := loop.(*agentLoop)

	sess := session.NewSession(session.DefaultConfig())
	for i := 0; i < 20; i++ {
		sess.AddUserMessage("This is a test message with some content to make it longer")
		sess.AddAssistantMessage("This is a response to the test message with some details")
	}

	ctx := context.Background()
	al.checkAndCompactNew(ctx, sess, 1)

	state := al.getContextState(1)
	if state != nil {
		t.Logf("State saved: goal=%s, decisions=%d", state.Goal, len(state.Decisions))
	}
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

func TestIntegration_CompactionWithLargeHistory(t *testing.T) {
	serverURL := getServerURL()
	skipIfNoServer(t, serverURL)

	compactionConfig := compress.DefaultCompactionConfig()
	compactionConfig.KeepLastMessages = 6
	compactionConfig.MaxWorkingMemory = 5

	config := LoopConfig{
		ModelHolder:       testModelHolder(serverURL),
		MaxTokens:         500,
		EnableCompression: true,
		CompactionConfig:  compactionConfig,
		EnableLogging:     true,
	}

	loop, err := NewAgentLoop(config, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create agentloop: %v", err)
	}

	al := loop.(*agentLoop)

	sess := session.NewSession(session.DefaultConfig())
	for i := 0; i < 100; i++ {
		sess.AddUserMessage("User message number %d with some additional content to increase token count")

		history := al.convertHistoryToMessages(sess.GetHistory())
		tokens := compress.EstimateMessagesTokensSimple(history)
		if tokens > config.MaxTokens {
			t.Logf("History exceeded max tokens at iteration %d: ~%d tokens", i, tokens)
			break
		}
	}

	history := al.convertHistoryToMessages(sess.GetHistory())
	initialTokens := compress.EstimateMessagesTokensSimple(history)
	t.Logf("Initial history: %d messages, ~%d tokens", len(history), initialTokens)

	ctx := context.Background()
	al.checkAndCompactNew(ctx, sess, 1)

	afterHistory := al.convertHistoryToMessages(sess.GetHistory())
	finalTokens := compress.EstimateMessagesTokensSimple(afterHistory)
	t.Logf("After compaction: %d messages, ~%d tokens", len(afterHistory), finalTokens)
}
