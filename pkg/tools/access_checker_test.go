package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencode/llama-client/pkg/access"
)

func setupAccessTest(t *testing.T) (string, *access.Controller) {
	dir, err := os.MkdirTemp("", "access_tools_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	ctrl := access.NewController([]string{dir})
	SetAccessController(ctrl)
	return dir, ctrl
}

func cleanupAccessTest(t *testing.T, dir string) {
	SetAccessController(nil)
	os.RemoveAll(dir)
}

func TestCheckPathAllowed(t *testing.T) {
	t.Run("allows path inside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		err := CheckPathAllowed(filepath.Join(dir, "test.txt"))
		if err != nil {
			t.Errorf("expected allowed, got: %v", err)
		}
	})

	t.Run("blocks path outside allowed dir", func(t *testing.T) {
		_, ctrl := setupAccessTest(t)
		defer func() { SetAccessController(nil) }()

		otherDir, err := os.MkdirTemp("", "outside_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(otherDir)

		err = CheckPathAllowed(filepath.Join(otherDir, "secret.txt"))
		if err == nil {
			t.Error("expected error for path outside allowed dir")
		}
		_ = ctrl
	})

	t.Run("allows nil controller", func(t *testing.T) {
		SetAccessController(nil)
		err := CheckPathAllowed("/any/path")
		if err != nil {
			t.Errorf("expected no error with nil controller, got: %v", err)
		}
	})
}

func TestResolvePathWithAccessControl(t *testing.T) {
	t.Run("resolvePath blocks path outside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		otherDir, err := os.MkdirTemp("", "outside_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(otherDir)

		_, err = resolvePath(otherDir)
		if err == nil {
			t.Error("resolvePath should block path outside allowed dir")
		}
	})

	t.Run("resolvePath allows path inside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		_, err := resolvePath(dir)
		if err != nil {
			t.Errorf("resolvePath should allow path inside allowed dir, got: %v", err)
		}
	})

	t.Run("resolvePath allows relative path inside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		oldWorkingDir := WorkingDir
		WorkingDir = dir
		defer func() { WorkingDir = oldWorkingDir }()

		_, err := resolvePath("subdir/file.txt")
		if err != nil {
			t.Errorf("resolvePath should allow relative path, got: %v", err)
		}
	})

	t.Run("resolvePath works with nil controller", func(t *testing.T) {
		SetAccessController(nil)

		_, err := resolvePath("/tmp")
		if err != nil {
			t.Errorf("resolvePath should work with nil controller, got: %v", err)
		}
	})
}

func TestFileToolsWithAccessControl(t *testing.T) {
	t.Run("FileReadTool blocks read outside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		tool := &FileReadTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"path": "/etc/passwd",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("expected failure for read outside allowed dir")
		}
		if result.Error == "" {
			t.Error("expected error message")
		}
	})

	t.Run("FileReadTool allows read inside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		testFile := filepath.Join(dir, "test.txt")
		if err := os.WriteFile(testFile, []byte("hello"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		tool := &FileReadTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"path": testFile,
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Errorf("expected success, got: %s", result.Error)
		}
	})

	t.Run("FileWriteTool blocks write outside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		tool := &FileWriteTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"path":    "/etc/evil.sh",
			"content": "rm -rf /",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("expected failure for write outside allowed dir")
		}
	})

	t.Run("FileWriteTool allows write inside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		tool := &FileWriteTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"path":    filepath.Join(dir, "output.txt"),
			"content": "test",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Errorf("expected success, got: %s", result.Error)
		}
	})

	t.Run("EditTool blocks edit outside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		tool := &EditTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"path":       "/etc/config",
			"old_string": "old",
			"new_string": "new",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("expected failure for edit outside allowed dir")
		}
	})

	t.Run("DirListTool blocks list outside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		tool := &DirListTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"path": "/etc",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("expected failure for list outside allowed dir")
		}
	})

	t.Run("DirListTool allows list inside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		tool := &DirListTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"path": dir,
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Errorf("expected success, got: %s", result.Error)
		}
	})
}

func TestCheckToolArgs(t *testing.T) {
	t.Run("returns nil for tools without paths", func(t *testing.T) {
		err := CheckToolArgs("calc", map[string]string{
			"expression": "2+2",
		})
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("returns nil when no controller set", func(t *testing.T) {
		SetAccessController(nil)
		err := CheckToolArgs("file_read", map[string]string{
			"path": "/etc/passwd",
		})
		if err != nil {
			t.Errorf("expected no error with nil controller, got: %v", err)
		}
	})
}

func TestToolErrorsUseDeniedMessage(t *testing.T) {
	t.Run("access denied message is clear and actionable", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		err := CheckPathAllowed("/etc/shadow")
		if err == nil {
			t.Fatal("expected error")
		}

		errStr := err.Error()
		if !strings.Contains(errStr, "access denied") && !strings.Contains(errStr, "outside") {
			t.Errorf("error should mention access denial, got: %s", errStr)
		}
	})
}

func TestGlobWithAccessControl(t *testing.T) {
	t.Run("GlobTool blocks path outside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		tool := &GlobTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"pattern": "*",
			"path":    "/etc",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("expected failure for glob outside allowed dir")
		}
	})

	t.Run("GlobTool works inside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		// Create a test file
		if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte("package main"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		tool := &GlobTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"pattern": "*.go",
			"path":    dir,
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Errorf("expected success, got: %s", result.Error)
		}
		data := result.Data.(map[string]interface{})
		if data["count"].(int) != 1 {
			t.Errorf("expected 1 match, got %d", data["count"])
		}
	})

	t.Run("GlobTool with default path works inside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		oldWd := WorkingDir
		WorkingDir = dir
		defer func() { WorkingDir = oldWd }()

		if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("data"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		tool := &GlobTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"pattern": "*.txt",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Errorf("expected success with default path, got: %s", result.Error)
		}
	})
}

func TestSearchCodeWithAccessControl(t *testing.T) {
	t.Run("GrepTool blocks search outside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		tool := &GrepTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"pattern": "root",
			"path":    "/etc",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("expected failure for grep outside allowed dir")
		}
	})

	t.Run("GrepTool works inside allowed dir", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		tool := &GrepTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"pattern": "func",
			"path":    dir,
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Errorf("expected success, got: %s", result.Error)
		}
		data := result.Data.(map[string]interface{})
		if data["count"].(int) < 1 {
			t.Errorf("expected at least 1 match, got %d", data["count"])
		}
	})
}

func TestSessionGrantIntegration(t *testing.T) {
	t.Run("GrantPath allows file tool access", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		outsideDir, err := os.MkdirTemp("", "session_grant_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(outsideDir)

		// Без grant доступ запрещён
		tool := &FileReadTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"path": filepath.Join(outsideDir, "test.txt"),
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("expected failure before grant")
		}

		// Grant доступа
		ctrl := GetAccessController()
		ctrl.GrantPath(outsideDir)

		// Создаём файл в granted директории
		testFile := filepath.Join(outsideDir, "granted.txt")
		if err := os.WriteFile(testFile, []byte("granted"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		// После grant доступ разрешён
		result, err = tool.Execute(context.Background(), map[string]string{
			"path": testFile,
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Errorf("expected success after grant, got: %s", result.Error)
		}
	})
}

func TestMultipleAllowedDirs(t *testing.T) {
	t.Run("multiple dirs all allow file operations", func(t *testing.T) {
		dir1, err := os.MkdirTemp("", "multi_allowed_1_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(dir1)

		dir2, err := os.MkdirTemp("", "multi_allowed_2_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(dir2)

		ctrl := access.NewController([]string{dir1, dir2})
		SetAccessController(ctrl)
		defer SetAccessController(nil)

		// Write to dir1
		tool := &FileWriteTool{}
		result, err := tool.Execute(context.Background(), map[string]string{
			"path":    filepath.Join(dir1, "f1.txt"),
			"content": "file1",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Errorf("write to dir1 should succeed, got: %s", result.Error)
		}

		// Write to dir2
		result, err = tool.Execute(context.Background(), map[string]string{
			"path":    filepath.Join(dir2, "f2.txt"),
			"content": "file2",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !result.Success {
			t.Errorf("write to dir2 should succeed, got: %s", result.Error)
		}

		// Write to third dir should fail
		dir3, err := os.MkdirTemp("", "multi_allowed_3_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(dir3)

		result, err = tool.Execute(context.Background(), map[string]string{
			"path":    filepath.Join(dir3, "f3.txt"),
			"content": "file3",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if result.Success {
			t.Error("write to dir3 (not allowed) should fail")
		}
	})
}
