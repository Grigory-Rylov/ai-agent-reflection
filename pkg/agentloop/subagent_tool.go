package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agent"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentpolicy"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type SubAgentTool struct {
	AgentConfig     agent.Config
	ContextResolver *ModelContextResolver
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
	ModelHolder     *modelsconfig.Holder
	SetActiveAgent  func(name string)
	Store           store.Store 
	ParentSessionID  string      
	ParentAgent      agent.Agent 
	AgentSessionID   string      
	Chain            []string    
	ParentAgentName  string      
	AllowedSubagents []string    
	SlotManager      *SlotManager
	Slots            *SlotClient
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
			if !a.Hidden && !a.Internal {
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

	a, err := t.createAgent(name, systemPrompt, task)
	if err != nil {
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("failed to create sub-agent %q: %v", name, err)}, nil
	}

	
	
	if t.Store != nil {
		t.saveParentHistory()
	}

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

	defer func() {
		if r := recover(); r != nil {
			t.cancelAgentSession()
			panic(r)
		}
	}()

	response, err := a.ProcessMessage(ctx, task, t.PeerID)
	if err != nil {
		
		
		if t.Store != nil {
			t.saveSessionHistory(a, t.AgentSessionID, task)
		}
		t.cancelAgentSession()
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("sub-agent %q failed: %v", name, err)}, nil
	}

	if t.Store != nil {
		t.saveSessionHistory(a, t.AgentSessionID, task)
	}
	
	
	t.completeAgentSession()

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
	var resolved string
	if t.AgentManager != nil {
		if _, err := t.AgentManager.GetAgent(raw); err == nil {
			resolved = raw
		} else {
			for _, a := range t.availableAgents() {
				if strings.Contains(a.Name, raw) || strings.Contains(raw, a.Name) {
					t.debugLog("Agent name %q fuzzy-matched to %q", raw, a.Name)
					resolved = a.Name
					break
				}
			}
			if resolved == "" {
				available := t.availableAgents()
				var names []string
				for _, a := range available {
					names = append(names, a.Name)
				}
				return "", fmt.Errorf("unknown agent: %q. Available: %s", raw, strings.Join(names, ", "))
			}
		}
	} else {
		return "", fmt.Errorf("cannot resolve agent %q: AgentManager not configured", raw)
	}
	if err := t.checkAllowedSubagent(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func (t *SubAgentTool) loadSystemPrompt(name string) (string, error) {
	if t.AgentManager != nil {
		if info, err := t.AgentManager.GetAgent(name); err == nil && info.Prompt != "" {
			return info.Prompt, nil
		}
	}
	for _, ext := range []string{".txt", ".md"} {
		promptPath := filepath.Join(t.SystemPromptDir, name+ext)
		data, err := os.ReadFile(promptPath)
		if err == nil {
			return string(data), nil
		}
	}
	return "", fmt.Errorf("failed to load system prompt for %q from %s", name, t.SystemPromptDir)
}

func (t *SubAgentTool) createAgent(name, systemPrompt, task string) (agent.Agent, error) {
	cfg := t.AgentConfig

	if t.ModelHolder != nil {
		_, modelName, llamaURL := t.ModelHolder.GetCurrent()
		cfg.LlamaServerURL = llamaURL
		cfg.Model = modelName
	}

	if t.ContextResolver != nil {
		ctx, err := t.ContextResolver.Resolve()
		if err != nil {
			return nil, err
		}
		cfg.MaxTokens = ctx
	}

	cfg.SystemPromptFile = ""
	cfg.SessionConfig = session.Config{
		SessionFile: "",
	}
	cfg.EnableLoopAlert = false
	cfg.EnableCompression = true
	cfg.AgentName = name
	cfg.SlotID = -1 
	cfg.SlotSave = false

	
	
	
	
	
	
	
	t.AgentSessionID = t.generateUUID()
	sessionID := t.AgentSessionID
	cfg.SessionConfig.SessionID = sessionID

	
	
	
	
	
	if t.ModelHolder != nil && t.ModelHolder.GetCurrentSlotSave() {
		cfg.SlotSave = true
		if slotID := AssignSessionSlot(t.SlotManager, t.Slots, t.ModelHolder, sessionID, t.Log); slotID >= 0 {
			cfg.SlotID = slotID
			cfg.SlotSaver = NewSlotSaver(t.SlotManager, t.Slots, t.ModelHolder, sessionID, t.Log)
			if t.Log != nil {
				t.Log.InfoLogf("[SLOT] assigned slot %d to sub-agent %s (session %s)", slotID, name, sessionID)
			}
		}
	}

	a := agent.NewAgent(cfg)
	a.GetSession(t.PeerID).UpdateSystemPrompt(systemPrompt)

	
	if t.Store != nil {
		
		chain := make([]string, len(t.Chain))
		copy(chain, t.Chain)
		chain = append(chain, sessionID)
		t.Chain = chain

		
		t.Store.SaveAgentSession(&store.AgentSessionData{
			ID:           sessionID,
			ParentID:     t.ParentSessionID,
			AgentName:    name,
			PeerID:       t.PeerID,
			SystemPrompt: systemPrompt,
			LastPrompt:   task,
			Status:       "active",
		})

		
		t.Store.SaveAgentChain(t.PeerID, chain)
	}

	return a, nil
}

func (t *SubAgentTool) registerMainTools(a agent.Agent) {
	reg := mainToolsWithoutTask(t.MainTools)
	if reg == nil {
		return
	}
	if inserter, ok := a.(toolInserter); ok {
		inserter.RegisterTools(reg)
	} else {
		schemas := reg.ToOpenAISchema()
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
		ContextResolver: t.ContextResolver,
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
		ModelHolder:     t.ModelHolder,
		SetActiveAgent:  t.SetActiveAgent,
		Store:           t.Store,
		ParentSessionID: t.AgentSessionID,
		ParentAgent:     a,
		Chain:           t.Chain,
		ParentAgentName: name,
		AllowedSubagents: t.AgentManager.SubagentTypesFor(name),
		SlotManager:     t.SlotManager,
		Slots:           t.Slots,
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


func newSessionUUID(parts ...string) string {
	h := fnv.New128a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{'-'})
	}
	h.Write([]byte(strconv.FormatInt(time.Now().UnixNano(), 10)))
	sum := h.Sum(nil)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}


func (t *SubAgentTool) generateUUID() string {
	return newSessionUUID(t.ParentSessionID, strconv.Itoa(t.CurrentDepth), strconv.FormatInt(t.PeerID, 10))
}


func (t *SubAgentTool) saveSessionHistory(a agent.Agent, sessionID, task string) {
	if t.Store == nil || sessionID == "" || a == nil {
		return
	}
	data, err := json.Marshal(a.GetSession(t.PeerID).GetHistory())
	if err != nil {
		if t.Log != nil {
			t.Log.DebugLogf("[SUBAGENT] save history marshal failed: %v", err)
		}
		return
	}
	if err := t.Store.UpdateAgentSession(sessionID, task, string(data)); err != nil {
		if t.Log != nil {
			t.Log.DebugLogf("[SUBAGENT] save history failed: %v", err)
		}
	}
}


func (t *SubAgentTool) saveParentHistory() {
	if t.Store == nil || t.ParentAgent == nil || t.ParentSessionID == "" {
		return
	}
	data, err := json.Marshal(t.ParentAgent.GetSession(t.PeerID).GetHistory())
	if err != nil {
		if t.Log != nil {
			t.Log.DebugLogf("[SUBAGENT] save parent history marshal failed: %v", err)
		}
		return
	}
	lastPrompt := ""
	if sd, err := t.Store.GetAgentSession(t.ParentSessionID); err == nil && sd != nil {
		lastPrompt = sd.LastPrompt
	}
	if err := t.Store.UpdateAgentSession(t.ParentSessionID, lastPrompt, string(data)); err != nil {
		if t.Log != nil {
			t.Log.DebugLogf("[SUBAGENT] save parent history failed: %v", err)
		}
	}
}


func (t *SubAgentTool) completeAgentSession() {
	t.cleanupAgentSession()
	if t.Store == nil || t.AgentSessionID == "" {
		return
	}
	t.Store.DeleteAgentSession(t.AgentSessionID)
	t.popChain()
}


func (t *SubAgentTool) cancelAgentSession() {
	t.cleanupAgentSession()
	if t.Store == nil || t.AgentSessionID == "" {
		return
	}
	t.Store.DeleteAgentSession(t.AgentSessionID)
	t.popChain()
}


func (t *SubAgentTool) cleanupAgentSession() {
	ReleaseSessionSlot(t.SlotManager, t.Slots, t.ModelHolder, t.AgentSessionID, t.Log)
}


func (t *SubAgentTool) popChain() {
	parent := t.Chain
	if len(parent) > 0 {
		parent = parent[:len(parent)-1]
	}
	t.Store.SaveAgentChain(t.PeerID, parent)
}


func mainToolsWithoutTask(reg *tools.Registry) *tools.Registry {
	if reg == nil {
		return nil
	}
	if !reg.IsRegistered("task") {
		return reg
	}
	filtered := tools.NewRegistry()
	for _, tool := range reg.GetAll() {
		if tool.Name() != "task" {
			filtered.Register(tool)
		}
	}
	return filtered
}


func (t *SubAgentTool) checkAllowedSubagent(name string) error {
	if len(t.AllowedSubagents) == 0 {
		return nil
	}
	for _, a := range t.AllowedSubagents {
		if a == name {
			return nil
		}
	}
	return fmt.Errorf("agent %q cannot delegate to %q: only %v allowed", t.ParentAgentName, name, t.AllowedSubagents)
}
