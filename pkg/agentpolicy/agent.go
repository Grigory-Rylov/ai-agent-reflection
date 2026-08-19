package agentpolicy

import "fmt"


type AgentMode string

const (
	ModePrimary  AgentMode = "primary"  
	ModeSubagent AgentMode = "subagent" 
	ModeAll      AgentMode = "all"      
)


type AgentInfo struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Mode        AgentMode                `json:"mode"`
	Native      bool                     `json:"native"`
	Hidden      bool                     `json:"hidden"`
	Internal    bool                     `json:"internal"`
	Leaf        bool                     `json:"leaf"`
	Review      bool                     `json:"review"`
	Coordinator bool                   `json:"coordinator"`
	SubagentTypes []string             `json:"subagentTypes"`
	Prompt      string                   `json:"prompt"`
	Model       string                   `json:"model"`
	Temperature *float64                 `json:"temperature"`
	TopP        *float64                 `json:"topP"`
	Color       string                   `json:"color"`
	Permission  Permission               `json:"permission"`
	Options     map[string]interface{}    `json:"options"`
}


type AgentManager struct {
	agents map[string]AgentInfo
}


func NewAgentManager() *AgentManager {
	am := &AgentManager{
		agents: make(map[string]AgentInfo),
	}
	am.initDefaults()
	return am
}


func (am *AgentManager) initDefaults() {
	defaultPerm := DefaultPermission()

	
	am.agents["build"] = AgentInfo{
		Name:        "build",
		Description: "The default agent. Executes tools based on configured permissions.",
		Mode:        ModePrimary,
		Native:      true,
		Permission:  MergePermissions(defaultPerm, Permission{}),
		Model:       "local-model",
		Options:     map[string]interface{}{},
	}

	
	planPerm := MergePermissions(
		defaultPerm,
		NewPermissionFromConfig(map[string]string{
			"edit": "deny",
		}),
	)
	am.agents["plan"] = AgentInfo{
		Name:        "plan",
		Description: "Plan mode. Disallows edit tools except for plan files.",
		Mode:        ModePrimary,
		Native:      true,
		Permission:  planPerm,
		Options:     map[string]interface{}{},
	}

	
	

	
	am.agents["general"] = AgentInfo{
		Name:        "general",
		Description: "General-purpose agent for researching complex questions and executing multi-step tasks.",
		Mode:        ModeSubagent,
		Native:      true,
		Permission:  defaultPerm,
		Prompt:      `You are a General-purpose agent. Research and execute multi-step tasks autonomously.

## Instructions
1. Use available tools to gather information and execute tasks
2. Be thorough — search widely, read strategically
3. Return clear, structured results to the caller
4. Do NOT make assumptions — verify with tools`,
		Options:     map[string]interface{}{},
	}

	
	am.agents["qa"] = AgentInfo{
		Name:        "qa",
		Description: "Reviews code, builds/tests it, calls worker for fixes, approves when done.",
		Mode:        ModeSubagent,
		Review:      true,
		Native:      true,
		Permission:  DefaultPermission(),
		Options:     map[string]interface{}{},
	}

	
	explorePerm := NewPermissionFromConfig(map[string]string{
		"grep":        "allow",
		"glob":        "allow",
		"read":        "allow",
		"file_list":   "allow",
		"file_read":   "allow",
		"web_fetch":   "allow",
		"web_search":  "allow",
		"shell_execute": "allow",
		"file_write":  "deny",
		"file_edit":   "deny",
	})
	am.agents["explore"] = AgentInfo{
		Name:        "explore",
		Description: "Fast agent specialized for exploring codebases.",
		Mode:        ModeSubagent,
		Native:      true,
		Permission:  explorePerm,
		Prompt:      `You are an Explorer. Search and investigate the codebase quickly.

## Available tools
- glob — find files by pattern
- grep — search code for keywords
- file_read — read file contents
- shell_execute — run commands (read-only)
- web_fetch — fetch URLs`,
		Options:     map[string]interface{}{},
	}

	
	am.agents["summary"] = AgentInfo{
		Name:        "summary",
		Description: "Agent for creating concise summaries of conversations and files.",
		Mode:        ModePrimary,
		Hidden:      true,
		Native:      true,
		Permission:  NewPermissionFromConfig(map[string]string{"*": "deny"}),
		Options:     map[string]interface{}{},
	}
}


func (am *AgentManager) GetAgent(name string) (AgentInfo, error) {
	a, ok := am.agents[name]
	if !ok {
		return AgentInfo{}, fmt.Errorf("agent not found: %s", name)
	}
	return a, nil
}


func (am *AgentManager) ListAgentNames() []string {
	names := make([]string, 0, len(am.agents))
	for name := range am.agents {
		names = append(names, name)
	}
	return names
}


func (am *AgentManager) ListAgents() []AgentInfo {
	agents := make([]AgentInfo, 0, len(am.agents))
	for _, a := range am.agents {
		agents = append(agents, a)
	}
	return agents
}


func (am *AgentManager) SubagentTypesFor(name string) []string {
	if am == nil {
		return nil
	}
	a, err := am.GetAgent(name)
	if err != nil {
		return nil
	}
	return a.SubagentTypes
}


func (am *AgentManager) CanAccess(agentName, toolName string) (bool, error) {
	a, err := am.GetAgent(agentName)
	if err != nil {
		return false, err
	}
	return a.Permission.Check(toolName), nil
}


func (am *AgentManager) DeriveSubagentPermission(parentPerm Permission, subagentName string) Permission {
	subagent, err := am.GetAgent(subagentName)
	if err != nil {
		return parentPerm 
	}
	
	return MergePermissions(parentPerm, subagent.Permission)
}


func (am *AgentManager) RegisterAgent(info AgentInfo) {
	
	if info.Permission == nil || len(info.Permission) == 0 {
		info.Permission = DefaultPermission()
	}
	if info.Options == nil {
		info.Options = map[string]interface{}{}
	}
	am.agents[info.Name] = info
}


func (am *AgentManager) RemoveAgent(name string) bool {
	if _, ok := am.agents[name]; ok {
		delete(am.agents, name)
		return true
	}
	return false
}


func (am *AgentManager) DefaultAgent() (AgentInfo, error) {
	return am.GetAgent("build")
}


func (am *AgentManager) GetAvailableModes() []AgentMode {
	return []AgentMode{ModePrimary, ModeSubagent, ModeAll}
}


func (am *AgentManager) LoadFromConfig(cfg map[string]AgentCfg) {
	for name, ac := range cfg {
		mode := AgentMode(ac.Mode)
		if mode == "" {
			mode = ModeSubagent
		}
		perm := ac.Permission
		if perm == nil || len(perm) == 0 {
			perm = DefaultPermission()
		}
		am.RegisterAgent(AgentInfo{
			Name:        name,
			Description: ac.Description,
			Mode:        mode,
			Hidden:      ac.Hidden,
			Internal:    ac.Internal,
			Leaf:        ac.Leaf,
			Review:      ac.Review,
			Coordinator: ac.Coordinator,
			SubagentTypes: ac.SubagentTypes,
			Prompt:      ac.Prompt,
			Permission:  perm,
		})
	}
}


type AgentCfg struct {
	Mode        string     `json:"mode"`
	Description string     `json:"description"`
	Prompt      string     `json:"prompt"`
	Hidden      bool       `json:"hidden"`
	Internal    bool       `json:"internal"`
	Leaf        bool       `json:"leaf"`
	Review      bool       `json:"review"`
	Coordinator bool       `json:"coordinator"`
	SubagentTypes []string `json:"subagentTypes"`
	Permission  Permission `json:"permission"`
}
