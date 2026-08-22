package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/access"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/permission"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

func TestShellPermissionNonFileCommandSkipsAsk(t *testing.T) {

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

func TestShellPermissionMutatingOutsideAllowedAsks(t *testing.T) {
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
		"command": "cp /etc/passwd /tmp/stolen",
	}, 12345)
	if !result {
		t.Error("expected allow after user approved")
	}
	if !askCalled {
		t.Error("expected permission ask for mutating command with path outside allowed dirs")
	}
}

func TestShellPermissionGrepInsideCwdSkipsAsk(t *testing.T) {
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

	cmd := `grep -rn '"cat /etc/passwd"' --include='*_test.go' pkg/agent/ && cut -d: -f1,2`
	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": cmd,
	}, 12345)
	if !result {
		t.Error("expected allow for grep inside working dir")
	}
	if askCalled {
		t.Error("expected NO permission ask: command only touches paths inside the working dir")
	}
}

func TestShellPermissionRedirectOutsideAllowedAsks(t *testing.T) {

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

func TestShellPermissionReadOnlyWithExternalPathsSkipsAsk(t *testing.T) {
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
		{"ls external bin", "ls /usr/local/go/bin 2>/dev/null && ls /mnt/data/usr/local/go/bin 2>/dev/null && which -a go 2>/dev/null && echo PATH=$PATH && tr ':' '\\n' && grep -i go"},
		{"ls-la and find external", "ls -la /mnt/data/usr/local/go/bin 2>&1 && ls ~/go/bin 2>/dev/null && which go golang 2>&1 && find /usr -maxdepth 4 -name 'go' -type f 2>/dev/null && head && echo PATH=$PATH"},
		{"ls which cat chain", "ls /usr/local/go/bin/ 2>/dev/null && which go 2>/dev/null && ls ~/go 2>/dev/null && head -3 && cat /home/grishberg/projects/go/ai-agent-reflection/build.sh"},
		{"find root", "find / -maxdepth 4 -name 'go' -type f -path '*bin*' 2>/dev/null && head && ls /mnt/data/usr/local/go/bin 2>/dev/null"},
		{"find executable go", "find /tmp /root /home -maxdepth 4 -name 'go' -type f -executable 2>/dev/null"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			askCalled = false
			result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
				"command": tt.command,
			}, 12345)
			if !result {
				t.Errorf("expected allow for read-only command %q", tt.command)
			}
			if askCalled {
				t.Errorf("expected NO permission ask for read-only command %q", tt.command)
			}
		})
	}
}

func TestShellPermissionPromptShowsOnlyProblematicFragments(t *testing.T) {
	dir := t.TempDir()
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(dir)
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	var gotQuestion string
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		gotQuestion, _ = q["question"].(string)
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	cmd := "ls /usr/local/go/bin && cp /etc/passwd /tmp/stolen"
	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": cmd,
	}, 12345)
	if !result {
		t.Fatal("expected allow after user approved")
	}
	if !strings.Contains(gotQuestion, "cp /etc/passwd /tmp/stolen") {
		t.Errorf("expected question to mention problematic fragment 'cp', got %q", gotQuestion)
	}
	if strings.Contains(gotQuestion, "ls /usr/local/go/bin") {
		t.Errorf("expected question to NOT include safe fragment 'ls', got %q", gotQuestion)
	}
}
