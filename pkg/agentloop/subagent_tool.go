package agentloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencode/llama-client/pkg/agent"
	"github.com/opencode/llama-client/pkg/agentpolicy"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

type SubAgentTool struct {
	AgentConfig     agent.Config
	MainTools       *tools.Registry
	SystemPromptDir string
	AgentManager    *agentpolicy.AgentManager
	CurrentDepth    int
	MaxDepth        int
	PeerID          int64
	ThinkingPeerID  int64
	VKClient        VKClient
	Log             Logger
	Debug           bool
	SetActiveAgent  func(name string)
}

func (t *SubAgentTool) Name() string {
	return "task"
}

func (t *SubAgentTool) Description() string {
	agents := t.availableAgents()
	if len(agents) == 0 {
		return "Launch a sub-agent to handle a task autonomously."
	}
	var b strings.Builder
	b.WriteString("Launch a new agent to handle complex, multistep tasks autonomously.\n\n")
	b.WriteString("When using the Task tool, you must specify a subagent_type parameter to select which agent type to use.\n\n")
	b.WriteString("When NOT to use the Task tool:\n")
	b.WriteString("- If you want to read a specific file path, use the Read or Glob tool instead\n")
	b.WriteString("- If you are searching for a specific class definition like \"class Foo\", use the Grep tool instead\n")
	b.WriteString("- If you are searching for code within a specific file or set of 2-3 files, use the Read tool instead\n")
	b.WriteString("- If no available agent is a good fit for the task, use other tools directly\n\n")
	b.WriteString("Usage notes:\n")
	b.WriteString("1. Launch multiple agents concurrently whenever possible, to maximize performance\n")
	b.WriteString("2. Once you have delegated work to an agent, do not duplicate that work yourself\n")
	b.WriteString("3. When the agent is done, it will return a single message back to you\n")
	b.WriteString("4. Each agent invocation starts with a fresh context\n")
	b.WriteString("5. The agent's outputs should generally be trusted\n")
	b.WriteString("6. Clearly tell the agent whether you expect it to write code or just do research\n")
	b.WriteString("7. If the agent description mentions that it should be used proactively, use it without user asking\n\n")
	b.WriteString("Available agent types and the tools they have access to:\n")
	for _, a := range agents {
		desc := a.Description
		if desc == "" {
			desc = "No description"
		}
		b.WriteString(fmt.Sprintf("- %s: %s\n", a.Name, desc))
	}
	return b.String()
}

func (t *SubAgentTool) availableAgents() []agentpolicy.AgentInfo {
	if t.AgentManager == nil {
		return nil
	}
	all := t.AgentManager.ListAgents()
	var result []agentpolicy.AgentInfo
	for _, a := range all {
		if a.Mode == agentpolicy.ModeSubagent || a.Mode == agentpolicy.ModeAll {
			if !a.Hidden {
				result = append(result, a)
			}
		}
	}
	return result
}

func (t *SubAgentTool) Schema() map[string]interface{} {
	nameDesc := "The type of specialized agent to use for this task"
	if t.AgentManager != nil {
		agents := t.availableAgents()
		if len(agents) > 0 {
			var names []string
			for _, a := range agents {
				names = append(names, a.Name)
			}
			nameDesc = fmt.Sprintf("The type of specialized agent to use. Available: %s", strings.Join(names, ", "))
		}
	}
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"subagent_type": tools.CreateStringParameter("subagent_type", nameDesc, true),
			"name": tools.CreateStringParameter("name",
				"The type of specialized agent to use (same as subagent_type).", false),
			"prompt": tools.CreateStringParameter("prompt",
				"The task for the agent to perform. Include full context.", true),
			"task": tools.CreateStringParameter("task",
				"The task description (same as prompt).", false),
			"description": tools.CreateStringParameter("description",
				"A short (3-5 words) description of the task.", false),
		},
		"required": []string{"subagent_type", "prompt"},
	}
}

func (t *SubAgentTool) Execute(ctx context.Context, inputs map[string]string) (tools.ToolResult, error) {
	// Normalize parameter names — opencode-style FIRST, then our aliases
	name := inputs["subagent_type"]
	if name == "" {
		name = inputs["name"]
	}
	if name == "" {
		name = inputs["agent"]
	}
	if name == "" {
		name = inputs["type"]
	}

	task := inputs["prompt"]
	if task == "" {
		task = inputs["task"]
	}
	if task == "" {
		task = inputs["description"]
	}
	if task == "" {
		task = inputs["instruction"]
	}
	if task == "" {
		task = inputs["title"]
	}

	if name == "" {
		return tools.ToolResult{Success: false,
			Error: fmt.Sprintf("name parameter is required. Available params: subagent_type=%q, name=%q, agent=%q",
				inputs["subagent_type"], inputs["name"], inputs["agent"])}, nil
	}
	if task == "" {
		return tools.ToolResult{Success: false,
			Error: fmt.Sprintf("task parameter is required. Available params: prompt=%q, task=%q, description=%q",
				inputs["prompt"], inputs["task"], inputs["description"])}, nil
	}

	// Validate agent name via AgentManager or fallback
	name, err := t.resolveAgentName(name)
	if err != nil {
		return tools.ToolResult{Success: false, Error: err.Error()}, nil
	}

	if t.CurrentDepth >= t.MaxDepth {
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("max recursion depth (%d) reached", t.MaxDepth)}, nil
	}

	systemPrompt, err := t.loadSystemPrompt(name)
	if err != nil {
		return tools.ToolResult{Success: false, Error: err.Error()}, nil
	}

	a := t.createAgent(name, systemPrompt)

	t.applyAgentPermissions(name, a)

	if t.isReviewAgent(name) {
		t.registerReadOnlyTools(a)
		t.registerReviewTool(name, a)
	} else {
		t.registerMainTools(a)
		t.registerSubAgentTool(name, a)
		t.registerReviewTool(name, a)
	}

	a.SetThinkingCallback(t.makeThinkingCallback(name))

	if t.SetActiveAgent != nil {
		t.SetActiveAgent(name)
	}

	response, err := a.ProcessMessage(ctx, task, t.PeerID)
	if err != nil {
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("sub-agent %q failed: %v", name, err)}, nil
	}

	return tools.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"response": response,
		},
	}, nil
}

func (t *SubAgentTool) applyAgentPermissions(name string, a agent.Agent) {
	if t.AgentManager == nil {
		return
	}
	info, err := t.AgentManager.GetAgent(name)
	if err != nil {
		return
	}
	if info.Permission == nil || len(info.Permission) == 0 {
		return
	}
	if ps, ok := a.(interface{ SetPermissionChecker(agent.PermissionChecker) }); ok {
		ps.SetPermissionChecker(agentpolicy.NewPermissionAdapter(info.Permission))
	}
}

func (t *SubAgentTool) registerReadOnlyTools(a agent.Agent) {
	roReg := tools.NewRegistry()
	roReg.Register(&tools.FileReadTool{})
	roReg.Register(&tools.TimeGetTool{})
	roReg.Register(&tools.DirListTool{})
	roReg.Register(&tools.WebFetchTool{})
	roReg.Register(&tools.WebSearchTool{})
	roReg.Register(&tools.GlobTool{})
	roReg.Register(&tools.GrepTool{})
	roReg.Register(&tools.CalcTool{})
	roReg.Register(&tools.ShellExecuteTool{})
	if inserter, ok := a.(toolInserter); ok {
		inserter.ReplaceTools(roReg)
	} else {
		schemas := roReg.ToOpenAISchema()
		if len(schemas) > 0 {
			a.SetTools(schemas)
		}
	}
}

func (t *SubAgentTool) resolveAgentName(raw string) (string, error) {
	// Если есть AgentManager — проверяем по нему
	if t.AgentManager != nil {
		// Прямое совпадение
		if _, err := t.AgentManager.GetAgent(raw); err == nil {
			return raw, nil
		}
		// Fuzzy match: ищем содержащий raw в имени или наоборот
		for _, a := range t.availableAgents() {
			if strings.Contains(a.Name, raw) || strings.Contains(raw, a.Name) {
				t.debugLog("Agent name %q fuzzy-matched to %q", raw, a.Name)
				return a.Name, nil
			}
		}
		available := t.availableAgents()
		var names []string
		for _, a := range available {
			names = append(names, a.Name)
		}
		return "", fmt.Errorf("unknown agent: %q. Available: %s", raw, strings.Join(names, ", "))
	}

	// Fallback: старый Flexible name matching
	normalizedName := raw
	switch {
	case strings.Contains(raw, "worker") || strings.Contains(raw, "coder") || strings.Contains(raw, "developer"):
		normalizedName = "worker"
	case strings.Contains(raw, "qa") || strings.Contains(raw, "review") || strings.Contains(raw, "tester"):
		normalizedName = "qa"
	}
	if normalizedName != raw {
		t.debugLog("Agent name %q normalized to %q", raw, normalizedName)
	}
	if normalizedName != "worker" && normalizedName != "qa" {
		return "", fmt.Errorf("unknown agent name: %q, use 'worker' or 'qa'", raw)
	}
	return normalizedName, nil
}

func (t *SubAgentTool) loadSystemPrompt(name string) (string, error) {
	// Если есть AgentManager — используем prompt из конфига
	if t.AgentManager != nil {
		if info, err := t.AgentManager.GetAgent(name); err == nil && info.Prompt != "" {
			return info.Prompt, nil
		}
	}
	// Fallback: читаем из файла system_prompt/<name>.txt
	promptPath := filepath.Join(t.SystemPromptDir, name+".txt")
	data, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("failed to load system prompt for %q from %s: %v", name, promptPath, err)
	}
	return string(data), nil
}

func (t *SubAgentTool) createAgent(name, systemPrompt string) agent.Agent {
	cfg := t.AgentConfig
	cfg.SystemPromptFile = ""
	cfg.SessionConfig = session.Config{
		AutoSave:    false,
		SessionFile: "",
		MaxHistory:  100,
	}
	cfg.EnableLoopAlert = false
	cfg.EnableContextCompression = true
	cfg.MaxToolCalls = 10
	cfg.AgentName = name

	a := agent.NewAgent(cfg)
	a.GetSession(t.PeerID).UpdateSystemPrompt(systemPrompt)
	return a
}

func (t *SubAgentTool) registerMainTools(a agent.Agent) {
	if t.MainTools == nil {
		return
	}
	if inserter, ok := a.(toolInserter); ok {
		inserter.RegisterTools(t.MainTools)
	} else {
		schemas := t.MainTools.ToOpenAISchema()
		if len(schemas) > 0 {
			a.SetTools(schemas)
		}
	}
}

func (t *SubAgentTool) isLeafAgent(name string) bool {
	if t.AgentManager != nil {
		if info, err := t.AgentManager.GetAgent(name); err == nil {
			return info.Leaf
		}
	}
	return false
}

func (t *SubAgentTool) isReviewAgent(name string) bool {
	if t.AgentManager != nil {
		if info, err := t.AgentManager.GetAgent(name); err == nil {
			return info.Review
		}
	}
	return false
}

func (t *SubAgentTool) registerSubAgentTool(name string, a agent.Agent) {
	if t.isLeafAgent(name) {
		return
	}
	subReg := tools.NewRegistry()
	subReg.Register(&SubAgentTool{
		AgentConfig:     t.AgentConfig,
		MainTools:       t.MainTools,
		SystemPromptDir: t.SystemPromptDir,
		AgentManager:    t.AgentManager,
		CurrentDepth:    t.CurrentDepth + 1,
		MaxDepth:        t.MaxDepth,
		PeerID:          t.PeerID,
		ThinkingPeerID:  t.ThinkingPeerID,
		VKClient:        t.VKClient,
		Log:             t.Log,
		Debug:           t.Debug,
		SetActiveAgent:  t.SetActiveAgent,
	})
	if inserter, ok := a.(toolInserter); ok {
		inserter.RegisterTools(subReg)
	} else {
		schemas := subReg.ToOpenAISchema()
		if len(schemas) > 0 {
			a.SetTools(schemas)
		}
	}
}

func (t *SubAgentTool) registerReviewTool(name string, a agent.Agent) {
	if !t.isReviewAgent(name) {
		return
	}
	reviewReg := tools.NewRegistry()
	reviewReg.Register(&tools.ReviewApproveTool{})
	if inserter, ok := a.(toolInserter); ok {
		inserter.RegisterTools(reviewReg)
	} else {
		schemas := reviewReg.ToOpenAISchema()
		if len(schemas) > 0 {
			a.SetTools(schemas)
		}
	}
}

func (t *SubAgentTool) debugLog(format string, args ...interface{}) {
	if !t.Debug {
		return
	}
	if t.Log != nil {
		t.Log.DebugLogf("[SUBAGENT] "+format, args...)
	}
}

func (t *SubAgentTool) makeThinkingCallback(agentName string) func(peerID int64, content string) error {
	return func(peerID int64, content string) error {
		if t.VKClient == nil || t.ThinkingPeerID <= 0 {
			return nil
		}
		prefixed := "[" + agentName + "] " + content
		_, err := t.VKClient.SendThinking(t.ThinkingPeerID, prefixed)
		if err != nil {
			if t.Log != nil {
				t.Log.DebugLogf("[THINKING] Failed to send: %v", err)
			}
		}
		return nil
	}
}
