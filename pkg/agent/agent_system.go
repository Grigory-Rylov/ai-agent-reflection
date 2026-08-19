package agent

import (
	"sort"
)


type AgentMode string

const (
	AgentModePrimary AgentMode = "primary" 
	AgentModeSubagent AgentMode = "subagent" 
	AgentModeAll     AgentMode = "all"     
)


type AgentRole struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Mode        AgentMode `json:"mode"`
	Native      bool     `json:"native"`
	Hidden      bool     `json:"hidden"`
	Color       string   `json:"color"`
}


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


func GetAgent(name string) (*AgentRole, bool) {
	agent, exists := AvailableAgents[name]
	if !exists {
		return nil, false
	}
	return &agent, true
}


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


func HasPermission(role, action string, permissions []string) bool {
	for _, perm := range permissions {
		if perm == role || perm == action || perm == "*" {
			return true
		}
	}
	return false
}


func GetDefaultAgent() string {
	if agent, exists := AvailableAgents["build"]; exists {
		return agent.Name
	}
	return "build"
}
