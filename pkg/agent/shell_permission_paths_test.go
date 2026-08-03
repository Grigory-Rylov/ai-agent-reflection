package agent

import (
	"context"
	"testing"

	"github.com/opencode/llama-client/pkg/access"
	"github.com/opencode/llama-client/pkg/permission"
	"github.com/opencode/llama-client/pkg/tools"
)

func TestShellPermissionNonFileCommandSkipsAsk(t *testing.T) {
	// docker, adb, curl — не файловые команды, нет путей вне allowed dirs
	// → не должны запрашивать пермишен
	dir := t.TempDir()
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(dir)
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	var askCalled bool
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		askCalled = true
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	tests := []struct {
		name    string
		command string
	}{
		{"docker ps", "docker ps"},
		{"docker ps && grep pattern", "docker ps && grep android-emulator"},
		{"docker ps && grep glob pattern", "docker ps && grep android-emulator?"},
		{"curl http://example.com", "curl http://example.com"},
		{"echo hello", "echo hello"},
		{"adb devices", "adb devices"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			askCalled = false
			result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
				"command": tt.command,
			}, 12345)
			if !result {
				t.Errorf("expected allow for non-file command %q", tt.command)
			}
			if askCalled {
				t.Errorf("expected NO permission ask for non-file command %q (no paths outside allowed dirs)", tt.command)
			}
		})
	}
}

func TestShellPermissionPathOutsideAllowedAsks(t *testing.T) {
	// cat /etc/passwd — путь вне allowed dirs → должен запрашивать
	dir := t.TempDir()
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(dir)
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	askCalled := false
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		askCalled = true
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "cat /etc/passwd",
	}, 12345)
	if !result {
		t.Error("expected allow after user approved")
	}
	if !askCalled {
		t.Error("expected permission ask for path outside allowed dirs (cat /etc/passwd)")
	}
}

func TestShellPermissionRedirectOutsideAllowedAsks(t *testing.T) {
	// echo hi > /tmp/x — редирекст вне allowed dirs → должен запрашивать
	dir := t.TempDir()
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(dir)
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	askCalled := false
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		askCalled = true
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "echo hi > /tmp/outside",
	}, 12345)
	if !result {
		t.Error("expected allow after user approved")
	}
	if !askCalled {
		t.Error("expected permission ask for redirect outside allowed dirs")
	}
}

func TestShellPermissionFileCommandInsideAllowedSkipsAsk(t *testing.T) {
	// cat ./local.txt — путь внутри allowed dirs → не должен запрашивать
	dir := t.TempDir()
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(dir)
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	var askCalled bool
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		askCalled = true
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "ls -la",
	}, 12345)
	if !result {
		t.Error("expected allow for ls in allowed dir")
	}
	if askCalled {
		t.Error("expected NO permission ask for file command inside allowed dirs")
	}
}
