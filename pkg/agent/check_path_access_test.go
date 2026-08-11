package agent

import (
	"context"
	"os"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/access"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

func TestFileToolPaths(t *testing.T) {
	t.Run("returns path for file_read", func(t *testing.T) {
		paths := tools.FileToolPaths("file_read", map[string]string{"path": "/tmp/test.txt"})
		if len(paths) != 1 || paths[0] != "/tmp/test.txt" {
			t.Errorf("expected [/tmp/test.txt], got %v", paths)
		}
	})

	t.Run("returns empty for tools without paths", func(t *testing.T) {
		paths := tools.FileToolPaths("calc", map[string]string{"expression": "2+2"})
		if len(paths) != 0 {
			t.Errorf("expected no paths, got %v", paths)
		}
	})

	t.Run("returns default dot for search_code without path", func(t *testing.T) {
		paths := tools.FileToolPaths("search_code", map[string]string{"pattern": "func"})
		if len(paths) != 1 || paths[0] != "." {
			t.Errorf("expected [.], got %v", paths)
		}
	})
}

func TestResolveToolPath(t *testing.T) {
	t.Run("resolves absolute path unchanged", func(t *testing.T) {
		path, err := resolveToolPath("/absolute/path/file.txt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if path != "/absolute/path/file.txt" {
			t.Errorf("expected /absolute/path/file.txt, got %s", path)
		}
	})

	t.Run("returns error for empty path", func(t *testing.T) {
		_, err := resolveToolPath("")
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "path is empty" {
			t.Errorf("expected 'path is empty', got: %v", err)
		}
	})

	t.Run("resolves relative path against WorkingDir", func(t *testing.T) {
		oldWD := tools.WorkingDir
		tools.WorkingDir = "/base"
		defer func() { tools.WorkingDir = oldWD }()

		path, err := resolveToolPath("relative/file.txt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if path != "/base/relative/file.txt" {
			t.Errorf("expected /base/relative/file.txt, got %s", path)
		}
	})
}

func TestCheckPathAccess(t *testing.T) {
	allowedDir, err := os.MkdirTemp("", "check_path_access_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(allowedDir)

	ctrl := access.NewController([]string{allowedDir})
	tools.SetAccessController(ctrl)
	defer tools.SetAccessController(nil)

	oldWD := tools.WorkingDir
	tools.WorkingDir = allowedDir
	defer func() { tools.WorkingDir = oldWD }()

	agent := &agentToolExecutor{
		agent: &agentImpl{
			thinkingCallback: func(peerID int64, content string) error {
				return nil
			},
		},
	}

	t.Run("allows path inside allowed dir", func(t *testing.T) {
		result := agent.checkPathAccess(context.Background(), "file_read",
			map[string]string{"path": allowedDir}, 0)
		if !result {
			t.Error("expected allowed for path inside allowed dir")
		}
	})

	t.Run("allows path outside allowed dir when no question callback", func(t *testing.T) {
		result := agent.checkPathAccess(context.Background(), "file_read",
			map[string]string{"path": "/etc"}, 0)
		if !result {
			t.Error("expected allowed (fallback) when no question callback set")
		}
	})

	t.Run("allows tools without paths", func(t *testing.T) {
		result := agent.checkPathAccess(context.Background(), "calc",
			map[string]string{"expression": "2+2"}, 0)
		if !result {
			t.Error("expected allowed for tool without paths")
		}
	})

	t.Run("allows when controller is nil", func(t *testing.T) {
		tools.SetAccessController(nil)
		defer tools.SetAccessController(ctrl)

		result := agent.checkPathAccess(context.Background(), "file_read",
			map[string]string{"path": "/any/path"}, 0)
		if !result {
			t.Error("expected allowed when controller is nil")
		}
	})
}

func TestCheckPathAccessGrantsAccess(t *testing.T) {
	allowedDir, err := os.MkdirTemp("", "check_path_grant_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(allowedDir)

	outsideDir, err := os.MkdirTemp("", "check_path_outside_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(outsideDir)

	ctrl := access.NewController([]string{allowedDir})
	tools.SetAccessController(ctrl)
	defer tools.SetAccessController(nil)

	oldWD := tools.WorkingDir
	tools.WorkingDir = allowedDir
	defer func() { tools.WorkingDir = oldWD }()

	// Ensure outside dir is blocked initially
	err = tools.CheckPathAllowed(outsideDir)
	if err == nil {
		t.Fatal("expected outside dir to be blocked initially")
	}

	// Grant access via the controller
	ctrl.GrantPath(outsideDir)

	// Now it should be allowed
	err = tools.CheckPathAllowed(outsideDir)
	if err != nil {
		t.Errorf("expected outside dir to be allowed after grant, got: %v", err)
	}
}
