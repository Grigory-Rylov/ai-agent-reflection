package agentpolicy

import (
	"encoding/json"
	"testing"
)

func TestLoadFromConfig(t *testing.T) {
	cfg := map[string]AgentCfg{
		"developer": {
			Mode:        "subagent",
			Description: "Developer, writes and implements code",
		},
		"reviewer": {
			Mode:        "subagent",
			Description: "Code reviewer",
			Leaf:        true,
			Review:      true,
			Permission:  NewPermissionFromConfig(map[string]string{"file_write": "deny", "edit": "deny", "apply_patch": "deny"}),
		},
		"qa": {
			Mode:        "subagent",
			Description: "QA engineer, writes and runs tests",
			Leaf:        true,
		},
	}

	am := NewAgentManager()
	am.LoadFromConfig(cfg)

	t.Run("developer has full access", func(t *testing.T) {
		info, err := am.GetAgent("developer")
		if err != nil {
			t.Fatal(err)
		}
		if info.Permission.Check("file_write") != true {
			t.Error("developer should be able to write files")
		}
		if info.Permission.Check("edit") != true {
			t.Error("developer should be able to edit files")
		}
		if info.Permission.Check("shell_execute") != true {
			t.Error("developer should be able to run shell")
		}
	})

	t.Run("reviewer has restricted access", func(t *testing.T) {
		info, err := am.GetAgent("reviewer")
		if err != nil {
			t.Fatal(err)
		}
		if info.Permission.Check("file_write") != false {
			t.Error("reviewer should NOT be able to write files")
		}
		if info.Permission.Check("edit") != false {
			t.Error("reviewer should NOT be able to edit files")
		}
		if info.Permission.Check("apply_patch") != false {
			t.Error("reviewer should NOT be able to apply patches")
		}
		if info.Permission.Check("file_read") != true {
			t.Error("reviewer should be able to read files")
		}
		if info.Permission.Check("shell_execute") != true {
			t.Error("reviewer should be able to run shell")
		}
	})

	t.Run("qa has default (full) access", func(t *testing.T) {
		info, err := am.GetAgent("qa")
		if err != nil {
			t.Fatal(err)
		}
		if info.Permission.Check("file_write") != true {
			t.Error("qa should be able to write files")
		}
		if info.Permission.Check("edit") != true {
			t.Error("qa should be able to edit files")
		}
		if info.Permission.Check("shell_execute") != true {
			t.Error("qa should be able to run shell")
		}
	})
}

func TestCanAccess(t *testing.T) {
	cfg := map[string]AgentCfg{
		"reviewer": {
			Mode:       "subagent",
			Permission: NewPermissionFromConfig(map[string]string{"file_write": "deny", "edit": "deny"}),
		},
		"developer": {
			Mode: "subagent",
		},
	}

	am := NewAgentManager()
	am.LoadFromConfig(cfg)

	t.Run("reviewer cannot write", func(t *testing.T) {
		ok, err := am.CanAccess("reviewer", "file_write")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Error("reviewer should not have file_write access")
		}
	})

	t.Run("reviewer can read", func(t *testing.T) {
		ok, err := am.CanAccess("reviewer", "file_read")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Error("reviewer should have file_read access")
		}
	})

	t.Run("developer can do anything", func(t *testing.T) {
		ok, err := am.CanAccess("developer", "file_write")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Error("developer should have file_write access")
		}
	})

	t.Run("unknown agent returns error", func(t *testing.T) {
		_, err := am.CanAccess("nonexistent", "file_read")
		if err == nil {
			t.Error("expected error for unknown agent")
		}
	})
}

func TestDeriveSubagentPermission(t *testing.T) {
	cfg := map[string]AgentCfg{
		"child": {
			Mode:       "subagent",
			Permission: NewPermissionFromConfig(map[string]string{"edit": "deny"}),
		},
	}

	am := NewAgentManager()
	am.LoadFromConfig(cfg)

	parentPerm := Permission{"file_write": "deny", "*": "allow"}

	t.Run("child inherits parent deny and adds its own", func(t *testing.T) {
		merged := am.DeriveSubagentPermission(parentPerm, "child")

		if merged.GetAction("file_write") != "deny" {
			t.Errorf("file_write should be denied (from parent): got %q", merged.GetAction("file_write"))
		}
		if merged.GetAction("edit") != "deny" {
			t.Errorf("edit should be denied (from child): got %q", merged.GetAction("edit"))
		}
		if merged.GetAction("file_read") != "allow" {
			t.Errorf("file_read should be allowed: got %q", merged.GetAction("file_read"))
		}
	})

	t.Run("unknown agent falls back to parent", func(t *testing.T) {
		merged := am.DeriveSubagentPermission(parentPerm, "nonexistent")
		if merged.GetAction("file_write") != "deny" {
			t.Error("should use parent permission for unknown agent")
		}
	})

	t.Run("child deny overrides parent allow", func(t *testing.T) {
		childCfg := map[string]AgentCfg{
			"restricted": {
				Mode:       "subagent",
				Permission: NewPermissionFromConfig(map[string]string{"read": "deny"}),
			},
		}
		localAm := NewAgentManager()
		localAm.LoadFromConfig(childCfg)

		parent := Permission{"*": "allow"}
		merged := localAm.DeriveSubagentPermission(parent, "restricted")

		if merged.GetAction("read") != "deny" {
			t.Errorf("child deny should override parent allow: got %q", merged.GetAction("read"))
		}
	})
}

func TestLoadFromConfigRealJSON(t *testing.T) {
	jsonCfg := `{
		"lead": {
			"description": "Lead agent",
			"mode": "primary",
			"prompt": "agents/lead.md"
		},
		"developer": {
			"description": "Developer",
			"mode": "subagent",
			"prompt": "agents/developer.md"
		},
		"reviewer": {
			"description": "Code reviewer",
			"mode": "subagent",
			"leaf": true,
			"review": true,
			"prompt": "agents/reviewer.md",
			"permission": {
				"file_write": "deny",
				"edit": "deny",
				"apply_patch": "deny"
			}
		},
		"qa": {
			"description": "QA engineer",
			"mode": "subagent",
			"leaf": true,
			"prompt": "agents/qa.md"
		}
	}`

	var agents map[string]AgentCfg
	if err := json.Unmarshal([]byte(jsonCfg), &agents); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	am := NewAgentManager()
	am.LoadFromConfig(agents)

	t.Run("reviewer has correct flags and permissions", func(t *testing.T) {
		info, err := am.GetAgent("reviewer")
		if err != nil {
			t.Fatal(err)
		}
		if !info.Leaf {
			t.Error("reviewer should be leaf")
		}
		if !info.Review {
			t.Error("reviewer should be review")
		}
		if info.Permission.GetAction("file_write") != "deny" {
			t.Errorf("file_write: got %q, want deny", info.Permission.GetAction("file_write"))
		}
		if info.Permission.GetAction("edit") != "deny" {
			t.Errorf("edit: got %q, want deny", info.Permission.GetAction("edit"))
		}
		if info.Permission.GetAction("file_read") != "allow" {
			t.Errorf("file_read: got %q, want allow", info.Permission.GetAction("file_read"))
		}
	})

	t.Run("developer has default permissions", func(t *testing.T) {
		info, err := am.GetAgent("developer")
		if err != nil {
			t.Fatal(err)
		}
		if info.Leaf {
			t.Error("developer should not be leaf")
		}
		if info.Review {
			t.Error("developer should not be review")
		}
		if info.Permission.GetAction("file_write") != "allow" {
			t.Errorf("file_write should be allow by default, got %q", info.Permission.GetAction("file_write"))
		}
	})

	t.Run("qa has default permissions", func(t *testing.T) {
		info, err := am.GetAgent("qa")
		if err != nil {
			t.Fatal(err)
		}
		if !info.Leaf {
			t.Error("qa should be leaf")
		}
		if info.Review {
			t.Error("qa should not be review")
		}
		if info.Permission.GetAction("file_write") != "allow" {
			t.Errorf("qa should be able to write, got %q", info.Permission.GetAction("file_write"))
		}
	})

	t.Run("lead has default permissions", func(t *testing.T) {
		info, err := am.GetAgent("lead")
		if err != nil {
			t.Fatal(err)
		}
		if info.Description != "Lead agent" {
			t.Errorf("description: got %q, want 'Lead agent'", info.Description)
		}
	})
}
