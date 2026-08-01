package agentpolicy

import (
	"encoding/json"
	"testing"

	"github.com/opencode/llama-client/pkg/permission"
)

func TestPermissionCheck(t *testing.T) {
	tests := []struct {
		name    string
		perm    Permission
		tool    string
		want    bool
		wantAct string
	}{
		{
			name:    "wildcard allow",
			perm:    Permission{"*": "allow"},
			tool:    "write",
			want:    true,
			wantAct: "allow",
		},
		{
			name:    "explicit deny",
			perm:    Permission{"edit": "deny", "*": "allow"},
			tool:    "edit",
			want:    false,
			wantAct: "deny",
		},
		{
			name:    "other tool allowed",
			perm:    Permission{"edit": "deny", "*": "allow"},
			tool:    "read",
			want:    true,
			wantAct: "allow",
		},
		{
			name:    "wildcard deny with explicit allow",
			perm:    Permission{"*": "deny", "read": "allow"},
			tool:    "read",
			want:    true,
			wantAct: "allow",
		},
		{
			name:    "ask action",
			perm:    Permission{"bash": "ask", "*": "allow"},
			tool:    "bash",
			want:    true,
			wantAct: "ask",
		},
		{
			name:    "empty permission defaults allow",
			perm:    Permission{},
			tool:    "anything",
			want:    true,
			wantAct: "allow",
		},
		{
			name:    "nil permission",
			perm:    nil,
			tool:    "anything",
			want:    true,
			wantAct: "allow",
		},
		{
			name:    "glob pattern deny file_*",
			perm:    Permission{"file_*": "deny"},
			tool:    "file_write",
			want:    false,
			wantAct: "deny",
		},
		{
			name:    "glob suffix pattern *.md",
			perm:    Permission{"*.md": "deny"},
			tool:    "readme.md",
			want:    false,
			wantAct: "deny",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.perm.Check(tt.tool)
			if got != tt.want {
				t.Errorf("Check(%q) = %v, want %v", tt.tool, got, tt.want)
			}
			gotAct := tt.perm.GetAction(tt.tool)
			if gotAct != tt.wantAct {
				t.Errorf("GetAction(%q) = %q, want %q", tt.tool, gotAct, tt.wantAct)
			}
		})
	}
}

func TestDefaultPermission(t *testing.T) {
	p := DefaultPermission()
	if p.Check("anything") != true {
		t.Error("DefaultPermission should allow everything")
	}
	if p.GetAction("anything") != "allow" {
		t.Error("DefaultPermission GetAction should return allow")
	}
}

func TestMergePermissions(t *testing.T) {
	base := Permission{"*": "allow", "edit": "deny"}
	override := Permission{"edit": "ask"}

	merged := MergePermissions(base, override)

	if merged.GetAction("edit") != "ask" {
		t.Errorf("override should win: got %q, want %q", merged.GetAction("edit"), "ask")
	}
	if merged.GetAction("read") != "allow" {
		t.Errorf("base allow should remain: got %q", merged.GetAction("read"))
	}

	override2 := Permission{"read": "deny"}
	merged2 := MergePermissions(base, override2)
	if merged2.GetAction("read") != "deny" {
		t.Errorf("override deny should win: got %q", merged2.GetAction("read"))
	}
}

func TestNewPermissionFromConfig(t *testing.T) {
	cfg := map[string]string{
		"write": "deny",
		"edit":  "deny",
		"read":  "allow",
	}
	p := NewPermissionFromConfig(cfg)

	if p.GetAction("write") != "deny" {
		t.Errorf("write: got %q, want deny", p.GetAction("write"))
	}
	if p.GetAction("edit") != "deny" {
		t.Errorf("edit: got %q, want deny", p.GetAction("edit"))
	}
	if p.GetAction("read") != "allow" {
		t.Errorf("read: got %q, want allow", p.GetAction("read"))
	}
	if p.GetAction("bash") != "allow" {
		t.Errorf("bash (unspecified): got %q, want allow", p.GetAction("bash"))
	}
}

func TestPermissionJSONUnmarshal(t *testing.T) {
	input := `{
		"reviewer": {
			"mode": "subagent",
			"permission": {
				"write": "deny",
				"edit": "deny"
			}
		}
	}`
	var cfg struct {
		Reviewer AgentCfg `json:"reviewer"`
	}
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Reviewer.Permission == nil {
		t.Fatal("permission should not be nil")
	}
	if cfg.Reviewer.Permission.GetAction("write") != "deny" {
		t.Errorf("write: got %q, want deny", cfg.Reviewer.Permission.GetAction("write"))
	}
	if cfg.Reviewer.Permission.GetAction("edit") != "deny" {
		t.Errorf("edit: got %q, want deny", cfg.Reviewer.Permission.GetAction("edit"))
	}
	if cfg.Reviewer.Permission.GetAction("read") != "allow" {
		t.Errorf("read (unspecified): got %q, want allow", cfg.Reviewer.Permission.GetAction("read"))
	}
}

func TestPermissionEmptyUnmarshal(t *testing.T) {
	input := `{
		"agent": {
			"mode": "subagent"
		}
	}`
	var cfg struct {
		Agent AgentCfg `json:"agent"`
	}
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Agent.Permission != nil {
		t.Log("permission is nil when not specified (expected)")
	}
}

func TestPermissionAdapter(t *testing.T) {
	t.Run("returns allow for nil", func(t *testing.T) {
		adapter := NewPermissionAdapter(nil)
		if adapter.Check("anything") != "allow" {
			t.Error("nil adapter should return allow")
		}
	})

	t.Run("returns allow for empty", func(t *testing.T) {
		adapter := NewPermissionAdapter(Permission{})
		if adapter.Check("anything") != "allow" {
			t.Error("empty adapter should return allow")
		}
	})

	t.Run("delegates to GetAction", func(t *testing.T) {
		perm := Permission{"edit": "deny", "file_write": "deny", "*": "allow"}
		adapter := NewPermissionAdapter(perm)

		if adapter.Check("edit") != "deny" {
			t.Errorf("edit: got %q, want deny", adapter.Check("edit"))
		}
		if adapter.Check("file_write") != "deny" {
			t.Errorf("file_write: got %q, want deny", adapter.Check("file_write"))
		}
		if adapter.Check("file_read") != "allow" {
			t.Errorf("file_read: got %q, want allow", adapter.Check("file_read"))
		}
		if adapter.Check("shell_execute") != "allow" {
			t.Errorf("shell_execute: got %q, want allow", adapter.Check("shell_execute"))
		}
	})
}

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
