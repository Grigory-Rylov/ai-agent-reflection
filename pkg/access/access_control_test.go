package access

import (
	"os"
	"path/filepath"
	"testing"
)

// ============================================================
// Тесты Access Control
// ============================================================

func setupTempDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "access_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	return dir
}

func cleanupTempDir(t *testing.T, dir string) {
	os.RemoveAll(dir)
}

func TestNewController(t *testing.T) {
	t.Run("creates controller with allowed dirs", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		controller := NewController([]string{dir})

		if controller == nil {
			t.Fatal("Controller should not be nil")
		}
	})

	t.Run("handles empty allowed dirs", func(t *testing.T) {
		controller := NewController([]string{})

		if controller == nil {
			t.Fatal("Controller should not be nil")
		}
	})
}

func TestCheckAccess(t *testing.T) {
	t.Run("allows path inside allowed directory", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		controller := NewController([]string{dir})
		testFile := filepath.Join(dir, "test.txt")

		result := controller.CheckAccess(testFile)

		if !result.Allowed {
			t.Errorf("expected access allowed, got denied: %s", result.Reason)
		}
	})

	t.Run("denies path outside allowed directory", func(t *testing.T) {
		allowedDir := setupTempDir(t)
		otherDir := setupTempDir(t)
		defer cleanupTempDir(t, allowedDir)
		defer cleanupTempDir(t, otherDir)

		controller := NewController([]string{allowedDir})
		testFile := filepath.Join(otherDir, "test.txt")

		result := controller.CheckAccess(testFile)

		if result.Allowed {
			t.Error("expected access denied, got allowed")
		}
		if result.Reason == "" {
			t.Error("expected reason for denied access")
		}
	})

	t.Run("handles path traversal attempts", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		controller := NewController([]string{dir})
		// Попытка выйти за пределы через ..
		testFile := filepath.Join(dir, "..", "..", "etc", "passwd")

		result := controller.CheckAccess(testFile)

		// После канонизации путь должен быть проверен
		if result.Allowed {
			t.Error("path traversal should be blocked")
		}
	})
}

func TestSanitizePath(t *testing.T) {
	t.Run("cleans path traversal", func(t *testing.T) {
		path := "../../etc/passwd"
		cleaned := SanitizePath(path)

		if cleaned != "" {
			t.Errorf("expected empty string for path traversal, got '%s'", cleaned)
		}
	})

	t.Run("returns cleaned normal path", func(t *testing.T) {
		path := "./subdir/file.txt"
		cleaned := SanitizePath(path)

		if cleaned == "" {
			t.Error("expected cleaned path, got empty string")
		}
	})
}

func TestIsPathSafe(t *testing.T) {
	t.Run("safe path passes", func(t *testing.T) {
		if !IsPathSafe("/home/user/documents/file.txt") {
			t.Error("expected safe path")
		}
	})

	t.Run("path with semicolon is unsafe", func(t *testing.T) {
		if IsPathSafe("/home/user/file.txt; rm -rf /") {
			t.Error("path with semicolon should be unsafe")
		}
	})

	t.Run("path with backtick is unsafe", func(t *testing.T) {
		if IsPathSafe("/home/user/`whoami`.txt") {
			t.Error("path with backtick should be unsafe")
		}
	})

	t.Run("path traversal is unsafe", func(t *testing.T) {
		if IsPathSafe("../secret/file.txt") {
			t.Error("path traversal should be unsafe")
		}
	})
}

func TestSafeReadFile(t *testing.T) {
	t.Run("reads file with allowed access", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		// Создаём тестовый файл
		testFile := filepath.Join(dir, "test.txt")
		content := "Hello, World!"
		os.WriteFile(testFile, []byte(content), 0644)

		controller := NewController([]string{dir})
		data, result := controller.SafeReadFile(testFile)

		if !result.Allowed {
			t.Errorf("expected allowed, got denied: %s", result.Reason)
		}
		if string(data) != content {
			t.Errorf("expected '%s', got '%s'", content, string(data))
		}
	})

	t.Run("denies read outside allowed directory", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		controller := NewController([]string{dir})
		_, result := controller.SafeReadFile("/etc/passwd")

		if result.Allowed {
			t.Error("expected read denied")
		}
	})
}

func TestSafeWriteFile(t *testing.T) {
	t.Run("writes file with allowed access", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		testFile := filepath.Join(dir, "output.txt")
		content := []byte("Test content")

		controller := NewController([]string{dir})
		result := controller.SafeWriteFile(testFile, content)

		if !result.Allowed {
			t.Errorf("expected allowed, got denied: %s", result.Reason)
		}

		// Проверяем что файл создан
		data, err := os.ReadFile(testFile)
		if err != nil {
			t.Fatalf("failed to read written file: %v", err)
		}
		if string(data) != string(content) {
			t.Errorf("file content mismatch")
		}
	})

	t.Run("denies write outside allowed directory", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		controller := NewController([]string{dir})
		result := controller.SafeWriteFile("/etc/systemd/system/test.service", []byte("content"))

		if result.Allowed {
			t.Error("expected write denied")
		}
	})
}
