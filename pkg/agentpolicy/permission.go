package agentpolicy

import (
	"path/filepath"
	"strings"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/permission"
)

// Permission — карта разрешений для инструментов
// Ключ = имя инструмента (или паттерн), Значение = "allow", "deny", "ask"
type Permission map[string]string

// Check проверяет разрешение для инструмента (bool)
func (p Permission) Check(toolName string) bool {
	return p.GetAction(toolName) != "deny"
}

// GetAction возвращает действие для инструмента: "allow", "deny", "ask"
func (p Permission) GetAction(toolName string) string {
	// Прямое совпадение
	if action, ok := p[toolName]; ok {
		return action
	}

	// Проверяем wildcard
	if action, ok := p["*"]; ok {
		return action
	}

	// Проверяем паттерны
	for pattern, action := range p {
		if strings.Contains(pattern, "*") {
			if matchGlob(pattern, toolName) {
				return action
			}
		}
	}

	return "allow"
}

// NewPermissionFromConfig создаёт Permission из карты "tool": "action"
func NewPermissionFromConfig(cfg map[string]string) Permission {
	p := make(Permission)
	for k, v := range cfg {
		p[k] = v
	}
	return p
}

// DefaultPermission возвращает разрешение по умолчанию
func DefaultPermission() Permission {
	return Permission{
		"*": "allow",
	}
}

// UserFacingPermission возвращает разрешение для пользовательского агента:
// опасные инструменты требуют подтверждения, остальное разрешено.
func UserFacingPermission() Permission {
	return Permission{
		"*":             "allow",
		"file_write":    "ask",
		"shell_execute": "ask",
		"edit":          "ask",
	}
}

// MergePermissions объединяет два Permission (второй имеет приоритет)
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

// PermissionAdapter адаптирует Permission к интерфейсу проверки разрешений.
// Реализует contract: Check(toolName string) string, возвращая "allow", "deny", "ask".
// Используется для интеграции с agent.PermissionChecker без импорта пакета agent.
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

// Evaluate возвращает действие по правилам (permission, pattern).
// Используется для shell-команд: permission="bash", pattern=полная команда.
func (a *PermissionAdapter) Evaluate(permissionName, pattern string) string {
	if a == nil || a.Ruleset == nil {
		return "ask"
	}
	return string(permission.Evaluate(permissionName, pattern, *a.Ruleset).Action)
}

// Approve добавляет правило allow для (permission, pattern).
// Используется при выборе "Always allow" для запомненной команды.
func (a *PermissionAdapter) Approve(permissionName, pattern string) {
	if a == nil {
		return
	}
	rs := permission.Merge(*a.Ruleset, permission.Ruleset{
		{Permission: permissionName, Pattern: pattern, Action: permission.Allow},
	})
	a.Ruleset = &rs
}

// SetRuleset устанавливает правила конфигурации.
func (a *PermissionAdapter) SetRuleset(rs permission.Ruleset) {
	a.Ruleset = &rs
}

// NewPermissionAdapter создаёт адаптер из Permission
func NewPermissionAdapter(p Permission) *PermissionAdapter {
	return &PermissionAdapter{P: p, Ruleset: toRuleset(p)}
}

// NewRulePermissionAdapter создаёт адаптер из правил конфигурации.
func NewRulePermissionAdapter(rs permission.Ruleset) *PermissionAdapter {
	return &PermissionAdapter{P: Permission{}, Ruleset: &rs}
}

// toRuleset преобразует Permission (tool -> action) в Ruleset с паттерном "*".
// Ключ "*" пропускается: он управляет только Check() на уровне инструмента,
// а для Evaluate (напр. bash-паттерны) не должен переопределять поведение.
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

// matchGlob — простой glob-матчер для паттернов
func matchGlob(pattern, name string) bool {
	// Поддерживаем простые паттерны: "file_*", "*.go", "edit:*/..."
	if !strings.Contains(pattern, "*") {
		return pattern == name
	}

	// Разбиваем по *
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

	// Fallback: используем filepath.Match
	matched, _ := filepath.Match(pattern, name)
	return matched
}
