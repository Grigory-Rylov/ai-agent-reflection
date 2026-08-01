package agent

import (
	"context"
	"testing"

	"github.com/opencode/llama-client/pkg/permission"
	"github.com/opencode/llama-client/pkg/tools"
)

// rulesetChecker implements permissionChecker backed by a real Ruleset.
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
		"command": "curl http://example.com",
	}, 12345)
	if !result {
		t.Error("expected allow after user approved one-time")
	}
	if !called {
		t.Error("expected question callback to be invoked")
	}
}

func TestCheckShellPermissionDeniesWhenUserRejects(t *testing.T) {
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
		"command": "curl http://example.com",
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
		"command": "npm run dev",
	}, 12345)

	found := false
	for _, rule := range *checker.P {
		if rule.Pattern == "npm run dev *" && rule.Action == permission.Allow {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'npm run dev *' allow rule, got %v", *checker.P)
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
