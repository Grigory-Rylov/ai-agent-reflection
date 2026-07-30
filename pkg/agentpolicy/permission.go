package agentpolicy

import (
	"path/filepath"
	"strings"
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
	P Permission
}

func (a *PermissionAdapter) Check(toolName string) string {
	if a == nil || a.P == nil {
		return "allow"
	}
	return a.P.GetAction(toolName)
}

// NewPermissionAdapter создаёт адаптер из Permission
func NewPermissionAdapter(p Permission) *PermissionAdapter {
	return &PermissionAdapter{P: p}
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
