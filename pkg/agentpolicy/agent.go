package agentpolicy

import "fmt"

// AgentMode определяет в каких контекстах может работать агент
type AgentMode string

const (
	ModePrimary  AgentMode = "primary"  // Главный агент, может общаться с пользователем
	ModeSubagent AgentMode = "subagent" // Под-агент, используется для делегирования
	ModeAll      AgentMode = "all"      // Оба режима
)

// AgentInfo описывает конфигурацию агента
type AgentInfo struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Mode        AgentMode                `json:"mode"`
	Native      bool                     `json:"native"`
	Hidden      bool                     `json:"hidden"`
	Leaf        bool                     `json:"leaf"`
	Review      bool                     `json:"review"`
	Coordinator bool                   `json:"coordinator"`
	Prompt      string                   `json:"prompt"`
	Model       string                   `json:"model"`
	Temperature *float64                 `json:"temperature"`
	TopP        *float64                 `json:"topP"`
	Color       string                   `json:"color"`
	Permission  Permission               `json:"permission"`
	Options     map[string]interface{}    `json:"options"`
}

// AgentManager управляет доступными агентами
type AgentManager struct {
	agents map[string]AgentInfo
	user   Permission
}

// NewAgentManager создаёт менеджер с предустановленными агентами
func NewAgentManager() *AgentManager {
	am := &AgentManager{
		agents: make(map[string]AgentInfo),
	}
	am.initDefaults()
	return am
}

// initDefaults инициализирует стандартные агенты (как в opencode)
func (am *AgentManager) initDefaults() {
	defaultPerm := DefaultPermission()

	// Build agent — основной агент для выполнения задач
	am.agents["build"] = AgentInfo{
		Name:        "build",
		Description: "The default agent. Executes tools based on configured permissions.",
		Mode:        ModePrimary,
		Native:      true,
		Permission:  MergePermissions(defaultPerm, Permission{}),
		Model:       "local-model",
		Options:     map[string]interface{}{},
	}

	// Plan agent — режим планирования (ограниченные права на редактирование)
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

	// General subagent — для исследования и параллельных задач
	am.agents["general"] = AgentInfo{
		Name:        "general",
		Description: "General-purpose agent for researching complex questions and executing multi-step tasks. Use this agent to execute multiple units of work in parallel.",
		Mode:        ModeSubagent,
		Native:      true,
		Permission:  defaultPerm,
		Prompt:      `You are a General-purpose agent. Research and execute multi-step tasks autonomously.

## Instructions
1. Use available tools to gather information and execute tasks
2. Be thorough — search widely, read strategically
3. Return clear, structured results to the caller
4. Do NOT make assumptions — verify with tools

## Rules
- You can create files and run commands as needed
- Return complete results — the caller has no other context`,
		Options:     map[string]interface{}{},
	}

	// Worker agent — leaf agent для выполнения задач
	am.agents["worker"] = AgentInfo{
		Name:        "worker",
		Description: "Implements code changes, writes files, runs commands. Has full tool access.",
		Mode:        ModeSubagent,
		Leaf:        true,
		Native:      true,
		Permission:  DefaultPermission(),
		Options:     map[string]interface{}{},
	}

	// QA agent — review агент, может вызывать worker для исправлений
	am.agents["qa"] = AgentInfo{
		Name:        "qa",
		Description: "Reviews code, builds/tests it, calls worker for fixes, approves when done.",
		Mode:        ModeSubagent,
		Review:      true,
		Native:      true,
		Permission:  DefaultPermission(),
		Options:     map[string]interface{}{},
	}

	// Explore agent — быстрый поиск по коду (только чтение)
	explorePerm := NewPermissionFromConfig(map[string]string{
		"grep":  "allow",
		"glob":  "allow",
		"read":  "allow",
		"file_list": "allow",
		"file_read": "allow",
		"web_fetch": "allow",
		"web_search": "allow",
		"shell_execute": "allow",
		"file_write": "deny",
		"file_edit": "deny",
	})
	am.agents["explore"] = AgentInfo{
		Name:        "explore",
		Description: "Fast agent specialized for exploring codebases. Use for finding files by patterns, searching code for keywords, answering questions about the codebase. Returns file paths as absolute paths.",
		Mode:        ModeSubagent,
		Native:      true,
		Permission:  explorePerm,
		Prompt:      "You are an Explorer. Search and investigate the codebase quickly.\n\n## Available tools\n- `glob` — find files by pattern\n- `search_code` — grep for patterns in files\n- `file_read` — read file contents\n- `file_list` — list directory contents\n- `shell_execute` — run commands (read-only, e.g. git log, ls)\n- `web_fetch` — fetch URLs\n\n## Instructions\n1. Search thoroughly but quickly\n2. Report file paths as absolute paths\n3. Read key files to provide relevant context\n\n## Rules\n- You CANNOT create or modify files\n- You CANNOT edit code\n- Be fast — focus on finding what's needed",
		Options:     map[string]interface{}{},
	}

	// Summary agent — для создания суммаризации
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

// GetAgent возвращает информацию о агенте по имени
func (am *AgentManager) GetAgent(name string) (AgentInfo, error) {
	a, ok := am.agents[name]
	if !ok {
		return AgentInfo{}, fmt.Errorf("agent not found: %s", name)
	}
	return a, nil
}

// ListAgentNames возвращает имена всех зарегистрированных агентов
func (am *AgentManager) ListAgentNames() []string {
	names := make([]string, 0, len(am.agents))
	for name := range am.agents {
		names = append(names, name)
	}
	return names
}

// ListAgents возвращает список всех доступных агентов
func (am *AgentManager) ListAgents() []AgentInfo {
	agents := make([]AgentInfo, 0, len(am.agents))
	for _, a := range am.agents {
		agents = append(agents, a)
	}
	return agents
}

// CanAccess проверяет может ли агент использовать инструмент
func (am *AgentManager) CanAccess(agentName, toolName string) (bool, error) {
	a, err := am.GetAgent(agentName)
	if err != nil {
		return false, err
	}
	return a.Permission.Check(toolName), nil
}

// DeriveSubagentPermission вычисляет разрешения для subagent относительно parent
func (am *AgentManager) DeriveSubagentPermission(parentPerm Permission, subagentName string) Permission {
	subagent, err := am.GetAgent(subagentName)
	if err != nil {
		return parentPerm // Если subagent не найден, используем parent
	}
	// Subagent наследует ограничения parent + свои собственные
	return MergePermissions(parentPerm, subagent.Permission)
}

// RegisterAgent добавляет или обновляет агента
func (am *AgentManager) RegisterAgent(info AgentInfo) {
	// Установить дефолтные разрешения если не указаны
	if info.Permission == nil || len(info.Permission) == 0 {
		info.Permission = DefaultPermission()
	}
	if info.Options == nil {
		info.Options = map[string]interface{}{}
	}
	am.agents[info.Name] = info
}

// RemoveAgent удаляет агента из реестра
func (am *AgentManager) RemoveAgent(name string) bool {
	if _, ok := am.agents[name]; ok {
		delete(am.agents, name)
		return true
	}
	return false
}

// DefaultAgent возвращает агента по умолчанию (build)
func (am *AgentManager) DefaultAgent() (AgentInfo, error) {
	return am.GetAgent("build")
}

// GetAvailableModes возвращает доступные режимы для агента
func (am *AgentManager) GetAvailableModes() []AgentMode {
	return []AgentMode{ModePrimary, ModeSubagent, ModeAll}
}

// LoadFromConfig загружает агентов из конфига (map[name]AgentCfg)
func (am *AgentManager) LoadFromConfig(cfg map[string]AgentCfg) {
	for name, ac := range cfg {
		mode := AgentMode(ac.Mode)
		if mode == "" {
			mode = ModeSubagent
		}
		am.RegisterAgent(AgentInfo{
			Name:        name,
			Description: ac.Description,
			Mode:        mode,
			Hidden:      ac.Hidden,
			Leaf:        ac.Leaf,
			Review:      ac.Review,
			Prompt:      ac.Prompt,
		})
	}
}

// AgentCfg — упрощённая конфигурация агента из JSON-конфига
type AgentCfg struct {
	Mode        string `json:"mode"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Hidden      bool   `json:"hidden"`
	Leaf        bool   `json:"leaf"`
	Review      bool   `json:"review"`
	Coordinator bool                   `json:"coordinator"`
}
