package tools

import (
	"context"
	"os"
	"path/filepath"
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
// Тесты Tool Metadata
// ============================================================

func TestToolMetadata(t *testing.T) {
	tools := []struct {
		name string
		tool Tool
	}{
		{"FileReadTool", &FileReadTool{}},
		{"FileWriteTool", &FileWriteTool{}},
		{"TimeGetTool", &TimeGetTool{}},
		{"DirListTool", &DirListTool{}},
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
