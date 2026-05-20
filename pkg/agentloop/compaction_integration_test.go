package agentloop

import (
	"context"
	"os"
	"testing"

	"github.com/opencode/llama-client/pkg/compress"
	"github.com/opencode/llama-client/pkg/tokenizers"
	"github.com/opencode/llama-client/session"
)

// ============================================================
// Integration Tests for AgentLoop with New Compactor
// ============================================================

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

// TestIntegration_AgentLoopCompaction тестирует сжатие в agentloop
func TestIntegration_AgentLoopCompaction(t *testing.T) {
	serverURL := getServerURL()
	skipIfNoServer(t, serverURL)

	// Создаём конфигурацию с низкими порогами для триггера сжатия
	compactionConfig := compress.DefaultCompactionConfig()
	compactionConfig.Thresholds = compress.CompactionThresholds{
		WarnPercent:       0.10,
		NormalPercent:     0.20,
		AggressivePercent: 0.30,
	}
	compactionConfig.KeepLastMessages = 4

	config := LoopConfig{
		LlamaServerURL:   serverURL,
		Model:            "",
		MaxTokens:        32000,
		Temperature:      0.7,
		EnableCompression: true,
		CompactionConfig: compactionConfig,
		SessionConfig: session.Config{
			SessionFile: "./test_session.json",
			AutoSave:    false,
		},
		EnableLogging: true,
	}

	// Создаём agentloop
	loop, err := NewAgentLoop(config, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create agentloop: %v", err)
	}

	// Проверяем что компактор инициализирован
	al := loop.(*agentLoop)
	if al.compactor == nil {
		t.Fatal("Compactor should be initialized")
	}

	t.Log("AgentLoop created with compactor")
}

// TestIntegration_ContextStateManagement тестирует управление состоянием контекста
func TestIntegration_ContextStateManagement(t *testing.T) {
	config := DefaultLoopConfig()
	config.EnableCompression = true

	loop, err := NewAgentLoop(config, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create agentloop: %v", err)
	}

	al := loop.(*agentLoop)

	// Создаём состояние
	state := &compress.ContextState{
		Goal:        "Test goal",
		Decisions:   []string{"Decision 1", "Decision 2"},
		WorkingMemory: []string{"Fact 1"},
	}

	peerID := int64(12345)

	// Сохраняем состояние
	al.saveContextState(peerID, state)

	// Получаем состояние
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

// TestIntegration_HistoryConversion тестирует конвертацию истории
func TestIntegration_HistoryConversion(t *testing.T) {
	config := DefaultLoopConfig()
	loop, _ := NewAgentLoop(config, nil, nil)
	al := loop.(*agentLoop)

	// Создаём тестовую историю
	history := []session.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
		{Role: "user", Content: "How are you?"},
		{Role: "assistant", Content: "I'm doing well, thanks!"},
	}

	// Конвертируем
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

// TestIntegration_CheckAndCompactNew тестирует новый метод сжатия
func TestIntegration_CheckAndCompactNew(t *testing.T) {
	serverURL := getServerURL()
	skipIfNoServer(t, serverURL)

	// Создаём конфигурацию с агрессивными порогами
	compactionConfig := compress.DefaultCompactionConfig()
	compactionConfig.Thresholds = compress.CompactionThresholds{
		WarnPercent:       0.01, // Очень низкие пороги
		NormalPercent:     0.02,
		AggressivePercent: 0.05,
	}
	compactionConfig.KeepLastMessages = 4

	config := LoopConfig{
		LlamaServerURL:   serverURL,
		MaxTokens:        1000, // Небольшой для триггера сжатия
		EnableCompression: true,
		CompactionConfig: compactionConfig,
		EnableLogging:    true,
	}

	loop, err := NewAgentLoop(config, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create agentloop: %v", err)
	}

	al := loop.(*agentLoop)

	// Создаём сессию с длинной историей
	sess := session.NewSession(session.DefaultConfig())
	for i := 0; i < 20; i++ {
		sess.AddUserMessage("This is a test message with some content to make it longer")
		sess.AddAssistantMessage("This is a response to the test message with some details")
	}

	// Проверяем сжатие
	ctx := context.Background()
	al.checkAndCompactNew(ctx, sess, 1)

	// Проверяем что состояние сохранено
	state := al.getContextState(1)
	if state != nil {
		t.Logf("State saved: goal=%s, decisions=%d", state.Goal, len(state.Decisions))
	}
}

// TestIntegration_FullPromptCycle тестирует полный цикл обработки промпта
func TestIntegration_FullPromptCycle(t *testing.T) {
	serverURL := getServerURL()
	skipIfNoServer(t, serverURL)

	config := DefaultLoopConfig()
	config.LlamaServerURL = serverURL
	config.MaxTokens = 1000
	config.EnableCompression = true
	config.EnableLogging = true

	loop, err := NewAgentLoop(config, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create agentloop: %v", err)
	}

	// Создаём сессию
	peerID := int64(1)
	sess := loop.EnsureSession(peerID)
	if sess == nil {
		t.Fatal("Session should be created")
	}

	t.Logf("Session created for peer %d", peerID)

	// Проверяем статистику контекста
	charCount, tokenCount, err := loop.GetContextStats(peerID)
	if err != nil {
		t.Logf("Context stats: chars=%d, tokens=%d", charCount, tokenCount)
	}
}

// TestIntegration_CompactionWithLargeHistory тестирует сжатие большой истории
func TestIntegration_CompactionWithLargeHistory(t *testing.T) {
	serverURL := getServerURL()
	skipIfNoServer(t, serverURL)

	// Создаём конфигурацию
	compactionConfig := compress.DefaultCompactionConfig()
	compactionConfig.KeepLastMessages = 6
	compactionConfig.MaxWorkingMemory = 5

	config := LoopConfig{
		LlamaServerURL:   serverURL,
		MaxTokens:        500, // Маленький для триггера сжатия
		EnableCompression: true,
		CompactionConfig: compactionConfig,
		EnableLogging:    true,
	}

	loop, err := NewAgentLoop(config, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create agentloop: %v", err)
	}

	al := loop.(*agentLoop)

	// Создаём очень длинную историю
	sess := session.NewSession(session.DefaultConfig())
	for i := 0; i < 100; i++ {
		sess.AddUserMessage("User message number %d with some additional content to increase token count")
		sess.AddAssistantMessage("Assistant response number %d with detailed explanation and analysis")
	}

	// Получаем историю
	history := sess.GetHistory()
	t.Logf("Created history with %d messages", len(history))

	// Конвертируем и проверяем размер
	messages := al.convertHistoryToMessages(history)
	tokens := compress.EstimateMessagesTokensSimple(messages)
	t.Logf("Estimated tokens: %d", tokens)

	// Выполняем сжатие
	ctx := context.Background()
	al.checkAndCompactNew(ctx, sess, 1)

	// Проверяем результат
	state := al.getContextState(1)
	if state != nil {
		t.Logf("State after compaction:")
		t.Logf("  Goal: %s", state.Goal)
		t.Logf("  Decisions: %d", len(state.Decisions))
		t.Logf("  Working Memory: %d", len(state.WorkingMemory))
		t.Logf("  Artifacts: %d", len(state.Artifacts))
	}
}

// TestIntegration_ArtifactStore тестирует хранилище артефактов
func TestIntegration_ArtifactStore(t *testing.T) {
	tmpDir := t.TempDir()

	config := DefaultLoopConfig()
	config.ArtifactStorePath = tmpDir
	config.EnableCompression = true

	loop, err := NewAgentLoop(config, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create agentloop: %v", err)
	}

	al := loop.(*agentLoop)

	if al.artifactStore == nil {
		t.Fatal("Artifact store should be initialized")
	}

	// Сохраняем артефакт
	content := "This is a large tool result that should be stored externally"
	ref, err := al.artifactStore.Save("test_result", content)
	if err != nil {
		t.Fatalf("Failed to save artifact: %v", err)
	}

	t.Logf("Artifact saved: %s (%d tokens)", ref.Path, ref.Tokens)

	// Загружаем артефакт
	loaded, err := al.artifactStore.Load(ref.Path)
	if err != nil {
		t.Fatalf("Failed to load artifact: %v", err)
	}

	if loaded != content {
		t.Error("Loaded content should match original")
	}
}
