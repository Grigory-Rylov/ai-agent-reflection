package agentpolicy

import (
	"path/filepath"
	"strings"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/permission"
)


type Permission map[string]string


func (p Permission) Check(toolName string) bool {
	return p.GetAction(toolName) != "deny"
}


func (p Permission) GetAction(toolName string) string {
	
	if action, ok := p[toolName]; ok {
		return action
	}

	
	if action, ok := p["*"]; ok {
		return action
	}

	
	for pattern, action := range p {
		if strings.Contains(pattern, "*") {
			if matchGlob(pattern, toolName) {
				return action
			}
		}
	}

	return "allow"
}


func NewPermissionFromConfig(cfg map[string]string) Permission {
	p := make(Permission)
	for k, v := range cfg {
		p[k] = v
	}
	return p
}


func DefaultPermission() Permission {
	return Permission{
		"*": "allow",
	}
}


func UserFacingPermission() Permission {
	return Permission{
		"*":             "allow",
		"file_write":    "ask",
		"shell_execute": "ask",
		"edit":          "ask",
	}
}


func MergePermissions(base Permission, override Permission) Permission {
	merged := make(Permission)
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}


type PermissionAdapter struct {
	P       Permission
	Ruleset *permission.Ruleset
}

func (a *PermissionAdapter) Check(toolName string) string {
	if a == nil || a.P == nil {
		return "allow"
	}
	return a.P.GetAction(toolName)
}


func (a *PermissionAdapter) Evaluate(permissionName, pattern string) string {
	if a == nil || a.Ruleset == nil {
		return "ask"
	}
	return string(permission.Evaluate(permissionName, pattern, *a.Ruleset).Action)
}


func (a *PermissionAdapter) Approve(permissionName, pattern string) {
	if a == nil {
		return
	}
	rs := permission.Merge(*a.Ruleset, permission.Ruleset{
		{Permission: permissionName, Pattern: pattern, Action: permission.Allow},
	})
	a.Ruleset = &rs
}


func (a *PermissionAdapter) SetRuleset(rs permission.Ruleset) {
	a.Ruleset = &rs
}


func NewPermissionAdapter(p Permission) *PermissionAdapter {
	return &PermissionAdapter{P: p, Ruleset: toRuleset(p)}
}


func NewRulePermissionAdapter(rs permission.Ruleset) *PermissionAdapter {
	return &PermissionAdapter{P: Permission{}, Ruleset: &rs}
}


func toRuleset(p Permission) *permission.Ruleset {
	rs := make(permission.Ruleset, 0, len(p))
	for tool, action := range p {
		if tool == "*" {
			continue
		}
		rs = append(rs, permission.Rule{
			Permission: tool,
			Pattern:    "*",
			Action:     permission.Action(action),
		})
	}
	if len(rs) == 0 {
		return nil
	}
	return &rs
}


func matchGlob(pattern, name string) bool {
	
	if !strings.Contains(pattern, "*") {
		return pattern == name
	}

	
	parts := strings.Split(pattern, "*")
	if len(parts) == 2 {
		prefix, suffix := parts[0], parts[1]
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			return false
		}
		if suffix != "" && !strings.HasSuffix(name, suffix) {
			return false
		}
		return true
	}

	
	matched, _ := filepath.Match(pattern, name)
	return matched
}
