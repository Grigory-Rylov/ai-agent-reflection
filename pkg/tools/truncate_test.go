package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)


func TestTruncateToolResult_PassesThroughSmallOutput(t *testing.T) {
	dir := t.TempDir()
	content := `{"success":true,"data":{"output":"short"}}`

	res, err := TruncateToolResult(content, TruncateOptions{MaxLines: 100, MaxBytes: 1024, Dir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Truncated {
		t.Errorf("expected not truncated, got truncated")
	}
	if res.Content != content {
		t.Errorf("expected content unchanged, got %q", res.Content)
	}
	if res.OutputPath != "" {
		t.Errorf("expected empty output path, got %q", res.OutputPath)
	}
}


func TestTruncateToolResult_TruncatesLargeOutput(t *testing.T) {
	dir := t.TempDir()
	content := `{"success":true,"data":{"output":"` + strings.Repeat("a", 4096) + `"}}`

	res, err := TruncateToolResult(content, TruncateOptions{MaxLines: 100, MaxBytes: 1024, Dir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated=true")
	}
	if res.OutputPath == "" {
		t.Fatal("expected output path to be set")
	}
	if !strings.Contains(res.Content, "truncated") {
		t.Errorf("expected 'truncated' marker in content, got %q", res.Content)
	}
	if !strings.Contains(res.Content, res.OutputPath) {
		t.Errorf("expected hint to mention saved file %q, got %q", res.OutputPath, res.Content)
	}
}


func TestTruncateToolResult_WritesFullOutputToFile(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("line-of-output\n", 5000)

	res, err := TruncateToolResult(content, TruncateOptions{MaxLines: 100, MaxBytes: 4096, Dir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated=true")
	}
	saved, err := os.ReadFile(res.OutputPath)
	if err != nil {
		t.Fatalf("failed to read saved output: %v", err)
	}
	if string(saved) != content {
		t.Errorf("saved output differs from original (len %d vs %d)", len(saved), len(content))
	}
	if len(res.Content) >= len(content) {
		t.Errorf("expected preview shorter than original, got %d vs %d", len(res.Content), len(content))
	}
}


func TestTruncateToolResult_RespectsMaxBytes(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("x", 10000)

	res, err := TruncateToolResult(content, TruncateOptions{MaxLines: 10, MaxBytes: 2048, Dir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated=true")
	}
	if len(res.Content) > 4096 {
		t.Errorf("preview too large: %d bytes", len(res.Content))
	}
}


func TestTruncateToolResult_HeadDirection(t *testing.T) {
	dir := t.TempDir()
	content := "HEAD-KEEP\n" + strings.Repeat("tail-line\n", 500)

	res, err := TruncateToolResult(content, TruncateOptions{MaxLines: 1, MaxBytes: 1 << 20, Dir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated=true")
	}
	if !strings.HasPrefix(res.Content, "HEAD-KEEP") {
		t.Errorf("expected head preserved, got %q", res.Content)
	}
}


func TestTruncateToolResult_Defaults(t *testing.T) {
	dir := t.TempDir()
	res, err := TruncateToolResult("small", TruncateOptions{Dir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Truncated {
		t.Error("expected not truncated")
	}

	big := strings.Repeat("y", DefaultToolOutputMaxBytes+1)
	res, err = TruncateToolResult(big, TruncateOptions{Dir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Truncated {
		t.Error("expected truncated with default limits")
	}
}


func TestTruncateToolResult_HasTaskTool(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("z", 8192)

	withTask, err := TruncateToolResult(content, TruncateOptions{MaxLines: 10, MaxBytes: 1024, Dir: dir, HasTaskTool: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(withTask.Content, "Task tool") {
		t.Errorf("expected Task tool hint, got %q", withTask.Content)
	}

	withoutTask, err := TruncateToolResult(content, TruncateOptions{MaxLines: 10, MaxBytes: 1024, Dir: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(withoutTask.Content, "Task tool") {
		t.Errorf("did not expect Task tool hint, got %q", withoutTask.Content)
	}
}


func TestTruncateToolResult_DefaultDir(t *testing.T) {
	oldWD := WorkingDir
	oldBase := BaseDir
	defer func() { WorkingDir = oldWD; BaseDir = oldBase }()
	WorkingDir = t.TempDir()
	BaseDir = t.TempDir()

	content := strings.Repeat("w", 8192)
	res, err := TruncateToolResult(content, TruncateOptions{MaxLines: 10, MaxBytes: 1024})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated=true")
	}
	if _, err := os.Stat(res.OutputPath); err != nil {
		t.Errorf("saved file not found: %v", err)
	}
	expectedDir := filepath.Join(BaseDir, "tool-output")
	if filepath.Dir(res.OutputPath) != expectedDir {
		t.Errorf("expected output in %q, got %q", expectedDir, res.OutputPath)
	}
}


func TestTruncateToolResult_Cleanup(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "tool_stale")
	if err := os.WriteFile(stale, []byte("old"), 0644); err != nil {
		t.Fatalf("failed to create stale file: %v", err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("failed to set stale mtime: %v", err)
	}

	content := strings.Repeat("q", 8192)
	if _, err := TruncateToolResult(content, TruncateOptions{MaxLines: 10, MaxBytes: 1024, Dir: dir}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("expected stale file removed, got err=%v", err)
	}
}
