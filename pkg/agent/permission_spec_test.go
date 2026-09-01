package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/access"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/permission"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

func TestSpecReadOnlyFileToolNeverAsks(t *testing.T) {
	dir := t.TempDir()
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(dir)
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	called := false
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		called = true
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "*", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "file_read", map[string]string{
		"path": "/etc/outside.txt",
	}, 424242)

	if !result {
		t.Error("expected file_read outside allowed dirs to be allowed")
	}
	if called {
		t.Error("expected NO question for read-only file tool")
	}
}

func TestSpecFileWriteOutsideAsksThenGrantsParentDir(t *testing.T) {
	allowedDir := t.TempDir()
	outsideDir := t.TempDir()
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(allowedDir)
	tools.SetAccessController(access.NewController([]string{allowedDir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	const peerID int64 = 555555
	target := filepath.Join(outsideDir, "notes.txt")
	sibling := filepath.Join(outsideDir, "sibling.txt")

	asks := 0
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		asks++
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "*", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	first := e.checkPermissionAsk(context.Background(), "file_write", map[string]string{
		"path":    target,
		"content": "hi",
	}, peerID)
	if !first {
		t.Fatal("expected file_write to be allowed after user answered Allow")
	}
	if asks != 1 {
		t.Fatalf("expected exactly 1 question, got %d", asks)
	}
	if !tools.IsPathGranted(peerID, target) {
		t.Error("expected path to be granted after Allow")
	}

	again := e.checkPermissionAsk(context.Background(), "file_write", map[string]string{
		"path":    target,
		"content": "hi again",
	}, peerID)
	if !again {
		t.Error("expected repeat file_write on same path to be allowed without question")
	}

	next := e.checkPermissionAsk(context.Background(), "file_write", map[string]string{
		"path":    sibling,
		"content": "sibling",
	}, peerID)
	if !next {
		t.Error("expected file_write on sibling file in granted dir to be allowed without question")
	}
	if asks != 1 {
		t.Errorf("expected no additional questions, total asks = %d", asks)
	}
}

func TestSpecShellReadOnlyCatOutsideDoesNotAsk(t *testing.T) {
	dir := t.TempDir()
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(dir)
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	called := false
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		called = true
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "cat /etc/outside.txt",
	}, 666666)

	if !result {
		t.Error("expected read-only cat outside allowed dirs to be allowed")
	}
	if called {
		t.Error("expected NO question for read-only shell command")
	}
}

func TestSpecShellTrReadingProcEnvironDoesNotAsk(t *testing.T) {
	dir := t.TempDir()
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(dir)
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	called := false
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		called = true
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": `tr '\0' '\n' < /proc/34947/environ`,
	}, 666666)

	if !result {
		t.Error("expected read-only tr reading /proc/<pid>/environ to be allowed")
	}
	if called {
		t.Error("expected NO question: stdin redirect is a read, not a write outside allowed dirs")
	}
}

func TestSpecShellTeeOutsideAsksOnceThenGrantCoversRepeat(t *testing.T) {
	allowedDir := t.TempDir()
	outsideDir := t.TempDir()
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(allowedDir)
	tools.SetAccessController(access.NewController([]string{allowedDir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	const peerID int64 = 777777
	cmd := "tee " + filepath.Join(outsideDir, "file.txt")

	asks := 0
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		asks++
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	first := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": cmd,
	}, peerID)
	if !first {
		t.Fatal("expected tee command to be allowed after user answered Allow")
	}
	if asks != 1 {
		t.Fatalf("expected exactly 1 question, got %d", asks)
	}

	repeat := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": cmd,
	}, peerID)
	if !repeat {
		t.Error("expected repeat tee command to be allowed without question (dir grant)")
	}
	if asks != 1 {
		t.Errorf("expected no additional questions, total asks = %d", asks)
	}
}

func TestSpecWriteThroughSymlinkGrantsRealDir(t *testing.T) {
	allowedDir := t.TempDir()
	realDir := t.TempDir()
	linkDir := filepath.Join(realDir, "link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(allowedDir)
	tools.SetAccessController(access.NewController([]string{allowedDir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	const peerID int64 = 888888
	newViaLink := filepath.Join(linkDir, "new.txt")
	otherViaLink := filepath.Join(linkDir, "other.txt")

	asks := 0
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		asks++
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	write := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "touch " + newViaLink,
	}, peerID)
	if !write {
		t.Fatal("expected touch through symlink to be allowed after Allow")
	}
	if asks != 1 {
		t.Fatalf("expected exactly 1 question, got %d", asks)
	}

	read := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "cat " + otherViaLink,
	}, peerID)
	if !read {
		t.Error("expected cat of neighbor file through symlink to be allowed without question")
	}
	if asks != 1 {
		t.Errorf("expected no additional questions, total asks = %d", asks)
	}
}
