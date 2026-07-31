package permission

import (
	"os"
	"strings"
)

// Action — результат оценки правила разрешения.
type Action string

const (
	Allow Action = "allow"
	Deny  Action = "deny"
	Ask   Action = "ask"
)

// Rule — одно правило разрешения: для какого инструмента (permission),
// для какого паттерна (pattern) и какое действие (action) применяется.
type Rule struct {
	Permission string
	Pattern    string
	Action     Action
}

// Ruleset — упорядоченный набор правил. Порядок важен:
// при оценке выигрывает последнее подходящее правило.
type Ruleset []Rule

// Evaluate возвращает подходящее правило для (permission, pattern).
// Проходит по всем rulesets в порядке передачи и возвращает последнее
// совпавшее правило (как opencode: findLast). Если ни одно правило
// не совпало — возвращает действие "ask".
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

// Merge объединяет несколько rulesets в один, сохраняя порядок.
func Merge(rulesets ...Ruleset) Ruleset {
	var out Ruleset
	for _, rs := range rulesets {
		out = append(out, rs...)
	}
	return out
}

// FromConfig преобразует конфигурацию вида
//
//	{"tool": "action"}                -> {permission: tool, pattern: "*", action}
//	{"tool": {"pattern": "action"}}   -> правила с конкретными паттернами
//
// Паттерны "~" и "$HOME" раскрываются в домашнюю директорию.
func FromConfig(cfg map[string]any) Ruleset {
	rules := make(Ruleset, 0, len(cfg))
	for key, value := range cfg {
		switch v := value.(type) {
		case string:
			rules = append(rules, Rule{Permission: key, Pattern: "*", Action: Action(v)})
		case map[string]any:
			for pattern, action := range v {
				rules = append(rules, Rule{
					Permission: key,
					Pattern:    expand(pattern),
					Action:     Action(action.(string)),
				})
			}
		}
	}
	return rules
}

// expand раскрывает "~/..." и "$HOME..." в абсолютный путь домашней директории.
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

// Disabled возвращает множество инструментов, которые полностью запрещены
// правилом deny с паттерном "*". Для edit/write/apply_patch используется
// общее разрешение "edit", как в opencode.
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

// lastForPermission возвращает последнее правило, чей permission-паттерн
// совпадает с указанным инструментом (без учёта pattern).
func lastForPermission(permission string, ruleset Ruleset) *Rule {
	for i := len(ruleset) - 1; i >= 0; i-- {
		if Match(permission, ruleset[i].Permission) {
			return &ruleset[i]
		}
	}
	return nil
}
