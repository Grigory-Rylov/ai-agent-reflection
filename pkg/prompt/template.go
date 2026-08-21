package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type TemplateType string

const (
	Default   TemplateType = "default"
	OpenAI    TemplateType = "openai"
	Anthropic TemplateType = "anthropic"
	Gemini    TemplateType = "gemini"
	Plan      TemplateType = "plan"
)

type Config struct {
	Model       string
	Provider    string
	WorkingDir  string
	Tools       []string
	MaxTokens   int
	Temperature float64
	Mode        string
}

type Engine struct {
	promptsDir string
	cache      map[TemplateType]string
}

func NewEngine(promptsDir string) *Engine {
	return &Engine{
		promptsDir: promptsDir,
		cache:      make(map[TemplateType]string),
	}
}

func (e *Engine) Resolve(cfg Config) (string, error) {
	templates := selectTemplates(cfg)

	var parts []string
	for i, t := range templates {
		content, err := e.load(t)
		if err != nil {
			return "", fmt.Errorf("load template %s: %w", t, err)
		}
		if content == "" && i == 0 {
			return "", fmt.Errorf("default template is empty in %s", e.promptsDir)
		}
		if content != "" {
			parts = append(parts, content)
		}
	}

	if len(parts) == 0 {
		return "", fmt.Errorf("no prompt templates found in %s", e.promptsDir)
	}

	combined := strings.Join(parts, "\n\n")
	combined = interpolate(combined, cfg)
	combined = strings.TrimSpace(combined)
	return combined, nil
}
func (e *Engine) load(t TemplateType) (string, error) {
	if cached, ok := e.cache[t]; ok {
		return cached, nil
	}

	path := filepath.Join(e.promptsDir, string(t)+".txt")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	content := strings.TrimSpace(string(data))
	e.cache[t] = content
	return content, nil
}

func (e *Engine) InvalidateCache() {
	e.cache = make(map[TemplateType]string)
}

func selectTemplates(cfg Config) []TemplateType {
	result := []TemplateType{Default}

	provider := strings.ToLower(cfg.Provider)
	switch {
	case provider == "anthropic" || provider == "claude":
		result = append(result, Anthropic)
	case provider == "gemini" || provider == "google":
		result = append(result, Gemini)
	default:
		result = append(result, OpenAI)
	}

	if cfg.Mode == "plan" {
		result = append(result, Plan)
	}

	return result
}

func interpolate(text string, cfg Config) string {
	result := text
	repl := map[string]string{
		"{{MODEL}}":       cfg.Model,
		"{{PROVIDER}}":    cfg.Provider,
		"{{WORKING_DIR}}": cfg.WorkingDir,
		"{{MAX_TOKENS}}":  fmt.Sprintf("%d", cfg.MaxTokens),
		"{{TEMPERATURE}}": fmt.Sprintf("%.1f", cfg.Temperature),
		"{{TOOLS}}":       strings.Join(cfg.Tools, ", "),
		"{{MODE}}":        cfg.Mode,
	}
	for k, v := range repl {
		result = strings.ReplaceAll(result, k, v)
	}
	return result
}

func DetectProvider(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "claude") || strings.Contains(m, "anthropic"):
		return "anthropic"
	case strings.Contains(m, "gemini") || strings.Contains(m, "gemma"):
		return "gemini"
	case strings.Contains(m, "gpt") || strings.Contains(m, "o1") || strings.Contains(m, "o3"):
		return "openai"
	default:
		return "openai"
	}
}
