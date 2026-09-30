package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func astTempWD(t *testing.T) string {
	t.Helper()
	if _, err := resolveAstGrep(); err != nil {
		t.Skip("ast-grep binary not available")
	}
	dir := t.TempDir()
	prev := WorkingDir
	WorkingDir = dir
	t.Cleanup(func() { WorkingDir = prev })
	return dir
}

func astDataString(t *testing.T, result ToolResult, key string) string {
	t.Helper()
	data, ok := result.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map data, got %T", result.Data)
	}
	v, ok := data[key]
	if !ok {
		t.Fatalf("missing key %q in data", key)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("key %q: expected string, got %T", key, v)
	}
	return s
}

func astDataCount(t *testing.T, result ToolResult) int {
	t.Helper()
	data := result.Data.(map[string]interface{})
	count, ok := data["count"].(int)
	if !ok {
		t.Fatalf("count: expected int, got %T", data["count"])
	}
	return count
}

func TestAstGrepFindsGoPattern(t *testing.T) {
	dir := astTempWD(t)
	src := "package main\n\nfunc alpha() {\n}\n\nfunc beta() {\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &AstGrepTool{}
	result, err := tool.Execute(context.Background(), map[string]string{"pattern": "func $F()"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if got := astDataCount(t, result); got != 2 {
		t.Errorf("expected 2 matches, got %d", got)
	}
	out := astDataString(t, result, "output")
	if !strings.Contains(out, "a.go:3:1") || !strings.Contains(out, "func alpha") {
		t.Errorf("output missing expected location/text:\n%s", out)
	}
}

func TestAstGrepTsxPatternWithLang(t *testing.T) {
	dir := astTempWD(t)
	src := "export function App() {\n  console.log(\"a\", 1)\n  return <div/>\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "App.tsx"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &AstGrepTool{}
	result, err := tool.Execute(context.Background(), map[string]string{
		"pattern": "console.log($$$)",
		"path":    "App.tsx",
		"lang":    "tsx",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if got := astDataCount(t, result); got != 1 {
		t.Errorf("expected 1 match, got %d", got)
	}
	out := astDataString(t, result, "output")
	if !strings.Contains(out, "App.tsx:2:3") {
		t.Errorf("output missing 1-based location:\n%s", out)
	}
}

func TestAstGrepRequiresPattern(t *testing.T) {
	tool := &AstGrepTool{}
	result, err := tool.Execute(context.Background(), map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.Contains(result.Error, "pattern") {
		t.Errorf("expected pattern-required failure, got %+v", result)
	}
}

func TestAstGrepBinaryMissing(t *testing.T) {
	if _, err := resolveAstGrep(); err != nil {
		t.Skip("ast-grep binary not available")
	}
	t.Setenv("PATH", "")
	tool := &AstGrepTool{}
	result, err := tool.Execute(context.Background(), map[string]string{"pattern": "func $F()"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.Contains(result.Error, "not found") {
		t.Errorf("expected clear binary-not-found error, got %+v", result)
	}
}

func TestAstGrepInvalidPath(t *testing.T) {
	astTempWD(t)
	tool := &AstGrepTool{}
	result, err := tool.Execute(context.Background(), map[string]string{
		"pattern": "func $F()",
		"path":    "no_such_dir_xyz",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Errorf("expected failure for missing path, got %+v", result)
	}
}

func TestAstEditAppliesRewrite(t *testing.T) {
	dir := astTempWD(t)
	src := "package main\n\nfunc old() {\n}\n"
	target := filepath.Join(dir, "a.go")
	if err := os.WriteFile(target, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &AstEditTool{}
	result, err := tool.Execute(context.Background(), map[string]string{
		"pattern": "func $F()",
		"rewrite": "func Wrapped$F()",
		"dry_run": "false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	onDisk, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), "func Wrappedold") {
		t.Errorf("file not rewritten on disk:\n%s", onDisk)
	}
	if got := astDataCount(t, result); got != 1 {
		t.Errorf("expected 1 match, got %d", got)
	}
	if out := astDataString(t, result, "output"); !strings.Contains(out, "a.go") {
		t.Errorf("output missing changed file:\n%s", out)
	}
}

func TestAstEditDryRunLeavesFileUnchanged(t *testing.T) {
	dir := astTempWD(t)
	src := "package main\n\nfunc old() {\n}\n"
	target := filepath.Join(dir, "a.go")
	if err := os.WriteFile(target, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &AstEditTool{}
	result, err := tool.Execute(context.Background(), map[string]string{
		"pattern": "func $F()",
		"rewrite": "func Wrapped$F()",
		"dry_run": "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	onDisk, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != src {
		t.Errorf("dry_run must not modify file, got:\n%s", onDisk)
	}
	out := astDataString(t, result, "output")
	if !strings.Contains(out, "+ func Wrappedold") {
		t.Errorf("dry_run output missing replacement preview:\n%s", out)
	}
}

func TestAstEditNoMatchLeavesFileUnchanged(t *testing.T) {
	dir := astTempWD(t)
	src := "package main\n"
	target := filepath.Join(dir, "a.go")
	if err := os.WriteFile(target, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	tool := &AstEditTool{}
	result, err := tool.Execute(context.Background(), map[string]string{
		"pattern": "nosuchpattern $X",
		"rewrite": "whatever",
		"lang":    "go",
		"dry_run": "false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("no matches should still succeed, got error: %s", result.Error)
	}
	if got := astDataCount(t, result); got != 0 {
		t.Errorf("expected 0 matches, got %d", got)
	}
	onDisk, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != src {
		t.Errorf("file must be untouched:\n%s", onDisk)
	}
}

func TestAstEditRequiresRewrite(t *testing.T) {
	if _, err := resolveAstGrep(); err != nil {
		t.Skip("ast-grep binary not available")
	}
	tool := &AstEditTool{}
	result, err := tool.Execute(context.Background(), map[string]string{"pattern": "func $F()"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.Contains(result.Error, "rewrite") {
		t.Errorf("expected rewrite-required failure, got %+v", result)
	}
}

func TestAstToolsSchemas(t *testing.T) {
	for _, tool := range []Tool{&AstGrepTool{}, &AstEditTool{}} {
		if tool.Name() == "" || tool.Description() == "" {
			t.Errorf("empty name/description for %T", tool)
		}
		props, ok := tool.Schema()["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: schema missing properties", tool.Name())
		}
		if _, ok := props["pattern"]; !ok {
			t.Errorf("%s: schema missing pattern property", tool.Name())
		}
	}
	if (&AstGrepTool{}).Name() != "ast_grep" || (&AstEditTool{}).Name() != "ast_edit" {
		t.Errorf("unexpected tool names")
	}
}
