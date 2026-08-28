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

	
	err = tools.CheckPathAllowed(outsideDir)
	if err == nil {
		t.Fatal("expected outside dir to be blocked initially")
	}

	
	ctrl.GrantPath(outsideDir)

	
	err = tools.CheckPathAllowed(outsideDir)
	if err != nil {
		t.Errorf("expected outside dir to be allowed after grant, got: %v", err)
	}
}

func TestCheckPathAccessReadOnlyNeverAsks(t *testing.T) {
	allowedDir := t.TempDir()
	outsideDir := t.TempDir()

	ctrl := access.NewController([]string{allowedDir})
	tools.SetAccessController(ctrl)
	defer tools.SetAccessController(nil)

	oldWD := tools.WorkingDir
	tools.WorkingDir = allowedDir
	defer func() { tools.WorkingDir = oldWD }()

	asked := 0
	tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		asked++
		return map[string]interface{}{"selected": []interface{}{"Allow"}}, nil
	})
	defer tools.SetQuestionCallback(nil)

	ex := &agentToolExecutor{
		agent: &agentImpl{thinkingCallback: func(int64, string) error { return nil }},
	}

	for _, toolName := range []string{"file_read", "dir_list", "glob", "search_code"} {
		if !ex.checkPathAccess(context.Background(), toolName, map[string]string{"path": outsideDir}, 0) {
			t.Errorf("%s should be allowed without asking on read-only access", toolName)
		}
	}
	if asked != 0 {
		t.Errorf("expected no permission questions for read tools, got %d", asked)
	}
}

func TestCheckPathAccessOneTimeAllowDoesNotPersist(t *testing.T) {
	run := func(answer string, expectPersisted bool) {
		outsideDir := t.TempDir()
		ctrl := access.NewController(nil)
		tools.SetAccessController(ctrl)
		defer tools.SetAccessController(nil)

		oldWD := tools.WorkingDir
		tools.WorkingDir = outsideDir
		defer func() { tools.WorkingDir = oldWD }()

		askCount := 0
		tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
			askCount++
			return map[string]interface{}{"selected": []interface{}{answer}}, nil
		})
		defer tools.SetQuestionCallback(nil)

		ex := &agentToolExecutor{
			agent: &agentImpl{thinkingCallback: func(int64, string) error { return nil }},
		}

		target := outsideDir + "/new.txt"
		if !ex.checkPathAccess(context.Background(), "file_write", map[string]string{"path": target}, 0) {
			t.Fatalf("expected tool call allowed after answer %q", answer)
		}
		errAfterGrant := tools.CheckPathAllowed(target + "-sibling")

		if expectPersisted && errAfterGrant != nil {
			t.Errorf("answer %q should persist session grant: %v", answer, errAfterGrant)
		}
		if !expectPersisted && errAfterGrant == nil {
			t.Errorf("one-time Allow must not persist grants")
		}

		if !ex.checkPathAccess(context.Background(), "file_write", map[string]string{"path": target}, 0) {
			t.Fatalf("second call should proceed (ask again or allowed via grant)")
		}
		expectedAsks := 2
		if expectPersisted {
			expectedAsks = 1
		}
		if askCount != expectedAsks {
			t.Errorf("answer %q: expected %d questions, got %d", answer, expectedAsks, askCount)
		}
	}

	run("Allow", false)
	run("Allow always", true)
}

func TestCheckPathAccessAlwaysAllowGrantsDirectoryTree(t *testing.T) {
	baseDir := t.TempDir()
	ctrl := access.NewController(nil)
	tools.SetAccessController(ctrl)
	defer tools.SetAccessController(nil)

	oldWD := tools.WorkingDir
	tools.WorkingDir = baseDir
	defer func() { tools.WorkingDir = oldWD }()

	tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"selected": []interface{}{"Allow always"}}, nil
	})
	defer tools.SetQuestionCallback(nil)

	ex := &agentToolExecutor{
		agent: &agentImpl{thinkingCallback: func(int64, string) error { return nil }},
	}
	peerID := int64(42)

	if err := os.MkdirAll(baseDir+"/sub", 0o755); err != nil {
		t.Fatalf("failed to create sub dir: %v", err)
	}

	filePath := baseDir + "/sub/file.txt"
	if !ex.checkPathAccess(context.Background(), "file_write", map[string]string{"path": filePath}, peerID) {
		t.Fatal("expected tool call allowed after Allow always")
	}

	for _, sibling := range []string{
		baseDir + "/sub/other.txt",
		baseDir + "/sub/deep/nested.txt",
	} {
		if err := tools.CheckPathAllowed(sibling); err != nil {
			t.Errorf("expected %s allowed within granted directory tree: %v", sibling, err)
		}
		if !tools.IsPathGranted(peerID, sibling) {
			t.Errorf("expected peer grant to cover %s inside the path", sibling)
		}
	}

	if err := tools.CheckPathAllowed(baseDir + "/top.txt"); err == nil {
		t.Error("expected baseDir/top.txt to remain outside the granted sub directory")
	}
}

func TestCheckPathAccessOneTimeAllowDoesNotGrantPeer(t *testing.T) {
	baseDir := t.TempDir()
	ctrl := access.NewController(nil)
	tools.SetAccessController(ctrl)
	defer tools.SetAccessController(nil)

	oldWD := tools.WorkingDir
	tools.WorkingDir = baseDir
	defer func() { tools.WorkingDir = oldWD }()

	tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"selected": []interface{}{"Allow"}}, nil
	})
	defer tools.SetQuestionCallback(nil)

	ex := &agentToolExecutor{
		agent: &agentImpl{thinkingCallback: func(int64, string) error { return nil }},
	}
	peerID := int64(7)

	filePath := baseDir + "/once.txt"
	if !ex.checkPathAccess(context.Background(), "file_write", map[string]string{"path": filePath}, peerID) {
		t.Fatal("expected one-time allow to proceed")
	}
	if tools.IsPathGranted(peerID, baseDir+"/once.txt") {
		t.Error("one-time Allow must not store a persistent peer grant")
	}
}
