package agentpolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    struct {
			desc  string
			mode  string
			leaf  bool
			model string
		}
		wantBody string
	}{
		{
			name:    "no frontmatter",
			content: "just a prompt body",
			want: struct {
				desc  string
				mode  string
				leaf  bool
				model string
			}{},
			wantBody: "just a prompt body",
		},
		{
			name:    "empty content",
			content: "",
			want: struct {
				desc  string
				mode  string
				leaf  bool
				model string
			}{},
			wantBody: "",
		},
		{
			name: "full frontmatter",
			content: `---
description: Test agent
mode: subagent
leaf: true
model: gpt-4
---
You are a test agent.`,
			want: struct {
				desc  string
				mode  string
				leaf  bool
				model string
			}{
				desc:  "Test agent",
				mode:  "subagent",
				leaf:  true,
				model: "gpt-4",
			},
			wantBody: "You are a test agent.",
		},
		{
			name: "frontmatter with tools",
			content: `---
description: Tool agent
mode: subagent
tools:
  write: true
  edit: false
  bash: ask
---
Agent body`,
			want: struct {
				desc  string
				mode  string
				leaf  bool
				model string
			}{
				desc: "Tool agent",
				mode: "subagent",
			},
			wantBody: "Agent body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body, err := ParseFrontmatter(tt.content)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fm.Description != tt.want.desc {
				t.Errorf("description = %q, want %q", fm.Description, tt.want.desc)
			}
			if fm.Mode != tt.want.mode {
				t.Errorf("mode = %q, want %q", fm.Mode, tt.want.mode)
			}
			if fm.Leaf != tt.want.leaf {
				t.Errorf("leaf = %v, want %v", fm.Leaf, tt.want.leaf)
			}
			if fm.Model != tt.want.model {
				t.Errorf("model = %q, want %q", fm.Model, tt.want.model)
			}
			if body != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

func TestParseFrontmatterFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	content := `---
description: File parsed agent
mode: primary
---
Prompt body from file`

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	fm, body, err := ParseFrontmatterFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Description != "File parsed agent" {
		t.Errorf("description = %q, want %q", fm.Description, "File parsed agent")
	}
	if fm.Mode != "primary" {
		t.Errorf("mode = %q, want %q", fm.Mode, "primary")
	}
	if body != "Prompt body from file" {
		t.Errorf("body = %q, want %q", body, "Prompt body from file")
	}
}

func TestParseFrontmatterFileNotFound(t *testing.T) {
	_, _, err := ParseFrontmatterFile("/nonexistent/path.md")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
