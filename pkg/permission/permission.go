package permission

import (
	"os"
	"sort"
	"strings"
)


type Action string

const (
	Allow Action = "allow"
	Deny  Action = "deny"
	Ask   Action = "ask"
)


type Rule struct {
	Permission string
	Pattern    string
	Action     Action
}


type Ruleset []Rule


func Evaluate(permission, pattern string, rulesets ...Ruleset) Rule {
	flat := Merge(rulesets...)
	for i := len(flat) - 1; i >= 0; i-- {
		rule := flat[i]
		if Match(permission, rule.Permission) && Match(pattern, rule.Pattern) {
			return rule
		}
	}
	return Rule{Permission: permission, Pattern: "*", Action: Ask}
}


func Merge(rulesets ...Ruleset) Ruleset {
	var out Ruleset
	for _, rs := range rulesets {
		out = append(out, rs...)
	}
	return out
}


func FromConfig(cfg map[string]any) Ruleset {
	keys := make([]string, 0, len(cfg))
	for key := range cfg {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	rules := make(Ruleset, 0, len(cfg))
	for _, key := range keys {
		switch v := cfg[key].(type) {
		case string:
			rules = append(rules, Rule{Permission: key, Pattern: "*", Action: Action(v)})
		case map[string]any:
			patterns := make([]string, 0, len(v))
			for pattern := range v {
				patterns = append(patterns, pattern)
			}
			sort.Strings(patterns)
			for _, pattern := range patterns {
				rules = append(rules, Rule{
					Permission: key,
					Pattern:    expand(pattern),
					Action:     Action(v[pattern].(string)),
				})
			}
		}
	}
	return rules
}


func expand(pattern string) string {
	if pattern == "~" {
		return os.Getenv("HOME")
	}
	if strings.HasPrefix(pattern, "~/") {
		return os.Getenv("HOME") + pattern[1:]
	}
	if strings.HasPrefix(pattern, "$HOME/") {
		return os.Getenv("HOME") + pattern[5:]
	}
	if strings.HasPrefix(pattern, "$HOME") {
		return os.Getenv("HOME") + pattern[5:]
	}
	return pattern
}


func Disabled(tools []string, ruleset Ruleset) map[string]bool {
	edits := map[string]bool{"edit": true, "write": true, "apply_patch": true}
	out := make(map[string]bool)
	for _, tool := range tools {
		perm := tool
		if edits[tool] {
			perm = "edit"
		}
		if rule := lastForPermission(perm, ruleset); rule != nil && rule.Action == Deny && rule.Pattern == "*" {
			out[tool] = true
		}
	}
	return out
}


func lastForPermission(permission string, ruleset Ruleset) *Rule {
	for i := len(ruleset) - 1; i >= 0; i-- {
		if Match(permission, ruleset[i].Permission) {
			return &ruleset[i]
		}
	}
	return nil
}
