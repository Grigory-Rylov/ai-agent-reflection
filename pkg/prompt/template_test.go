package prompt

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemplate(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name+".txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}
}

func TestResolve_Default(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "default", "You are {{MODEL}} running on {{PROVIDER}}")

	e := NewEngine(dir)
	result, err := e.Resolve(Config{
		Model: "test-model", Provider: "openai",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result != "You are test-model running on openai" {
		t.Errorf("unexpected: %s", result)
	}
}

func TestResolve_ProviderSpecific(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "default", "Base prompt for {{MODEL}}")
	writeTemplate(t, dir, "anthropic", "Claude-specific instructions")

	e := NewEngine(dir)
	result, err := e.Resolve(Config{
		Model: "claude-3", Provider: "anthropic",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if !contains(result, "Base prompt") {
		t.Error("expected base prompt")
	}
	if !contains(result, "Claude-specific") {
		t.Error("expected Anthropic prompt")
	}
}

func TestResolve_PlanMode(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "default", "Base prompt")
	writeTemplate(t, dir, "plan", "Plan mode instructions")

	e := NewEngine(dir)
	result, err := e.Resolve(Config{
		Model: "test", Provider: "openai", Mode: "plan",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if !contains(result, "Base prompt") {
		t.Error("expected base prompt")
	}
	if !contains(result, "Plan mode") {
		t.Error("expected plan mode prompt")
	}
}

func TestResolve_MissingTemplate(t *testing.T) {
	dir := t.TempDir()
	e := NewEngine(dir)
	_, err := e.Resolve(Config{Model: "test", Provider: "openai"})
	if err == nil {
		t.Error("expected error when no templates exist")
	}
}

func TestResolve_Interpolation(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "default",
		"Model: {{MODEL}} Provider: {{PROVIDER}} Dir: {{WORKING_DIR}}")

	e := NewEngine(dir)
	result, err := e.Resolve(Config{
		Model: "m", Provider: "p", WorkingDir: "/wd",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if result != "Model: m Provider: p Dir: /wd" && result != "Model: m Provider: p Dir: /wd\n" {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestSelectTemplates(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		expected []TemplateType
	}{
		{
			name: "default openai",
			cfg:  Config{Provider: "openai"},
			expected: []TemplateType{Default, OpenAI},
		},
		{
			name: "anthropic",
			cfg:  Config{Provider: "anthropic"},
			expected: []TemplateType{Default, Anthropic},
		},
		{
			name: "gemini",
			cfg:  Config{Provider: "google"},
			expected: []TemplateType{Default, Gemini},
		},
		{
			name: "plan mode",
			cfg:  Config{Provider: "openai", Mode: "plan"},
			expected: []TemplateType{Default, OpenAI, Plan},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := selectTemplates(tt.cfg)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %v, got %v", tt.expected, result)
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("index %d: expected %s, got %s", i, tt.expected[i], v)
				}
			}
		})
	}
}

func TestDetectProvider(t *testing.T) {
	tests := []struct {
		model    string
		expected string
	}{
		{"claude-3-opus", "anthropic"},
		{"claude-sonnet-4-20250514", "anthropic"},
		{"gemini-2.0-pro", "gemini"},
		{"gemma-3-27b-it", "gemini"},
		{"gpt-4o", "openai"},
		{"o1-mini", "openai"},
		{"llama-3.1-70b", "openai"},
		{"deepseek-v3", "openai"},
		{"unknown-model", "openai"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := DetectProvider(tt.model)
			if got != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, got)
			}
		})
	}
}

func TestCache(t *testing.T) {
	dir := t.TempDir()
	writeTemplate(t, dir, "default", "version 1")

	e := NewEngine(dir)
	r1, _ := e.Resolve(Config{Model: "m", Provider: "openai"})

	os.WriteFile(filepath.Join(dir, "default.txt"), []byte("version 2"), 0644)
	r2, _ := e.Resolve(Config{Model: "m", Provider: "openai"})

	if r1 != r2 {
		t.Error("expected cached result")
	}

	e.InvalidateCache()
	r3, _ := e.Resolve(Config{Model: "m", Provider: "openai"})
	if r3 == r1 {
		t.Error("expected different result after cache invalidation")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		len(s) == len(s)-len(substr)+len(substr) &&
		findSubstring(s, substr) >= 0
}

func findSubstring(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
