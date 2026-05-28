package access

import (
	"os"
	"path/filepath"
	"testing"
)

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
		testFile := filepath.Join(dir, "..", "..", "etc", "passwd")

		result := controller.CheckAccess(testFile)

		if result.Allowed {
			t.Error("path traversal should be blocked")
		}
	})

	t.Run("denies access when no dirs configured", func(t *testing.T) {
		controller := NewController([]string{})
		result := controller.CheckAccess("/some/path")
		if result.Allowed {
			t.Error("expected denied when no dirs configured")
		}
	})

	t.Run("allows subdirectory inside allowed dir", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		subdir := filepath.Join(dir, "subdir")
		os.MkdirAll(subdir, 0755)

		controller := NewController([]string{dir})
		result := controller.CheckAccess(filepath.Join(subdir, "file.txt"))

		if !result.Allowed {
			t.Errorf("expected allowed for subdirectory, got: %s", result.Reason)
		}
	})
}

func TestGrantPath(t *testing.T) {
	t.Run("grants access to new path", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		controller := NewController([]string{})
		testFile := filepath.Join(dir, "test.txt")

		result := controller.CheckAccess(testFile)
		if result.Allowed {
			t.Fatal("expected denied before grant")
		}

		controller.GrantPath(dir)

		result = controller.CheckAccess(testFile)
		if !result.Allowed {
			t.Errorf("expected allowed after grant, got: %s", result.Reason)
		}
	})

	t.Run("revoke removes granted access", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		controller := NewController([]string{})
		controller.GrantPath(dir)

		result := controller.CheckAccess(filepath.Join(dir, "file.txt"))
		if !result.Allowed {
			t.Fatal("expected allowed after grant")
		}

		controller.RevokePath(dir)

		result = controller.CheckAccess(filepath.Join(dir, "file.txt"))
		if result.Allowed {
			t.Error("expected denied after revoke")
		}
	})

	t.Run("grants and allows multiple dirs", func(t *testing.T) {
		dir1 := setupTempDir(t)
		dir2 := setupTempDir(t)
		defer cleanupTempDir(t, dir1)
		defer cleanupTempDir(t, dir2)

		controller := NewController([]string{dir1})
		controller.GrantPath(dir2)

		if !controller.CheckAccess(filepath.Join(dir1, "a.txt")).Allowed {
			t.Error("dir1 should be allowed (global)")
		}
		if !controller.CheckAccess(filepath.Join(dir2, "b.txt")).Allowed {
			t.Error("dir2 should be allowed (session grant)")
		}
	})
}

func TestAllowedDirs(t *testing.T) {
	t.Run("returns all dirs including session", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		controller := NewController([]string{dir})
		additionalDir := setupTempDir(t)
		defer cleanupTempDir(t, additionalDir)

		controller.GrantPath(additionalDir)

		dirs := controller.AllowedDirs()
		if len(dirs) != 2 {
			t.Errorf("expected 2 dirs, got %d", len(dirs))
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

func TestAddAllowedDir(t *testing.T) {
	t.Run("adds dir after creation", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		controller := NewController([]string{})
		controller.AddAllowedDir(dir)

		result := controller.CheckAccess(filepath.Join(dir, "test.txt"))
		if !result.Allowed {
			t.Errorf("expected allowed after AddAllowedDir, got: %s", result.Reason)
		}
	})
}

func TestCheckAccess_EdgeCases(t *testing.T) {
	t.Run("exact path match is allowed", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		controller := NewController([]string{dir})
		result := controller.CheckAccess(dir)
		if !result.Allowed {
			t.Errorf("exact match should be allowed, got: %s", result.Reason)
		}
	})

	t.Run("parent directory is denied", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		parent := filepath.Dir(dir)
		controller := NewController([]string{dir})
		result := controller.CheckAccess(parent)
		if result.Allowed {
			t.Error("parent directory should be denied")
		}
	})

	t.Run("sibling directory is denied", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		sibling := filepath.Join(dir, "..", "sibling")
		controller := NewController([]string{dir})
		result := controller.CheckAccess(sibling)
		if result.Allowed {
			t.Error("sibling directory should be denied")
		}
	})

	t.Run("multiple allowed dirs all work", func(t *testing.T) {
		dir1 := setupTempDir(t)
		dir2 := setupTempDir(t)
		defer cleanupTempDir(t, dir1)
		defer cleanupTempDir(t, dir2)

		controller := NewController([]string{dir1, dir2})

		if !controller.CheckAccess(filepath.Join(dir1, "a.txt")).Allowed {
			t.Error("dir1 should be allowed")
		}
		if !controller.CheckAccess(filepath.Join(dir2, "b.txt")).Allowed {
			t.Error("dir2 should be allowed")
		}
	})

	t.Run("non-existent file in allowed dir is allowed", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		controller := NewController([]string{dir})
		result := controller.CheckAccess(filepath.Join(dir, "nonexistent", "file.txt"))
		if !result.Allowed {
			t.Errorf("non-existent file in allowed dir should be allowed, got: %s", result.Reason)
		}
	})

	t.Run("path in new subdir is within allowed dir", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		controller := NewController([]string{dir})
		result := controller.CheckAccess(filepath.Join(dir, "newdir", "file.txt"))
		if !result.Allowed {
			t.Errorf("path in new subdir should be within allowed dir, got: %s", result.Reason)
		}
	})

	t.Run("write to existing subdir in allowed dir is allowed", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		subdir := filepath.Join(dir, "existingsub")
		if err := os.MkdirAll(subdir, 0755); err != nil {
			t.Fatalf("failed to create subdir: %v", err)
		}

		controller := NewController([]string{dir})
		result := controller.CheckWriteAccess(filepath.Join(subdir, "file.txt"))
		if !result.Allowed {
			t.Errorf("write to existing subdir should be allowed, got: %s", result.Reason)
		}
	})

	t.Run("non-existent parent outside allowed dir is denied", func(t *testing.T) {
		dir := setupTempDir(t)
		defer cleanupTempDir(t, dir)

		controller := NewController([]string{dir})
		result := controller.CheckAccess("/nonexistent_parent/file.txt")
		if result.Allowed {
			t.Error("path in non-existent parent outside allowed dir should be denied")
		}
	})
}

func TestCheckAccess_Symlink(t *testing.T) {
	t.Run("symlink inside allowed dir to outside is blocked", func(t *testing.T) {
		allowedDir := setupTempDir(t)
		outsideDir := setupTempDir(t)
		defer cleanupTempDir(t, allowedDir)
		defer cleanupTempDir(t, outsideDir)

		// Создаём симлинк внутри разрешённой директории, указывающий наружу
		linkPath := filepath.Join(allowedDir, "outside_link")
		if err := os.Symlink(outsideDir, linkPath); err != nil {
			t.Skip("symlink not supported on this system")
		}

		controller := NewController([]string{allowedDir})

		// Сам симлинк разрешается в outsideDir, поэтому должен быть заблокирован
		result := controller.CheckAccess(linkPath)
		if result.Allowed {
			t.Error("symlink that resolves outside allowed dir should be blocked")
		}

		// Файл через симлинк (фактически снаружи) должен быть запрещён
		result = controller.CheckAccess(filepath.Join(linkPath, "secret.txt"))
		if result.Allowed {
			t.Error("file accessed through symlink to outside should be denied")
		}
	})
}
