package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencode/llama-client/pkg/agentpolicy"
)

func TestConfigUnmarshalWithAgents(t *testing.T) {
	input := `{
		"llama_server_url": "127.0.0.1:8080",
		"model": "test-model",
		"max_tokens": 4096,
		"temperature": 0.7,
		"agents": {
			"lead": {
				"description": "Lead agent",
				"mode": "primary",
				"prompt": "agents/lead.md"
			},
			"developer": {
				"description": "Developer",
				"mode": "subagent",
				"prompt": "agents/developer.md"
			},
			"reviewer": {
				"description": "Code reviewer",
				"mode": "subagent",
				"leaf": true,
				"review": true,
				"prompt": "agents/reviewer.md",
				"permission": {
					"file_write": "deny",
					"edit": "deny"
				}
			},
			"qa": {
				"description": "QA engineer",
				"mode": "subagent",
				"leaf": true,
				"prompt": "agents/qa.md"
			}
		}
	}`

	var cfg Config
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if cfg.Agents == nil {
		t.Fatal("Agents field is nil")
	}

	t.Run("lead agent", func(t *testing.T) {
		a, ok := cfg.Agents["lead"]
		if !ok {
			t.Fatal("lead not found")
		}
		if a.Description != "Lead agent" {
			t.Errorf("description: got %q, want 'Lead agent'", a.Description)
		}
		if a.Mode != "primary" {
			t.Errorf("mode: got %q, want 'primary'", a.Mode)
		}
		if a.Prompt != "agents/lead.md" {
			t.Errorf("prompt: got %q, want 'agents/lead.md'", a.Prompt)
		}
	})

	t.Run("developer agent", func(t *testing.T) {
		a, ok := cfg.Agents["developer"]
		if !ok {
			t.Fatal("developer not found")
		}
		if a.Mode != "subagent" {
			t.Errorf("mode: got %q, want 'subagent'", a.Mode)
		}
		if a.Permission != nil {
			t.Error("developer should not have explicit permission")
		}
	})

	t.Run("reviewer agent with permission", func(t *testing.T) {
		a, ok := cfg.Agents["reviewer"]
		if !ok {
			t.Fatal("reviewer not found")
		}
		if !a.Leaf {
			t.Error("reviewer should be leaf")
		}
		if !a.Review {
			t.Error("reviewer should be review")
		}
		if a.Permission == nil {
			t.Fatal("reviewer should have permission")
		}
		if a.Permission.GetAction("file_write") != "deny" {
			t.Errorf("file_write: got %q, want deny", a.Permission.GetAction("file_write"))
		}
		if a.Permission.GetAction("edit") != "deny" {
			t.Errorf("edit: got %q, want deny", a.Permission.GetAction("edit"))
		}
		if a.Permission.GetAction("file_read") != "allow" {
			t.Errorf("file_read: got %q, want allow", a.Permission.GetAction("file_read"))
		}
	})

	t.Run("qa agent without permission", func(t *testing.T) {
		a, ok := cfg.Agents["qa"]
		if !ok {
			t.Fatal("qa not found")
		}
		if !a.Leaf {
			t.Error("qa should be leaf")
		}
		if a.Review {
			t.Error("qa should not be review")
		}
	})
}

func TestLoadConfigFileWithAgents(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfgContent := `{
		"llama_server_url": "127.0.0.1:8080",
		"model": "test-model",
		"max_tokens": 4096,
		"temperature": 0.7,
		"agents": {
			"lead": {
				"description": "Lead agent",
				"mode": "primary",
				"prompt": "agents/lead.md"
			},
			"developer": {
				"description": "Developer",
				"mode": "subagent",
				"prompt": "agents/developer.md"
			},
			"reviewer": {
				"description": "Code reviewer",
				"mode": "subagent",
				"leaf": true,
				"review": true,
				"prompt": "agents/reviewer.md",
				"permission": {
					"file_write": "deny",
					"edit": "deny",
					"apply_patch": "deny"
				}
			}
		}
	}`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Agents == nil {
		t.Fatal("Agents is nil")
	}
	if len(cfg.Agents) != 3 {
		t.Errorf("expected 3 agents, got %d", len(cfg.Agents))
	}
	// Verify reviewer permission loaded
	reviewer := cfg.Agents["reviewer"]
	if reviewer.Permission.GetAction("file_write") != "deny" {
		t.Errorf("file_write: got %q, want deny", reviewer.Permission.GetAction("file_write"))
	}
}

func TestInitAgentManagerWithPrompts(t *testing.T) {
	dir := t.TempDir()

	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create test .md prompts (without frontmatter — already stripped)
	leadPrompt := filepath.Join(agentsDir, "lead.md")
	if err := os.WriteFile(leadPrompt, []byte("You are a Lead Agent. Delegate tasks."), 0644); err != nil {
		t.Fatal(err)
	}

	devPrompt := filepath.Join(agentsDir, "developer.md")
	if err := os.WriteFile(devPrompt, []byte("You are a Developer. Write code."), 0644); err != nil {
		t.Fatal(err)
	}

	reviewerPrompt := filepath.Join(agentsDir, "reviewer.md")
	if err := os.WriteFile(reviewerPrompt, []byte("You are a Reviewer. Review code."), 0644); err != nil {
		t.Fatal(err)
	}

	agents := map[string]agentpolicy.AgentCfg{
		"lead": {
			Description: "Lead agent",
			Mode:        "primary",
			Prompt:      "agents/lead.md",
		},
		"developer": {
			Description: "Developer",
			Mode:        "subagent",
			Prompt:      "agents/developer.md",
		},
		"reviewer": {
			Description: "Code reviewer",
			Mode:        "subagent",
			Leaf:        true,
			Review:      true,
			Prompt:      "agents/reviewer.md",
			Permission: agentpolicy.NewPermissionFromConfig(map[string]string{
				"file_write": "deny",
				"edit":       "deny",
			}),
		},
	}

	log := &testLogger{}
	am := initAgentManager(agents, dir, log)

	t.Run("lead agent loaded", func(t *testing.T) {
		info, err := am.GetAgent("lead")
		if err != nil {
			t.Fatal(err)
		}
		if info.Description != "Lead agent" {
			t.Errorf("description: got %q", info.Description)
		}
		if info.Prompt != "You are a Lead Agent. Delegate tasks." {
			t.Errorf("prompt: got %q, want resolved content", info.Prompt)
		}
	})

	t.Run("developer agent loaded", func(t *testing.T) {
		info, err := am.GetAgent("developer")
		if err != nil {
			t.Fatal(err)
		}
		if info.Prompt != "You are a Developer. Write code." {
			t.Errorf("prompt: got %q, want resolved content", info.Prompt)
		}
		if info.Permission.GetAction("anything") != "allow" {
			t.Error("developer should have default allow permission")
		}
	})

	t.Run("reviewer agent with permission", func(t *testing.T) {
		info, err := am.GetAgent("reviewer")
		if err != nil {
			t.Fatal(err)
		}
		if !info.Leaf {
			t.Error("reviewer should be leaf")
		}
		if !info.Review {
			t.Error("reviewer should be review")
		}
		if info.Permission.GetAction("file_write") != "deny" {
			t.Errorf("file_write: got %q, want deny", info.Permission.GetAction("file_write"))
		}
		if info.Permission.GetAction("edit") != "deny" {
			t.Errorf("edit: got %q, want deny", info.Permission.GetAction("edit"))
		}
		if info.Permission.GetAction("file_read") != "allow" {
			t.Errorf("file_read: got %q, want allow", info.Permission.GetAction("file_read"))
		}
	})
}

func TestInitAgentManagerWithFrontmatter(t *testing.T) {
	dir := t.TempDir()
	agentsDir := filepath.Join(dir, "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create .md with frontmatter — should be stripped by LoadMDPrompt
	mdPath := filepath.Join(agentsDir, "sample.md")
	content := `---
description: Sample agent
mode: subagent
---
You are a Sample Agent. Do the thing.`
	if err := os.WriteFile(mdPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	agents := map[string]agentpolicy.AgentCfg{
		"sample": {
			Description: "Sample",
			Mode:        "subagent",
			Prompt:      "agents/sample.md",
		},
	}

	log := &testLogger{}
	am := initAgentManager(agents, dir, log)

	info, err := am.GetAgent("sample")
	if err != nil {
		t.Fatal(err)
	}
	if info.Prompt != "You are a Sample Agent. Do the thing." {
		t.Errorf("prompt should have frontmatter stripped:\n got: %q\nwant: %q", info.Prompt, "You are a Sample Agent. Do the thing.")
	}
}

func TestInitAgentManagerMissingPromptFile(t *testing.T) {
	agents := map[string]agentpolicy.AgentCfg{
		"missing": {
			Description: "Missing prompt agent",
			Mode:        "subagent",
			Prompt:      "agents/nonexistent.md",
		},
	}

	log := &testLogger{}
	am := initAgentManager(agents, "/tmp", log)

	// Agent with missing prompt file should be skipped (not registered)
	_, err := am.GetAgent("missing")
	if err == nil {
		t.Error("expected error for agent with missing prompt file")
	}
}

func TestInitAgentManagerNilAgents(t *testing.T) {
	log := &testLogger{}
	am := initAgentManager(nil, "/tmp", log)
	if am == nil {
		t.Fatal("AgentManager should not be nil")
	}
	// Should still have default agents
	_, err := am.GetAgent("build")
	if err != nil {
		t.Errorf("default agent 'build' should exist: %v", err)
	}
}

type testLogger struct{}

func (l *testLogger) InfoLogf(format string, args ...interface{}) {
	// silent logger for tests
}

func TestBuildQuestionTextKeepsOptions(t *testing.T) {
	q := map[string]interface{}{
		"header":   "🔐 bash",
		"question": "Allow shell command: cat > /tmp/file.py << 'PYEOF'?",
		"options": []interface{}{
			map[string]interface{}{"label": "✅ Allow", "description": "one time"},
			map[string]interface{}{"label": "✅ Always allow", "description": "session"},
			map[string]interface{}{"label": "❌ Deny", "description": "now"},
		},
	}

	text := buildQuestionText(q)

	if !strings.Contains(text, "Options:") {
		t.Error("expected Options block in question text")
	}
	for _, label := range []string{"✅ Allow", "✅ Always allow", "❌ Deny"} {
		if !strings.Contains(text, label) {
			t.Errorf("expected option %q in question text", label)
		}
	}
	if strings.Contains(text, "...") {
		t.Error("question text should not be truncated when short")
	}
}

func TestTruncateQuestionKeepsOptionsAndLimit(t *testing.T) {
	longQuestion := strings.Repeat("code line\n", 500)
	q := map[string]interface{}{
		"header":   "🔐 bash",
		"question": longQuestion,
		"options": []interface{}{
			map[string]interface{}{"label": "✅ Allow"},
			map[string]interface{}{"label": "❌ Deny"},
		},
	}

	text := buildQuestionText(q)

	if len([]rune(text)) > 4096 {
		t.Errorf("question text %d chars exceeds VK limit 4096", len([]rune(text)))
	}
	if !strings.Contains(text, "✅ Allow") || !strings.Contains(text, "❌ Deny") {
		t.Error("options must survive truncation")
	}
	if !strings.HasSuffix(text, "Reply with your choice") {
		t.Error("expected reply instruction at the end")
	}
	if !strings.Contains(text, "...") {
		t.Error("expected truncation marker for long question")
	}
}

func TestTruncateQuestionWithoutOptions(t *testing.T) {
	longQuestion := strings.Repeat("x", 5000)
	q := map[string]interface{}{
		"question": longQuestion,
	}

	text := buildQuestionText(q)

	if len([]rune(text)) > 4096 {
		t.Errorf("question text %d chars exceeds VK limit 4096", len([]rune(text)))
	}
	if !strings.Contains(text, "...") {
		t.Error("expected truncation marker")
	}
}
