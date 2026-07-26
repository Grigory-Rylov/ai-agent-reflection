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
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Mode        AgentMode   `json:"mode"`
	Native      bool        `json:"native"`
	Hidden      bool        `json:"hidden"`
	Prompt      string      `json:"prompt"`
	Model       string      `json:"model"`
	Temperature *float64    `json:"temperature"`
	TopP        *float64    `json:"topP"`
	Color       string      `json:"color"`
	Permission  Permission  `json:"permission"`
	Options     map[string]interface{} `json:"options"`
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
		Description: "General-purpose agent for researching complex questions and executing multi-step tasks.",
		Mode:        ModeSubagent,
		Native:      true,
		Permission:  defaultPerm,
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
		Description: "Fast agent specialized for exploring codebases. Use for finding files, searching code, answering questions about the codebase.",
		Mode:        ModeSubagent,
		Native:      true,
		Permission:  explorePerm,
		Prompt:      "You are an explorer. Be thorough but fast. Search widely, read strategically.",
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
