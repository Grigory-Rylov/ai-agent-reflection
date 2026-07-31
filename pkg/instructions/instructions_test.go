package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func makeGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestProjectFiles_FindsInWorkingDir(t *testing.T) {
	root := t.TempDir()
	makeGitRepo(t, root)
	writeFile(t, filepath.Join(root, "AGENTS.md"), "project instructions")

	files := projectFiles(root)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(files), files)
	}
	if !strings.HasSuffix(files[0], "AGENTS.md") {
		t.Errorf("expected AGENTS.md, got %q", files[0])
	}
}

func TestProjectFiles_NearestFirst(t *testing.T) {
	root := t.TempDir()
	makeGitRepo(t, root)
	writeFile(t, filepath.Join(root, "AGENTS.md"), "root instructions")

	sub := filepath.Join(root, "a", "b")
	writeFile(t, filepath.Join(sub, "AGENTS.md"), "sub instructions")

	files := projectFiles(sub)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(files), files)
	}
	if !strings.HasSuffix(files[0], filepath.Join("b", "AGENTS.md")) {
		t.Errorf("expected nearest file first, got %q", files[0])
	}
	if !strings.HasSuffix(files[1], filepath.Join(root, "AGENTS.md")) {
		t.Errorf("expected root file second, got %q", files[1])
	}
}

func TestProjectFiles_StopsAtGitRoot(t *testing.T) {
	root := t.TempDir()
	makeGitRepo(t, root)
	writeFile(t, filepath.Join(root, "AGENTS.md"), "root instructions")

	above := filepath.Join(filepath.Dir(root), "AGENTS.md")
	writeFile(t, above, "above repo instructions")
	defer os.Remove(above)

	sub := filepath.Join(root, "a", "b")
	files := projectFiles(sub)
	for _, f := range files {
		if f == above {
			t.Error("expected file above git root to be excluded")
		}
	}
}

func TestProjectFiles_CLAUDE_Fallback(t *testing.T) {
	root := t.TempDir()
	makeGitRepo(t, root)
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "claude instructions")
	files := projectFiles(root)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d: %v", len(files), files)
	}
	if !strings.HasSuffix(files[0], "CLAUDE.md") {
		t.Errorf("expected CLAUDE.md, got %q", files[0])
	}
}

func TestProjectFiles_AGENTSTakesPriority(t *testing.T) {
	root := t.TempDir()
	makeGitRepo(t, root)
	writeFile(t, filepath.Join(root, "AGENTS.md"), "agents")
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "claude")

	files := projectFiles(root)
	if len(files) != 1 {
		t.Fatalf("expected only AGENTS.md to win, got %d: %v", len(files), files)
	}
	if !strings.HasSuffix(files[0], "AGENTS.md") {
		t.Errorf("expected AGENTS.md to take priority, got %q", files[0])
	}
}

func TestBuild_FormatsInstructions(t *testing.T) {
	oldHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", oldHome) })
	// Перенаправляем глобальную директорию, чтобы тест не зависел от машины
	home := t.TempDir()
	os.Setenv("HOME", home)
	configDir = filepath.Join(home, ".config", "ai-agent")

	root := t.TempDir()
	makeGitRepo(t, root)
	writeFile(t, filepath.Join(root, "AGENTS.md"), "project instructions")

	content := Build(root)
	if !strings.Contains(content, "Instructions from: "+filepath.Join(root, "AGENTS.md")) {
		t.Errorf("expected 'Instructions from:' header, got:\n%s", content)
	}
	if !strings.Contains(content, "project instructions") {
		t.Errorf("expected project instructions content, got:\n%s", content)
	}
}

func TestBuild_EmptyWhenNoFiles(t *testing.T) {
	oldHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", oldHome) })
	home := t.TempDir()
	os.Setenv("HOME", home)
	configDir = filepath.Join(home, ".config", "ai-agent")

	root := t.TempDir()
	makeGitRepo(t, root)

	if content := Build(root); content != "" {
		t.Errorf("expected empty content, got:\n%s", content)
	}
}

func TestBuild_ReadsGlobalAGENTS(t *testing.T) {
	oldHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", oldHome) })
	home := t.TempDir()
	os.Setenv("HOME", home)
	configDir = filepath.Join(home, ".config", "ai-agent")

	writeFile(t, filepath.Join(configDir, "AGENTS.md"), "global instructions")

	root := t.TempDir()
	makeGitRepo(t, root)

	content := Build(root)
	if !strings.Contains(content, "global instructions") {
		t.Errorf("expected global instructions in output, got:\n%s", content)
	}
}
