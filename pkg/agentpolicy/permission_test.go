package agentpolicy

import (
	"testing"

	"github.com/opencode/llama-client/pkg/permission"
)

func TestPermissionAdapterEvaluate(t *testing.T) {
	adapter := NewPermissionAdapter(Permission{
		"bash":          "ask",
		"file_write":    "ask",
		"shell_execute": "ask",
	})

	if got := adapter.Check("file_write"); got != "ask" {
		t.Errorf("Check(file_write) = %q, want ask", got)
	}
	if got := adapter.Check("bash"); got != "ask" {
		t.Errorf("Check(bash) = %q, want ask", got)
	}
	if got := adapter.Check("unknown_tool"); got != "allow" {
		t.Errorf("Check(unknown_tool) = %q, want allow (from * default)", got)
	}
}

func TestPermissionAdapterEvaluateBash(t *testing.T) {
	adapter := NewRulePermissionAdapter(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
		{Permission: "bash", Pattern: "git *", Action: permission.Allow},
	})

	if got := adapter.Evaluate("bash", "git status"); got != "allow" {
		t.Errorf("Evaluate(bash, git status) = %q, want allow", got)
	}
	if got := adapter.Evaluate("bash", "rm file"); got != "ask" {
		t.Errorf("Evaluate(bash, rm file) = %q, want ask", got)
	}
}

func TestPermissionAdapterApprove(t *testing.T) {
	adapter := NewRulePermissionAdapter(permission.Ruleset{
		{Permission: "bash", Pattern: "*", Action: permission.Ask},
	})

	if got := adapter.Evaluate("bash", "ls -la"); got != "ask" {
		t.Fatalf("Evaluate before approve = %q, want ask", got)
	}

	adapter.Approve("bash", "ls *")

	if got := adapter.Evaluate("bash", "ls -la"); got != "allow" {
		t.Errorf("Evaluate after approve = %q, want allow", got)
	}
	if got := adapter.Evaluate("bash", "cat file"); got != "ask" {
		t.Errorf("Evaluate unrelated command = %q, want ask", got)
	}
}

func TestToRulesetSkipsWildcardKey(t *testing.T) {
	p := Permission{
		"*":          "allow",
		"bash":       "ask",
		"file_write": "deny",
	}
	rs := toRuleset(p)
	if rs == nil {
		t.Fatal("expected non-nil ruleset")
	}
	for _, rule := range *rs {
		if rule.Permission == "*" {
			t.Error("wildcard key should be skipped in ruleset")
		}
	}
	if got := permission.Evaluate("bash", "*", *rs).Action; got != permission.Ask {
		t.Errorf("Evaluate(bash) = %q, want ask (no bash allow override)", got)
	}
	if got := permission.Evaluate("file_write", "*", *rs).Action; got != permission.Deny {
		t.Errorf("Evaluate(file_write) = %q, want deny", got)
	}
}

func TestToRulesetNilForWildcardOnly(t *testing.T) {
	if rs := toRuleset(Permission{"*": "allow"}); rs != nil {
		t.Errorf("expected nil ruleset for wildcard-only permission, got %v", *rs)
	}
}
