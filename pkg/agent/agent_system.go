package agent

import (
	"sort"
)

// ============================================================
// Agent System — поддержка ролей агентов как в opencode
// ============================================================

// AgentMode определяет в каком режиме может работать агент
type AgentMode string

const (
	AgentModePrimary AgentMode = "primary" // Основной агент (build, plan)
	AgentModeSubagent AgentMode = "subagent" // Субагент (general, explore)
	AgentModeAll     AgentMode = "all"     // Любой режим
)

// AgentRole представляет роль агента
type AgentRole struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Mode        AgentMode `json:"mode"`
	Native      bool     `json:"native"`
	Hidden      bool     `json:"hidden"`
	Color       string   `json:"color"`
}

// AvailableAgents возвращает доступные агенты
var AvailableAgents = map[string]AgentRole{
	"build": {
		Name: "build",
		Description: "The default agent. Executes tools based on configured permissions.",
		Mode: AgentModePrimary,
		Native: true,
	},
	"plan": {
		Name: "plan", 
		Description: "Plan mode. Disallows all edit tools.",
		Mode: AgentModePrimary,
		Native: true,
	},
	"general": {
		Name: "general",
		Description: "General-purpose agent for researching complex questions and executing multi-step tasks.",
		Mode: AgentModeSubagent,
		Native: true,
	},
	"explore": {
		Name: "explore",
		Description: "Fast agent specialized for exploring codebases.",
		Mode: AgentModeSubagent,
		Native: true,
	},
	"compaction": {
		Name: "compaction",
		Description: "Compaction mode for summarizing conversation history.",
		Mode: AgentModePrimary,
		Native: true,
		Hidden: true,
	},
	"title": {
		Name: "title",
		Description: "Generates titles for sessions.",
		Mode: AgentModePrimary,
		Native: true,
		Hidden: true,
	},
	"summary": {
		Name: "summary",
		Description: "Summarizes conversation history.",
		Mode: AgentModePrimary,
		Native: true,
		Hidden: true,
	},
}

// GetAgent возвращает информацию об агенте
func GetAgent(name string) (*AgentRole, bool) {
	agent, exists := AvailableAgents[name]
	if !exists {
		return nil, false
	}
	return &agent, true
}

// ListAgents возвращает список доступных агентов
func ListAgents(modeFilter AgentMode) []string {
	var names []string
	for name, role := range AvailableAgents {
		if modeFilter != "" && role.Mode != modeFilter {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// HasPermission проверяет имеет ли роль разрешение на действие
func HasPermission(role, action string, permissions []string) bool {
	for _, perm := range permissions {
		if perm == role || perm == action || perm == "*" {
			return true
		}
	}
	return false
}

// GetDefaultAgent возвращает агента по умолчанию
func GetDefaultAgent() string {
	if agent, exists := AvailableAgents["build"]; exists {
		return agent.Name
	}
	return "build"
}
