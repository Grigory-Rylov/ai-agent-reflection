package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================
// Вспомогательные функции
// ============================================================

func setupTempDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "tools_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	return dir
}

func cleanupTempDir(t *testing.T, dir string) {
	os.RemoveAll(dir)
}

// ============================================================
// Тесты FileReadTool
// ============================================================

func TestFileReadTool(t *testing.T) {
	t.Run("reads file successfully", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		tool := &FileReadTool{}

		// Создаём тестовый файл
		testFile := filepath.Join(dir, "test.txt")
		os.WriteFile(testFile, []byte("Hello, World!"), 0644)

		result, err := tool.Execute(context.Background(), map[string]string{
			"path": testFile,
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Error)
		}

		data := result.Data.(map[string]interface{})
		if data["content"] != "Hello, World!" {
			t.Errorf("Expected 'Hello, World!', got '%s'", data["content"])
		}
	})

	t.Run("returns error for missing path", func(t *testing.T) {
		tool := &FileReadTool{}
		result, err := tool.Execute(context.Background(), map[string]string{})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if result.Success {
			t.Error("Expected failure for missing path")
		}
	})

	t.Run("returns error for non-existent file", func(t *testing.T) {
		tool := &FileReadTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"path": "/nonexistent/file.txt",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if result.Success {
			t.Error("Expected failure for non-existent file")
		}
	})
}

// ============================================================
// Тесты FileWriteTool
// ============================================================

func TestFileWriteTool(t *testing.T) {
	t.Run("writes file successfully", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		tool := &FileWriteTool{}
		testFile := filepath.Join(dir, "output.txt")

		result, err := tool.Execute(context.Background(), map[string]string{
			"path":    testFile,
			"content": "Test content",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Error)
		}

		// Проверяем что файл создан
		data, err := os.ReadFile(testFile)
		if err != nil {
			t.Fatalf("Failed to read written file: %v", err)
		}

		if string(data) != "Test content" {
			t.Errorf("Expected 'Test content', got '%s'", string(data))
		}
	})

	t.Run("returns error for missing parameters", func(t *testing.T) {
		tool := &FileWriteTool{}
		result, err := tool.Execute(context.Background(), map[string]string{})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if result.Success {
			t.Error("Expected failure for missing parameters")
		}
	})
}

// ============================================================
// Тесты TimeGetTool
// ============================================================

func TestTimeGetTool(t *testing.T) {
	t.Run("returns current time", func(t *testing.T) {
		tool := &TimeGetTool{}
		result, err := tool.Execute(context.Background(), map[string]string{})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Error)
		}

		data := result.Data.(map[string]interface{})
		if data["time"] == "" {
			t.Error("Expected non-empty time value")
		}
	})
}

// ============================================================
// Тесты DirListTool
// ============================================================

func TestDirListTool(t *testing.T) {
	t.Run("lists directory contents", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		// Создаём тестовые файлы
		os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("content1"), 0644)
		os.Mkdir(filepath.Join(dir, "subdir"), 0755)

		tool := &DirListTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"path": dir,
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Error)
		}

		data := result.Data.(map[string]interface{})
		if data["count"].(int) < 2 {
			t.Errorf("Expected at least 2 items, got %d", data["count"])
		}
	})

	t.Run("lists current directory by default", func(t *testing.T) {
		tool := &DirListTool{}
		result, err := tool.Execute(context.Background(), map[string]string{})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if !result.Success {
			t.Fatalf("Expected success for current directory, got error: %s", result.Error)
		}
	})
}

// ============================================================
// Тесты ShellExecuteTool
// ============================================================

func TestShellExecuteTool(t *testing.T) {
	t.Run("executes command successfully", func(t *testing.T) {
		tool := &ShellExecuteTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"command": "echo hello",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Error)
		}
		data := result.Data.(map[string]interface{})
		if data["output"] != "hello\n" {
			t.Errorf("Expected 'hello\\n', got '%s'", data["output"])
		}
	})

	t.Run("returns error for missing command", func(t *testing.T) {
		tool := &ShellExecuteTool{}
		result, err := tool.Execute(context.Background(), map[string]string{})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("Expected failure for missing command")
		}
	})
}

// ============================================================
// Тесты GlobTool
// ============================================================

func TestGlobTool(t *testing.T) {
	t.Run("finds files by pattern", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		os.WriteFile(filepath.Join(dir, "test.go"), []byte("package main"), 0644)
		os.WriteFile(filepath.Join(dir, "test.txt"), []byte("text"), 0644)

		tool := &GlobTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"pattern": "*.go",
			"path":    dir,
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Error)
		}
		data := result.Data.(map[string]interface{})
		if data["count"].(int) != 1 {
			t.Errorf("Expected 1 .go file, got %d", data["count"])
		}
	})

	t.Run("returns error for missing pattern", func(t *testing.T) {
		tool := &GlobTool{}
		result, err := tool.Execute(context.Background(), map[string]string{})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("Expected failure for missing pattern")
		}
	})
}

// ============================================================
// Тесты GrepTool
// ============================================================

func TestGrepTool(t *testing.T) {
	t.Run("finds pattern in files", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}"), 0644)
		os.WriteFile(filepath.Join(dir, "utils.go"), []byte("package main\nfunc helper() {}"), 0644)

		tool := &GrepTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"pattern": "func",
			"path":    dir,
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Error)
		}
		data := result.Data.(map[string]interface{})
		if data["count"].(int) < 2 {
			t.Errorf("Expected at least 2 matches, got %d", data["count"])
		}
	})

	t.Run("returns error for missing pattern", func(t *testing.T) {
		tool := &GrepTool{}
		result, err := tool.Execute(context.Background(), map[string]string{})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("Expected failure for missing pattern")
		}
	})
}

// ============================================================
// Тесты CalcTool
// ============================================================

func TestCalcTool(t *testing.T) {
	t.Run("evaluates simple expression", func(t *testing.T) {
		tool := &CalcTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"expression": "2 + 2 * 3",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Error)
		}
		data := result.Data.(map[string]interface{})
		if data["result"].(float64) != 8 {
			t.Errorf("Expected 8, got %v", data["result"])
		}
	})

	t.Run("returns error for missing expression", func(t *testing.T) {
		tool := &CalcTool{}
		result, err := tool.Execute(context.Background(), map[string]string{})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("Expected failure for missing expression")
		}
	})
}

// ============================================================
// Тесты EditTool
// ============================================================

func TestEditTool(t *testing.T) {
	t.Run("edits file successfully", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		testFile := filepath.Join(dir, "test.txt")
		os.WriteFile(testFile, []byte("Hello, World!\nHello, Universe!"), 0644)

		tool := &EditTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"path":       testFile,
			"old_string": "Hello",
			"new_string": "Hi",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Error)
		}

		data, _ := os.ReadFile(testFile)
		if string(data) != "Hi, World!\nHi, Universe!" {
			t.Errorf("Expected edited content, got '%s'", string(data))
		}
	})

	t.Run("returns error for missing parameters", func(t *testing.T) {
		tool := &EditTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"path": "/tmp/nonexistent.txt",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("Expected failure for missing parameters")
		}
	})
}

// ============================================================
// Дополнительные тесты ShellExecuteTool
// ============================================================

func TestShellExecuteToolExitCode(t *testing.T) {
	t.Run("returns exit code on failure", func(t *testing.T) {
		tool := &ShellExecuteTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"command": "exit 42",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		data := result.Data.(map[string]interface{})
		code, ok := data["exit_code"].(int)
		if !ok {
			fcode, ok := data["exit_code"].(float64)
			if ok {
				code = int(fcode)
			} else {
				t.Fatalf("exit_code has unexpected type %T", data["exit_code"])
			}
		}
		if code != 42 {
			t.Errorf("Expected exit code 42, got %d", code)
		}
	})
}

// ============================================================
// Дополнительные тесты GrepTool с фильтром include
// ============================================================

func TestGrepToolWithInclude(t *testing.T) {
	t.Run("filters by include pattern", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}"), 0644)
		os.WriteFile(filepath.Join(dir, "main.py"), []byte("def main():\n    pass"), 0644)

		tool := &GrepTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"pattern": "main",
			"path":    dir,
			"include": "*.go",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Fatalf("Expected success, got error: %s", result.Error)
		}
		data := result.Data.(map[string]interface{})
		results := data["results"].([]map[string]interface{})
		for _, r := range results {
			file := r["file"].(string)
			if strings.HasSuffix(file, ".py") {
				t.Errorf("Expected no .py files with include *.go, got %s", file)
			}
		}
	})
}

// ============================================================
// Тест Registry — все инструменты регистрируются и выполняются
// ============================================================

func TestRegistryAllToolsExecute(t *testing.T) {
	registry := NewRegistry()
	registry.Register(&FileReadTool{})
	registry.Register(&FileWriteTool{})
	registry.Register(&ShellExecuteTool{})
	registry.Register(&TimeGetTool{})
	registry.Register(&DirListTool{})
	registry.Register(&WebFetchTool{})
	registry.Register(&WebSearchTool{})
	registry.Register(&GlobTool{})
	registry.Register(&GrepTool{})
	registry.Register(&CalcTool{})
	registry.Register(&EditTool{})

	t.Run("all tools registered", func(t *testing.T) {
		if len(registry.GetAll()) != 11 {
			t.Errorf("Expected 11 tools registered, got %d", len(registry.GetAll()))
		}
	})

	t.Run("all tools have valid OpenAI schemas", func(t *testing.T) {
		schemas := registry.ToOpenAISchema()
		if len(schemas) != 11 {
			t.Errorf("Expected 11 tool schemas, got %d", len(schemas))
		}

		for _, s := range schemas {
			if s["type"] != "function" {
				t.Errorf("Expected type 'function', got '%v'", s["type"])
			}
			fn := s["function"].(map[string]interface{})
			if fn["name"] == "" {
				t.Error("Tool name should not be empty in OpenAI schema")
			}
			if fn["description"] == "" {
				t.Errorf("Tool '%v' has empty description", fn["name"])
			}
		}
	})

	t.Run("time_get executes via registry", func(t *testing.T) {
		tool, ok := registry.Get("time_get")
		if !ok {
			t.Fatal("time_get tool not found in registry")
		}
		result, err := tool.Execute(context.Background(), map[string]string{})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Errorf("time_get should succeed, got: %s", result.Error)
		}
	})

	t.Run("calc executes via registry", func(t *testing.T) {
		tool, ok := registry.Get("calc")
		if !ok {
			t.Fatal("calc tool not found in registry")
		}
		result, err := tool.Execute(context.Background(), map[string]string{
			"expression": "2 + 2",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Errorf("calc should succeed, got: %s", result.Error)
		}
		data := result.Data.(map[string]interface{})
		if data["result"].(float64) != 4 {
			t.Errorf("Expected 4, got %v", data["result"])
		}
	})
}

// ============================================================
// Тест resolvePath
// ============================================================

func TestResolvePath(t *testing.T) {
	t.Run("resolves absolute path", func(t *testing.T) {
		path, err := resolvePath("/tmp")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if path != "/tmp" {
			t.Errorf("Expected /tmp, got %s", path)
		}
	})

	t.Run("resolves relative path", func(t *testing.T) {
		path, err := resolvePath("some/file.txt")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !strings.HasSuffix(path, "some/file.txt") {
			t.Errorf("Expected path ending with 'some/file.txt', got %s", path)
		}
	})

	t.Run("empty path returns error", func(t *testing.T) {
		_, err := resolvePath("")
		if err == nil {
			t.Error("Expected error for empty path")
		}
	})
}

// ============================================================
// Тесты WebFetchTool (без сети — проверка параметров)
// ============================================================

func TestWebFetchTool(t *testing.T) {
	t.Run("returns error for missing url", func(t *testing.T) {
		tool := &WebFetchTool{}
		result, err := tool.Execute(context.Background(), map[string]string{})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("Expected failure for missing url")
		}
	})
}

// ============================================================
// Тесты WebSearchTool (без сети — проверка параметров)
// ============================================================

func TestWebSearchTool(t *testing.T) {
	t.Run("returns error for missing query", func(t *testing.T) {
		tool := &WebSearchTool{}
		result, err := tool.Execute(context.Background(), map[string]string{})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("Expected failure for missing query")
		}
	})
}

// ============================================================
// Тесты EvaluateExpression
// ============================================================

func TestEvaluateExpression(t *testing.T) {
	tests := []struct {
		expr    string
		want    float64
		wantErr bool
	}{
		{"2 + 2", 4, false},
		{"2 + 2 * 3", 8, false},
		{"(2 + 2) * 3", 12, false},
		{"10 / 2", 5, false},
		{"2 ** 3", 8, false},
		{"10 % 3", 1, false},
		{"-5 + 3", -2, false},
		{"3.5 * 2", 7, false},
		{"sqrt(9)", 3, false},
		{"abs(-5)", 5, false},
		{"round(3.7)", 4, false},
		{"1 / 0", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := EvaluateExpression(tt.expr)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Expected %v, got %v", tt.want, got)
			}
		})
	}
}

// ============================================================
// Тесты Tool Metadata
// ============================================================

func TestToolMetadata(t *testing.T) {
	tools := []struct {
		name string
		tool Tool
	}{
		{"FileReadTool", &FileReadTool{}},
		{"FileWriteTool", &FileWriteTool{}},
		{"ShellExecuteTool", &ShellExecuteTool{}},
		{"TimeGetTool", &TimeGetTool{}},
		{"DirListTool", &DirListTool{}},
		{"WebFetchTool", &WebFetchTool{}},
		{"WebSearchTool", &WebSearchTool{}},
		{"GlobTool", &GlobTool{}},
		{"GrepTool", &GrepTool{}},
		{"CalcTool", &CalcTool{}},
		{"EditTool", &EditTool{}},
	}

	for _, tt := range tools {
		t.Run(tt.name+"_metadata", func(t *testing.T) {
			if tt.tool.Name() == "" {
				t.Error("Tool name should not be empty")
			}
			if tt.tool.Description() == "" {
				t.Error("Tool description should not be empty")
			}
			if tt.tool.Schema() == nil {
				t.Error("Tool schema should not be nil")
			}
		})
	}
}

// ============================================================
// Тесты утилит
// ============================================================

func TestUnmarshalToolResult(t *testing.T) {
	t.Run("unmarshals valid JSON", func(t *testing.T) {
		jsonStr := `{"success": true, "data": {"key": "value"}}`
		result, err := UnmarshalToolResult(jsonStr)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Error("Expected success")
		}
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		_, err := UnmarshalToolResult("invalid json")
		if err == nil {
			t.Error("Expected error for invalid JSON")
		}
	})
}
