package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/access"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/permission"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)


type rulesetChecker struct {
	P *permission.Ruleset
}

func (r *rulesetChecker) Check(toolName string) string {
	return string(permission.Evaluate(toolName, "*", *r.P).Action)
}

func (r *rulesetChecker) Evaluate(name, pattern string) string {
	return string(permission.Evaluate(name, pattern, *r.P).Action)
}

func (r *rulesetChecker) Approve(name, pattern string) {
	rs := permission.Merge(*r.P, permission.Ruleset{
		{Permission: name, Pattern: pattern, Action: permission.Allow},
	})
	r.P = &rs
}

func newShellTestAgent(rules permission.Ruleset) *agentImpl {
	config := DefaultConfig()
	config.AgentName = "test-agent"
	a := NewAgent(config)
	a.SetPermissionChecker(&rulesetChecker{P: &rules})
	return a
}

func withQuestionCallback(t *testing.T, cb func(peerID int64, q map[string]interface{}) (map[string]interface{}, error)) {
	t.Helper()
	tools.SetQuestionCallback(cb)
	t.Cleanup(func() { tools.SetQuestionCallback(nil) })
}

func TestCheckShellPermissionAllowsMatching(t *testing.T) {
	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "ls *", Action: permission.Allow},
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "ls -la",
	}, 12345)
	if !result {
		t.Error("expected allow for ls command matching 'ls *' allow rule")
	}
}

func TestCheckShellPermissionDeniesMatching(t *testing.T) {
	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Allow},
		{Permission: "bash", Pattern: "rm *", Action: permission.Deny},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "rm -rf /tmp/x",
	}, 12345)
	if result {
		t.Error("expected deny for rm command matching 'rm *' deny rule")
	}
}

func TestCheckShellPermissionAsksForUnmatched(t *testing.T) {
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
		return map[string]interface{}{
			"selected": []interface{}{"✅ Allow"},
		}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "cat /etc/passwd",
	}, 12345)
	if !result {
		t.Error("expected allow after user approved one-time")
	}
	if !called {
		t.Error("expected question callback to be invoked")
	}
}

func TestCheckShellPermissionDeniesWhenUserRejects(t *testing.T) {
	dir := t.TempDir()
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(dir)
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{
			"selected": []interface{}{"❌ Deny"},
		}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "cat /etc/passwd",
	}, 12345)
	if result {
		t.Error("expected deny after user rejected")
	}
}

func TestCheckShellPermissionAlwaysAllowPersistsPrefix(t *testing.T) {
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{
			"selected": []interface{}{"✅ Always allow"},
		}, nil
	})

	rules := permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	}
	a := newShellTestAgent(rules)
	e := newAgentToolExecutor(a)

	first := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "git log --oneline",
	}, 12345)
	if !first {
		t.Fatal("expected first call allowed after always-allow")
	}

	second := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "git log --stat",
	}, 12345)
	if !second {
		t.Error("expected 'git log *' always rule to cover similar git log commands")
	}

	third := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "git status",
	}, 12345)
	if !third {
		t.Error("expected 'git status *' not to be blocked after always-allow of git log")
	}
}

func TestCheckShellPermissionCdOnly(t *testing.T) {
	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "cd /tmp",
	}, 12345)
	if !result {
		t.Error("expected cd-only command to be allowed without asking")
	}
}

func TestCheckShellPermissionAlwaysUsesPrefixOnly(t *testing.T) {
	dir := t.TempDir()
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(dir)
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{
			"selected": []interface{}{"✅ Always allow"},
		}, nil
	})

	rules := permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	}
	checker := &rulesetChecker{P: &rules}
	a := NewAgent(DefaultConfig())
	a.SetPermissionChecker(checker)
	e := newAgentToolExecutor(a)

	_ = e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "cat /etc/passwd",
	}, 12345)

	found := false
	for _, rule := range *checker.P {
		if rule.Pattern == "cat *" && rule.Action == permission.Allow {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'cat *' allow rule, got %v", *checker.P)
	}
}

func TestCheckShellPermissionNestedCommand(t *testing.T) {
	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "cat *", Action: permission.Deny},
		{Permission: "bash", Pattern: "echo *", Action: permission.Allow},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": `echo $(cat /etc/passwd)`,
	}, 12345)
	if result {
		t.Error("expected deny when nested cat matches 'cat *' deny rule")
	}
}

func TestCheckShellPermissionNoQuestionWhenAllAllowed(t *testing.T) {
	called := false
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		called = true
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Allow},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "echo hi",
	}, 12345)
	if !result {
		t.Error("expected allow for wildcard allow rule")
	}
	if called {
		t.Error("expected no question when all commands are allowed")
	}
}

func TestCheckShellPermissionDeniedSubcommand(t *testing.T) {
	a := newShellTestAgent(permission.Ruleset{
		{Permission: "bash", Pattern: "rm *", Action: permission.Deny},
	})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "rm foo.txt && echo done",
	}, 12345)
	if result {
		t.Error("expected deny when any subcommand matches deny rule")
	}
}

func TestCheckShellPermissionSkipsAskForPathlessWhenEnabled(t *testing.T) {
	called := false
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		called = true
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	config := DefaultConfig()
	config.SkipShellPermissionForPathless = true
	a := NewAgent(config)
	a.SetPermissionChecker(&rulesetChecker{P: &permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	}})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "adb -s emulator-5554 devices -l",
	}, 12345)
	if !result {
		t.Error("expected pathless command allowed without asking")
	}
	if called {
		t.Error("expected no question for pathless command when flag enabled")
	}
}

func TestCheckShellPermissionStillAsksForFileCommandWhenEnabled(t *testing.T) {
	called := false
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		called = true
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	dir := t.TempDir()
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() { tools.SetAccessController(nil) })

	config := DefaultConfig()
	config.SkipShellPermissionForPathless = true
	a := NewAgent(config)
	a.SetPermissionChecker(&rulesetChecker{P: &permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	}})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "echo hi > /etc/file",
	}, 12345)
	if !result {
		t.Error("expected command with redirect outside dir to be asked and approved")
	}
	if !called {
		t.Error("expected question for command with file path")
	}
}

func TestCheckShellPermissionDenyStillBlocksWhenFlagEnabled(t *testing.T) {
	config := DefaultConfig()
	config.SkipShellPermissionForPathless = true
	a := NewAgent(config)
	a.SetPermissionChecker(&rulesetChecker{P: &permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Deny},
	}})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "adb devices",
	}, 12345)
	if result {
		t.Error("expected explicit deny to block even pathless command")
	}
}

func TestCheckShellPermissionPathlessSkipsAsk(t *testing.T) {
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
		"command": "adb devices",
	}, 12345)
	if !result {
		t.Error("expected allow for filesystem-safe command")
	}
	if called {
		t.Error("expected NO question for filesystem-safe command (no paths outside allowed dirs)")
	}
}

func TestCheckShellPermissionSkipsAskForADBDeviceCommandWhenEnabled(t *testing.T) {
	called := false
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		called = true
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	config := DefaultConfig()
	config.SkipShellPermissionForPathless = true
	a := NewAgent(config)
	a.SetPermissionChecker(&rulesetChecker{P: &permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	}})
	e := newAgentToolExecutor(a)

	cmd := "adb -s emulator-5554 shell uiautomator dump /data/local/tmp/ui.xml 2>&1 && cat /data/local/tmp/ui.xml && head -30"
	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": cmd,
	}, 12345)
	if !result {
		t.Error("expected adb device command allowed without asking")
	}
	if called {
		t.Error("expected no question: device paths are not host file operations")
	}
}

func TestCheckShellPermissionStillAsksForADBHostPushOutsideWhenEnabled(t *testing.T) {
	called := false
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		called = true
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	dir := t.TempDir()
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() { tools.SetAccessController(nil) })

	config := DefaultConfig()
	config.SkipShellPermissionForPathless = true
	a := NewAgent(config)
	a.SetPermissionChecker(&rulesetChecker{P: &permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	}})
	e := newAgentToolExecutor(a)

	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": "adb push /etc/passwd /sdcard/",
	}, 12345)
	if !result {
		t.Error("expected host push to be asked and approved")
	}
	if !called {
		t.Error("expected question: host source of push is outside allowed dir")
	}
}

func TestCheckShellPermissionSkipsAskForADBPullChainWhenEnabled(t *testing.T) {
	called := false
	withQuestionCallback(t, func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		called = true
		return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
	})

	dir := t.TempDir()
	prevWD := tools.WorkingDir
	tools.SetWorkingDir(dir)
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		tools.SetAccessController(nil)
		tools.SetWorkingDir(prevWD)
	})

	config := DefaultConfig()
	config.SkipShellPermissionForPathless = true
	a := NewAgent(config)
	a.SetPermissionChecker(&rulesetChecker{P: &permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	}})
	e := newAgentToolExecutor(a)

	cmd := "adb -s emulator-5554 shell uiautomator dump /data/local/tmp/ui.xml && sleep 1 && adb -s emulator-5554 pull /data/local/tmp/ui.xml ./ui_test.xml 2>&1 && head -30 ui_test.xml"
	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": cmd,
	}, 12345)
	if !result {
		t.Error("expected adb pull chain allowed without asking")
	}
	if called {
		t.Error("expected no question: only device paths and cwd host files")
	}
}

func TestAskShellPermissionQuestionTruncatesLongCommand(t *testing.T) {
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

	config := DefaultConfig()
	config.SkipShellPermissionForPathless = false
	a := NewAgent(config)
	a.SetPermissionChecker(&rulesetChecker{P: &permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	}})
	e := newAgentToolExecutor(a)

	longCmd := "cat > /outside/file.py << 'PYEOF'\n" + strings.Repeat("code line\n", 500) + "PYEOF"
	result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
		"command": longCmd,
	}, 12345)
	if !result {
		t.Error("expected shell command approved after user choice")
	}
	if !strings.Contains(gotQuestion, "Allow shell command:") {
		t.Errorf("expected permission question, got %q", gotQuestion)
	}
	if len(gotQuestion) > 300 {
		t.Errorf("permission question too long (%d chars): %q", len(gotQuestion), gotQuestion)
	}
}
