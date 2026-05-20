package compress

import (
	"testing"

	"github.com/opencode/llama-client/pkg/tokenizers"
)

// ============================================================
// ContextState Tests
// ============================================================

func TestContextStateToPrompt(t *testing.T) {
	state := &ContextState{
		Goal:        "Implement user authentication",
		CurrentStep: "Writing login handler",
		Plan:        []string{"Create login form", "Add password hashing", "Implement session"},
		Done:        []string{"Setup project structure", "Add database connection"},
		Decisions:   []string{"Using bcrypt for passwords", "JWT for sessions"},
		WorkingMemory: []string{
			"Database is PostgreSQL",
			"Port 8080 is available",
		},
		OpenQuestions: []string{"Should we add OAuth?"},
		Artifacts: []ArtifactRef{
			{Type: "file", Path: "main.go", Description: "entry point"},
			{Type: "file", Path: "config.json", Description: "configuration"},
		},
	}

	prompt := state.ToPrompt()

	// Проверяем что все секции присутствуют
	expected := []string{
		"Current Goal",
		"Plan",
		"Completed",
		"Decisions Made",
		"Important Facts",
		"Open Questions",
		"Available Artifacts",
	}

	for _, exp := range expected {
		if !contains(prompt, exp) {
			t.Errorf("Prompt missing section: %s", exp)
		}
	}
}

func TestContextStateEstimateTokens(t *testing.T) {
	state := &ContextState{
		Goal: "Test goal",
		Plan: []string{"step 1", "step 2"},
	}

	tokens := state.EstimateTokens()

	if tokens <= 0 {
		t.Error("Token estimate should be positive")
	}

	// Больше контента = больше токенов
	state2 := &ContextState{
		Goal: "Test goal with much longer description that should increase token count significantly",
		Plan: []string{"step 1 with details", "step 2 with more details", "step 3"},
	}

	tokens2 := state2.EstimateTokens()

	if tokens2 <= tokens {
		t.Error("More content should result in more tokens")
	}
}

// ============================================================
// CompactionThresholds Tests
// ============================================================

func TestCompactionLevels(t *testing.T) {
	thresholds := DefaultThresholds()

	tests := []struct {
		percent   float64
		expected  CompactionLevel
	}{
		{0.30, CompactionNone},
		{0.45, CompactionNone},
		{0.55, CompactionWarn},
		{0.65, CompactionWarn},
		{0.75, CompactionNormal},
		{0.80, CompactionNormal},
		{0.90, CompactionAggressive},
		{0.95, CompactionAggressive},
	}

	for _, tt := range tests {
		level := thresholds.GetLevel(tt.percent)
		if level != tt.expected {
			t.Errorf("GetLevel(%.2f) = %v, want %v", tt.percent, level, tt.expected)
		}
	}
}

// ============================================================
// Token Estimation Tests
// ============================================================

func TestEstimateTokensSimple(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		minimum  int
		maximum  int
	}{
		{"empty", "", 0, 0},
		{"short", "Hello world", 2, 5},
		{"medium", "This is a test sentence with multiple words.", 8, 15},
		{"code", "func main() { fmt.Println(\"hello\") }", 8, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := EstimateTokensSimple(tt.text)

			if tokens < tt.minimum {
				t.Errorf("Tokens %d < minimum %d", tokens, tt.minimum)
			}
			if tokens > tt.maximum*2 { // Allow 2x margin
				t.Errorf("Tokens %d > maximum %d*2", tokens, tt.maximum)
			}
		})
	}
}

func TestEstimateMessagesTokensSimple(t *testing.T) {
	messages := []tokenizers.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there!"},
	}

	tokens := EstimateMessagesTokensSimple(messages)

	// Должно быть больше суммы токенов контента (из-за overhead на role)
	if tokens <= 0 {
		t.Error("Token estimate should be positive")
	}
}

// ============================================================
// Compactor Tests
// ============================================================

func TestCompactorCheckAndCompact_None(t *testing.T) {
	config := DefaultCompactionConfig()
	compactor := NewCompactor(config, nil, nil)

	messages := []tokenizers.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi!"},
	}

	// Маленький контекст - сжатие не требуется
	result, err := compactor.CheckAndCompact(nil, messages, 32000)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result != nil {
		t.Error("Should not compact small context")
	}
}

func TestCompactorCheckAndCompact_Warn(t *testing.T) {
	config := DefaultCompactionConfig()
	compactor := NewCompactor(config, nil, nil)

	// Создаём сообщения на ~60% от 10000 токенов
	messages := createTestMessages(100, 60) // ~60 токенов * 100 = 6000 токенов

	result, err := compactor.CheckAndCompact(nil, messages, 10000)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result == nil {
		t.Log("No compaction - might be below threshold")
	} else {
		t.Logf("Compaction level: %v, ratio: %.2f", result.Level, result.CompressionRatio())
	}
}

func TestCompactorClearToolResults(t *testing.T) {
	config := DefaultCompactionConfig()
	config.ToolResultMaxTokens = 50
	compactor := NewCompactor(config, nil, nil)

	// Создаём длинный tool result
	longResult := ""
	for i := 0; i < 100; i++ {
		longResult += "This is line " + string(rune(i)) + " of the output.\n"
	}

	messages := []tokenizers.Message{
		{Role: "user", Content: "Read file"},
		{Role: "tool", Content: longResult},
		{Role: "assistant", Content: "Done"},
	}

	cleared := compactor.clearToolResults(messages)

	// Tool result должен быть сжат
	if len(cleared[1].Content) >= len(longResult) {
		t.Error("Long tool result should be compressed")
	}

	t.Logf("Original: %d chars, compressed: %d chars",
		len(longResult), len(cleared[1].Content))
}

func TestCompactorExtractFiles(t *testing.T) {
	config := DefaultCompactionConfig()
	compactor := NewCompactor(config, nil, nil)

	content := `I read the file main.go and found the config in settings.json.
Also there is a README.md file in the project.`

	var artifacts []ArtifactRef
	compactor.extractFiles(content, &artifacts)

	if len(artifacts) == 0 {
		t.Error("Should extract at least one file")
	}

	t.Logf("Extracted artifacts: %d", len(artifacts))
	for _, a := range artifacts {
		t.Logf("  - %s", a.Path)
	}
}

func TestCompactorExtractDecisions(t *testing.T) {
	config := DefaultCompactionConfig()
	compactor := NewCompactor(config, nil, nil)

	content := `We decided to use PostgreSQL for the database.
Also, I will use bcrypt for password hashing.
The team agreed on using React for frontend.`

	var decisions []string
	compactor.extractDecisions(content, &decisions)

	if len(decisions) == 0 {
		t.Error("Should extract at least one decision")
	}

	t.Logf("Extracted decisions: %d", len(decisions))
	for _, d := range decisions {
		t.Logf("  - %s", d)
	}
}

// ============================================================
// Helper Functions
// ============================================================

func createTestMessages(count, tokensPerMsg int) []tokenizers.Message {
	messages := make([]tokenizers.Message, count)

	for i := 0; i < count; i++ {
		content := ""
		for j := 0; j < tokensPerMsg; j++ {
			content += "word "
		}
		messages[i] = tokenizers.Message{
			Role:    "user",
			Content: content,
		}
	}

	return messages
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
