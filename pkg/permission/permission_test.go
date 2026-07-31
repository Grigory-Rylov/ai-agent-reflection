package permission

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name       string
		permission string
		pattern    string
		rulesets   []Ruleset
		wantAction Action
	}{
		{
			name:       "empty ruleset returns ask",
			permission: "bash",
			pattern:    "rm",
			rulesets:   nil,
			wantAction: Ask,
		},
		{
			name:       "no matching pattern returns ask",
			permission: "edit",
			pattern:    "etc/passwd",
			rulesets:   []Ruleset{Ruleset{{Permission: "edit", Pattern: "src/*", Action: Allow}}},
			wantAction: Ask,
		},
		{
			name:       "exact pattern match",
			permission: "bash",
			pattern:    "rm",
			rulesets:   []Ruleset{Ruleset{{Permission: "bash", Pattern: "rm", Action: Deny}}},
			wantAction: Deny,
		},
		{
			name:       "wildcard pattern match",
			permission: "bash",
			pattern:    "rm",
			rulesets:   []Ruleset{Ruleset{{Permission: "bash", Pattern: "*", Action: Allow}}},
			wantAction: Allow,
		},
		{
			name:       "last matching rule wins",
			permission: "bash",
			pattern:    "rm",
			rulesets: []Ruleset{Ruleset{
				{Permission: "bash", Pattern: "*", Action: Allow},
				{Permission: "bash", Pattern: "rm", Action: Deny},
			}},
			wantAction: Deny,
		},
		{
			name:       "last matching rule wins wildcard after specific",
			permission: "bash",
			pattern:    "rm",
			rulesets: []Ruleset{Ruleset{
				{Permission: "bash", Pattern: "rm", Action: Deny},
				{Permission: "bash", Pattern: "*", Action: Allow},
			}},
			wantAction: Allow,
		},
		{
			name:       "glob pattern match",
			permission: "edit",
			pattern:    "src/foo.ts",
			rulesets:   []Ruleset{Ruleset{{Permission: "edit", Pattern: "src/*", Action: Allow}}},
			wantAction: Allow,
		},
		{
			name:       "last matching glob wins",
			permission: "edit",
			pattern:    "src/components/Button.tsx",
			rulesets: []Ruleset{Ruleset{
				{Permission: "edit", Pattern: "src/*", Action: Deny},
				{Permission: "edit", Pattern: "src/components/*", Action: Allow},
			}},
			wantAction: Allow,
		},
		{
			name:       "unknown permission returns ask",
			permission: "unknown_tool",
			pattern:    "anything",
			rulesets:   []Ruleset{Ruleset{{Permission: "bash", Pattern: "*", Action: Allow}}},
			wantAction: Ask,
		},
		{
			name:       "wildcard permission matches any permission",
			permission: "bash",
			pattern:    "rm",
			rulesets:   []Ruleset{Ruleset{{Permission: "*", Pattern: "*", Action: Deny}}},
			wantAction: Deny,
		},
		{
			name:       "glob permission pattern",
			permission: "mcp_server_tool",
			pattern:    "anything",
			rulesets:   []Ruleset{Ruleset{{Permission: "mcp_*", Pattern: "*", Action: Allow}}},
			wantAction: Allow,
		},
		{
			name:       "specific permission overrides wildcard permission",
			permission: "bash",
			pattern:    "rm",
			rulesets: []Ruleset{Ruleset{
				{Permission: "*", Pattern: "*", Action: Deny},
				{Permission: "bash", Pattern: "*", Action: Allow},
			}},
			wantAction: Allow,
		},
		{
			name:       "multiple matching permission patterns last wins",
			permission: "mcp_dangerous",
			pattern:    "anything",
			rulesets: []Ruleset{Ruleset{
				{Permission: "*", Pattern: "*", Action: Ask},
				{Permission: "mcp_*", Pattern: "*", Action: Allow},
				{Permission: "mcp_dangerous", Pattern: "*", Action: Deny},
			}},
			wantAction: Deny,
		},
		{
			name:       "merges multiple rulesets, later wins",
			permission: "bash",
			pattern:    "rm",
			rulesets: []Ruleset{
				Ruleset{{Permission: "bash", Pattern: "*", Action: Allow}},
				Ruleset{{Permission: "bash", Pattern: "rm", Action: Deny}},
			},
			wantAction: Deny,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := Evaluate(tt.permission, tt.pattern, tt.rulesets...)
			if rule.Action != tt.wantAction {
				t.Errorf("Evaluate(%q, %q).action = %q, want %q", tt.permission, tt.pattern, rule.Action, tt.wantAction)
			}
		})
	}
}

func TestEvaluateTrailingSpaceWildcard(t *testing.T) {
	ruleset := Ruleset{
		{Permission: "bash", Pattern: "*", Action: Ask},
		{Permission: "bash", Pattern: "git *", Action: Allow},
	}

	tests := []struct {
		pattern string
		want    Action
	}{
		{"git status", Allow},
		{"git", Allow},
		{"npm run build", Ask},
		{"git commit -m x", Allow},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			rule := Evaluate("bash", tt.pattern, ruleset)
			if rule.Action != tt.want {
				t.Errorf("Evaluate action = %q, want %q", rule.Action, tt.want)
			}
		})
	}
}

func TestMerge(t *testing.T) {
	tests := []struct {
		name     string
		rulesets []Ruleset
		want     Ruleset
	}{
		{
			name:     "simple concatenation",
			rulesets: []Ruleset{Ruleset{{Permission: "bash", Pattern: "*", Action: Allow}}, Ruleset{{Permission: "bash", Pattern: "*", Action: Deny}}},
			want: Ruleset{
				{Permission: "bash", Pattern: "*", Action: Allow},
				{Permission: "bash", Pattern: "*", Action: Deny},
			},
		},
		{
			name:     "empty ruleset does nothing",
			rulesets: []Ruleset{Ruleset{{Permission: "bash", Pattern: "*", Action: Allow}}, Ruleset{}},
			want:     Ruleset{{Permission: "bash", Pattern: "*", Action: Allow}},
		},
		{
			name: "preserves rule order",
			rulesets: []Ruleset{
				Ruleset{
					{Permission: "edit", Pattern: "src/*", Action: Allow},
					{Permission: "edit", Pattern: "src/secret/*", Action: Deny},
				},
				Ruleset{{Permission: "edit", Pattern: "src/secret/ok.ts", Action: Allow}},
			},
			want: Ruleset{
				{Permission: "edit", Pattern: "src/*", Action: Allow},
				{Permission: "edit", Pattern: "src/secret/*", Action: Deny},
				{Permission: "edit", Pattern: "src/secret/ok.ts", Action: Allow},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Merge(tt.rulesets...)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Merge() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFromConfig(t *testing.T) {
	if os.Getenv("HOME") == "" {
		t.Setenv("HOME", "/home/testuser")
	}
	expandedHome := os.Getenv("HOME")

	tests := []struct {
		name string
		cfg  map[string]any
		want Ruleset
	}{
		{
			name: "string value becomes wildcard rule",
			cfg:  map[string]any{"bash": "allow"},
			want: Ruleset{{Permission: "bash", Pattern: "*", Action: Allow}},
		},
		{
			name: "object value converts to rules array",
			cfg:  map[string]any{"bash": map[string]any{"*": "allow", "rm": "deny"}},
			want: Ruleset{
				{Permission: "bash", Pattern: "*", Action: Allow},
				{Permission: "bash", Pattern: "rm", Action: Deny},
			},
		},
		{
			name: "mixed string and object values",
			cfg: map[string]any{
				"bash":     map[string]any{"*": "allow", "rm": "deny"},
				"edit":     "allow",
				"webfetch": "ask",
			},
			want: Ruleset{
				{Permission: "bash", Pattern: "*", Action: Allow},
				{Permission: "bash", Pattern: "rm", Action: Deny},
				{Permission: "edit", Pattern: "*", Action: Allow},
				{Permission: "webfetch", Pattern: "*", Action: Ask},
			},
		},
		{
			name: "empty object",
			cfg:  map[string]any{},
			want: Ruleset{},
		},
		{
			name: "expands tilde to home directory",
			cfg:  map[string]any{"external_directory": map[string]any{"~/projects/*": "allow"}},
			want: Ruleset{{Permission: "external_directory", Pattern: filepath.Join(expandedHome, "projects", "*"), Action: Allow}},
		},
		{
			name: "expands dollar HOME to home directory",
			cfg:  map[string]any{"external_directory": map[string]any{"$HOME/projects/*": "allow"}},
			want: Ruleset{{Permission: "external_directory", Pattern: filepath.Join(expandedHome, "projects", "*"), Action: Allow}},
		},
		{
			name: "expands exact tilde to home directory",
			cfg:  map[string]any{"external_directory": map[string]any{"~": "allow"}},
			want: Ruleset{{Permission: "external_directory", Pattern: expandedHome, Action: Allow}},
		},
		{
			name: "does not expand tilde in middle of path",
			cfg:  map[string]any{"external_directory": map[string]any{"/some/~/path": "allow"}},
			want: Ruleset{{Permission: "external_directory", Pattern: "/some/~/path", Action: Allow}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromConfig(tt.cfg)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FromConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDisabled(t *testing.T) {
	tests := []struct {
		name    string
		tools   []string
		ruleset Ruleset
		want    map[string]bool
	}{
		{
			name:    "returns empty set when all tools allowed",
			tools:   []string{"bash", "edit", "read"},
			ruleset: Ruleset{{Permission: "*", Pattern: "*", Action: Allow}},
			want:    map[string]bool{},
		},
		{
			name:    "disables tool when denied",
			tools:   []string{"bash", "edit", "read"},
			ruleset: Ruleset{{Permission: "*", Pattern: "*", Action: Allow}, {Permission: "bash", Pattern: "*", Action: Deny}},
			want:    map[string]bool{"bash": true},
		},
		{
			name:    "disables edit/write/apply_patch when edit denied",
			tools:   []string{"edit", "write", "apply_patch", "bash"},
			ruleset: Ruleset{{Permission: "*", Pattern: "*", Action: Allow}, {Permission: "edit", Pattern: "*", Action: Deny}},
			want:    map[string]bool{"edit": true, "write": true, "apply_patch": true},
		},
		{
			name:    "does not disable when partially denied",
			tools:   []string{"bash"},
			ruleset: Ruleset{{Permission: "bash", Pattern: "*", Action: Allow}, {Permission: "bash", Pattern: "rm *", Action: Deny}},
			want:    map[string]bool{},
		},
		{
			name:    "does not disable when action is ask",
			tools:   []string{"bash", "edit"},
			ruleset: Ruleset{{Permission: "*", Pattern: "*", Action: Ask}},
			want:    map[string]bool{},
		},
		{
			name:    "specific allow overrides wildcard deny",
			tools:   []string{"bash", "edit", "read"},
			ruleset: Ruleset{{Permission: "*", Pattern: "*", Action: Deny}, {Permission: "bash", Pattern: "*", Action: Allow}},
			want:    map[string]bool{"edit": true, "read": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Disabled(tt.tools, tt.ruleset)
			if len(got) != len(tt.want) {
				t.Errorf("Disabled() = %v, want %v", got, tt.want)
				return
			}
			for k := range tt.want {
				if !got[k] {
					t.Errorf("Disabled(): expected %q disabled, got %v", k, got)
				}
			}
		})
	}
}
