package agentpolicy

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeAgentMD(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write md: %v", err)
	}
	return path
}

func TestSplitFrontmatter(t *testing.T) {
	fm, body, ok := splitFrontmatter("---\nname: a\n---\nhello\n")
	if !ok || fm != "name: a" || body != "hello\n" {
		t.Fatalf("ok=%v fm=%q body=%q", ok, fm, body)
	}
	fm2, body2, ok2 := splitFrontmatter("no frontmatter here\n")
	if ok2 || body2 != "no frontmatter here\n" || fm2 != "" {
		t.Fatalf("expected no frontmatter: ok=%v fm=%q body=%q", ok2, fm2, body2)
	}
}

func TestParseAgentMarkdownWithFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := writeAgentMD(t, dir, "scout.md", "---\nname: scout\ndescription: read only explorer\nmode: subagent\ntools: [read, grep, glob]\nsubagentTypes: [worker]\nleaf: true\n---\nYou are a scout.\nDo good work.\n")
	name, cfg, err := parseAgentMarkdown(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if name != "scout" {
		t.Errorf("name = %q, want scout", name)
	}
	if cfg.Description != "read only explorer" {
		t.Errorf("description = %q", cfg.Description)
	}
	if cfg.Mode != "subagent" {
		t.Errorf("mode = %q", cfg.Mode)
	}
	if !cfg.Leaf {
		t.Errorf("leaf = false, want true")
	}
	if len(cfg.Tools) != 3 {
		t.Errorf("tools = %v, want 3", cfg.Tools)
	}
	if len(cfg.SubagentTypes) != 1 || cfg.SubagentTypes[0] != "worker" {
		t.Errorf("subagentTypes = %v", cfg.SubagentTypes)
	}
	if cfg.Prompt != "You are a scout.\nDo good work.\n" {
		t.Errorf("prompt = %q", cfg.Prompt)
	}
	if cfg.Permission.GetAction("read") != "allow" {
		t.Errorf("read should be allowed")
	}
	if cfg.Permission.GetAction("bash") != "deny" {
		t.Errorf("bash should be denied")
	}
}

func TestParseAgentMarkdownNoFrontmatterUsesStem(t *testing.T) {
	dir := t.TempDir()
	path := writeAgentMD(t, dir, "plain.md", "Just a prompt body, no frontmatter.\n")
	name, cfg, err := parseAgentMarkdown(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if name != "plain" {
		t.Errorf("name = %q, want plain", name)
	}
	if cfg.Prompt != "Just a prompt body, no frontmatter.\n" {
		t.Errorf("prompt = %q", cfg.Prompt)
	}
	if cfg.Permission != nil {
		t.Errorf("permission should be nil without tools, got %v", cfg.Permission)
	}
}

func TestDiscoverAgentFilesPrecedenceAndSkip(t *testing.T) {
	userDir := t.TempDir()
	projDir := t.TempDir()
	writeAgentMD(t, userDir, "shared.md", "---\ndescription: from user\n---\nuser body\n")
	writeAgentMD(t, userDir, "useronly.md", "user only body\n")
	writeAgentMD(t, projDir, "shared.md", "---\ndescription: from project\n---\nproject body\n")
	writeAgentMD(t, projDir, "notes.txt", "not markdown\n")
	if err := os.Mkdir(filepath.Join(projDir, "subdir.md"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := DiscoverAgentFiles([]string{userDir, projDir, filepath.Join(userDir, "missing")})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("agents = %d, want 2", len(got))
	}
	if got["shared"].Description != "from project" {
		t.Errorf("shared precedence: description = %q, want from project", got["shared"].Description)
	}
	if _, ok := got["useronly"]; !ok {
		t.Errorf("useronly agent missing")
	}
}
func TestParseAgentMarkdownAcceptsCommaSeparatedToolLists(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string
	}{
		{"comma separated", "tools: read, grep, glob", []string{"read", "grep", "glob"}},
		{"no spaces", "tools: read,grep", []string{"read", "grep"}},
		{"flow list", "tools: [read, grep]", []string{"read", "grep"}},
		{"block list", "tools:\n  - read\n  - grep", []string{"read", "grep"}},
		{"single value", "tools: read", []string{"read"}},
		{"empty", "tools:", nil},
		{"comma subagentTypes", "subagentTypes: worker, qa", []string{"worker", "qa"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeAgentMD(t, dir, "agent.md", "---\n"+tc.line+"\n---\nbody\n")
			_, cfg, err := parseAgentMarkdown(filepath.Join(dir, "agent.md"))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var got []string
			if strings.Contains(tc.line, "subagentTypes") {
				got = cfg.SubagentTypes
			} else {
				got = cfg.Tools
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
func TestDiscoverAgentFilesSurvivesBrokenFiles(t *testing.T) {
	dir := t.TempDir()
	writeAgentMD(t, dir, "good.md", "---\ndescription: good\n---\ngood body\n")
	writeAgentMD(t, dir, "broken.md", "---\ndescription: [unclosed\n---\nbroken body\n")

	got, err := DiscoverAgentFiles([]string{dir})
	if err == nil {
		t.Fatal("expected error for broken agent file")
	}
	if !strings.Contains(err.Error(), "broken.md") {
		t.Errorf("error should name offending file: %v", err)
	}
	if got["good"].Description != "good" {
		t.Errorf("surviving agent lost, got %v", got)
	}
	if _, ok := got["broken"]; ok {
		t.Error("broken agent should be skipped")
	}
}
