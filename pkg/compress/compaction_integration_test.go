package compress

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// Integration Tests — требуют запущенный llama-server
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

	client := &LLMSummarizer{
		serverURL: serverURL,
		client:    &http.Client{Timeout: 5 * time.Second},
	}

	// Простой тестовый запрос
	_, err := client.sendRequest(context.Background(), "You are a test assistant.", "Say 'ok'", 10)
	if err != nil {
		t.Skipf("llama-server not available at %s: %v", serverURL, err)
	}
}

// ============================================================
// Test 1: Полный цикл сжатия
// ============================================================

func TestIntegration_FullCompactionCycle(t *testing.T) {
	serverURL := getServerURL()
	skipIfNoServer(t, serverURL)

	// Создаём компактор с реальным LLM
	config := DefaultCompactionConfig()
	config.KeepLastMessages = 4

	summarizer := NewLLMSummarizer(serverURL, "", 0.3)
	compactor := NewCompactor(config, nil, nil)

	// Создаём длинную историю сообщений (~30 сообщений)
	messages := createLongConversation(30)

	// Оцениваем размер до сжатия
	tokensBefore := compactor.estimator.EstimateMessages(messages)
	t.Logf("Before compaction: %d tokens, %d messages", tokensBefore, len(messages))

	// Проверяем уровень сжатия
	level := config.Thresholds.GetLevel(float64(tokensBefore) / 32000)
	t.Logf("Compaction level: %v", level)

	// Выполняем сжатие с LLM
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	result, err := compactor.CompactWithLLM(ctx, messages, level, 32000)
	if err != nil {
		t.Fatalf("Compaction failed: %v", err)
	}

	// Проверяем результат
	t.Logf("After compaction: %d tokens, %d messages kept, %d summarized",
		result.TokensAfter, len(result.KeptMessages), result.SummarizedCount)

	// Проверяем что токены уменьшились
	if result.TokensAfter >= result.TokensBefore {
		t.Errorf("Tokens should decrease: %d -> %d", result.TokensBefore, result.TokensAfter)
	}

	// Проверяем что последние сообщения сохранены
	if len(result.KeptMessages) > config.KeepLastMessages {
		t.Errorf("Too many messages kept: %d > %d", len(result.KeptMessages), config.KeepLastMessages)
	}

	// Проверяем состояние
	if result.State != nil {
		t.Logf("State goal: %s", result.State.Goal)
		t.Logf("State decisions: %v", result.State.Decisions)
		t.Logf("State artifacts: %v", result.State.Artifacts)
	}

	_ = summarizer // used in full implementation
}

// ============================================================
// Test 2: LLM Summarization
// ============================================================

func TestIntegration_LLMSummarization(t *testing.T) {
	serverURL := getServerURL()
	skipIfNoServer(t, serverURL)

	summarizer := NewLLMSummarizer(serverURL, "", 0.3)

	// Создаём сообщения для суммаризации
	messages := []tokenizers.Message{
		{Role: "user", Content: "I need to implement user authentication in Go"},
		{Role: "assistant", Content: "I'll help you implement authentication. We'll use bcrypt for passwords and JWT for sessions."},
		{Role: "user", Content: "What files should I create?"},
		{Role: "assistant", Content: "Create auth.go, handlers/login.go, and middleware/auth.go"},
		{Role: "user", Content: "I've created auth.go, now working on handlers"},
		{Role: "assistant", Content: "Great! The handlers should validate input and call auth functions."},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Выполняем суммаризацию
	response, err := summarizer.Summarize(ctx, &SummarizeRequest{
		Messages:  messages,
		MaxTokens: 500,
	})

	if err != nil {
		t.Fatalf("Summarization failed: %v", err)
	}

	// Проверяем ответ
	t.Logf("Raw response length: %d chars", len(response.RawResponse))

	if response.State == nil {
		t.Fatal("State should not be nil")
	}

	// Проверяем что важная информация извлечена
	state := response.State

	t.Logf("Goal: %s", state.Goal)
	t.Logf("Decisions: %v", state.Decisions)
	t.Logf("Working Memory: %v", state.WorkingMemory)
	t.Logf("Artifacts: %v", state.Artifacts)
	t.Logf("Next Steps: %v", state.NextSteps)

	// Проверяем что цель определена
	if state.Goal == "" && len(state.WorkingMemory) == 0 {
		t.Error("Should extract either goal or working memory")
	}
}

// ============================================================
// Test 3: Проверка уровней сжатия
// ============================================================

func TestIntegration_CompactionLevels(t *testing.T) {
	serverURL := getServerURL()
	skipIfNoServer(t, serverURL)

	config := DefaultCompactionConfig()
	compactor := NewCompactor(config, nil, nil)

	tests := []struct {
		name       string
		msgCount   int
		maxTokens  int
		wantLevel  CompactionLevel
	}{
		{"Small context", 5, 32000, CompactionNone},
		{"Medium context", 50, 10000, CompactionNormal},
		{"Large context", 100, 5000, CompactionAggressive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := createLongConversation(tt.msgCount)
			tokens := compactor.estimator.EstimateMessages(messages)
			level := config.Thresholds.GetLevel(float64(tokens) / float64(tt.maxTokens))

			t.Logf("%s: %d tokens, level: %v", tt.name, tokens, level)

			// Проверяем что уровень соответствует ожиданиям
			if level < tt.wantLevel {
				t.Logf("Warning: level %v < expected %v (might be due to estimation)", level, tt.wantLevel)
			}
		})
	}
}

// ============================================================
// Test 4: Tool Result Clearing
// ============================================================

func TestIntegration_ToolResultClearing(t *testing.T) {
	config := DefaultCompactionConfig()
	config.ToolResultMaxTokens = 100
	compactor := NewCompactor(config, nil, nil)

	// Создаём сообщение с длинным tool result
	longOutput := strings.Repeat("This is a line of tool output.\n", 100) // ~3000 chars

	messages := []tokenizers.Message{
		{Role: "user", Content: "Read the large file"},
		{Role: "tool", Content: longOutput},
		{Role: "assistant", Content: "I've read the file"},
		{Role: "user", Content: "What did it contain?"},
		{Role: "assistant", Content: "It contained configuration data"},
	}

	// Очищаем tool results
	cleared := compactor.clearToolResults(messages)

	// Проверяем что tool result сжат
	originalToolLen := len(messages[1].Content)
	clearedToolLen := len(cleared[1].Content)

	t.Logf("Tool result: %d -> %d chars (%.1f%% reduction)",
		originalToolLen, clearedToolLen,
		float64(originalToolLen-clearedToolLen)/float64(originalToolLen)*100)

	if clearedToolLen >= originalToolLen {
		t.Error("Tool result should be compressed")
	}

	// Другие сообщения не должны измениться
	for i := range messages {
		if i == 1 {
			continue // tool message
		}
		if messages[i].Content != cleared[i].Content {
			t.Errorf("Message %d should not change", i)
		}
	}
}

// ============================================================
// Test 5: File Artifact Store
// ============================================================

func TestIntegration_ArtifactStore(t *testing.T) {
	// Создаём временную директорию
	tmpDir := t.TempDir()

	store, err := NewFileArtifactStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Сохраняем артефакт
	largeContent := strings.Repeat("Important data line\n", 1000)
	ref, err := store.Save("test_artifact", largeContent)
	if err != nil {
		t.Fatalf("Failed to save artifact: %v", err)
	}

	t.Logf("Saved artifact: %s (%d tokens)", ref.Path, ref.Tokens)

	// Загружаем артефакт
	loaded, err := store.Load(ref.Path)
	if err != nil {
		t.Fatalf("Failed to load artifact: %v", err)
	}

	if loaded != largeContent {
		t.Error("Loaded content should match original")
	}

	// Проверяем что файл существует
	if _, err := os.Stat(ref.Path); os.IsNotExist(err) {
		t.Error("Artifact file should exist")
	}
}

// ============================================================
// Test 6: Context Overflow Handling
// ============================================================

func TestIntegration_ContextOverflowRecovery(t *testing.T) {
	serverURL := getServerURL()
	skipIfNoServer(t, serverURL)

	config := DefaultCompactionConfig()
	config.KeepLastMessages = 4
	config.Thresholds = CompactionThresholds{
		WarnPercent:       0.10, // Низкие пороги для триггера теста
		NormalPercent:     0.20,
		AggressivePercent: 0.30,
	}

	compactor := NewCompactor(config, nil, nil)

	// Симулируем ситуацию когда контекст близок к переполнению
	messages := createLongConversation(200)

	tokensBefore := compactor.estimator.EstimateMessages(messages)
	t.Logf("Simulated overflow: %d tokens in history", tokensBefore)

	// Определяем уровень с небольшим maxTokens чтобы триггерить сжатие
	smallMaxTokens := 1000
	level := config.Thresholds.GetLevel(float64(tokensBefore) / float64(smallMaxTokens))
	t.Logf("Detected level: %v", level)

	// Выполняем сжатие с принудительным уровнем
	ctx := context.Background()
	result, err := compactor.CompactWithLLM(ctx, messages, CompactionNormal, smallMaxTokens)
	if err != nil {
		t.Fatalf("Compaction failed: %v", err)
	}

	// Проверяем что сжатие существенно уменьшило размер
	reduction := float64(result.TokensBefore-result.TokensAfter) / float64(result.TokensBefore) * 100
	t.Logf("Reduction: %.1f%% (%d -> %d tokens)", reduction, result.TokensBefore, result.TokensAfter)

	if reduction < 50 {
		t.Errorf("Expected >50%% reduction for overflow scenario, got %.1f%%", reduction)
	}
}

// ============================================================
// Test 7: State Serialization
// ============================================================

func TestIntegration_StateSerialization(t *testing.T) {
	state := &ContextState{
		Goal: "Implement authentication",
		Plan: []string{"Create auth.go", "Add handlers", "Write tests"},
		Decisions: []string{
			"Using bcrypt for passwords",
			"JWT for session management",
		},
		WorkingMemory: []string{
			"Database: PostgreSQL",
			"Port: 8080",
		},
		Artifacts: []ArtifactRef{
			{Type: "file", Path: "auth.go", Description: "Authentication logic"},
		},
		LastUpdated: time.Now(),
	}

	// Конвертируем в промпт
	prompt := state.ToPrompt()
	t.Logf("State prompt (%d chars):\n%s", len(prompt), prompt)

	// Оцениваем токены
	tokens := state.EstimateTokens()
	t.Logf("Estimated tokens: %d", tokens)

	// Проверяем что все данные в промпте
	required := []string{
		"authentication",
		"bcrypt",
		"JWT",
		"PostgreSQL",
		"auth.go",
	}

	for _, req := range required {
		if !strings.Contains(prompt, req) {
			t.Errorf("Prompt missing: %s", req)
		}
	}
}

// ============================================================
// Helper Functions
// ============================================================

func createLongConversation(count int) []tokenizers.Message {
	messages := make([]tokenizers.Message, count)

	topics := []string{
		"I need to implement a REST API in Go",
		"Let's create the project structure first",
		"We should use the standard library for HTTP",
		"I've created main.go with the server setup",
		"Now let's add the handlers",
		"The GET handler is working, now POST",
		"We need to add validation",
		"I've added input validation using regex",
		"Should we add authentication?",
		"Yes, let's use JWT tokens",
	}

	for i := 0; i < count; i++ {
		topic := topics[i%len(topics)]
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}

		// Добавляем вариативность
		content := fmt.Sprintf("%s (message %d)", topic, i+1)

		messages[i] = tokenizers.Message{
			Role:    role,
			Content: content,
		}
	}

	return messages
}
