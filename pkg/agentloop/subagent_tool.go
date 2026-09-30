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
	"unicode"
	"unicode/utf8"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agent"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agentpolicy"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type SubAgentTool struct {
	AgentConfig      agent.Config
	ContextResolver  *ModelContextResolver
	MainTools        *tools.Registry
	SystemPromptDir  string
	AgentManager     *agentpolicy.AgentManager
	CurrentDepth     int
	MaxDepth         int
	PeerID           int64
	ThinkingPeerID   int64
	VKClient         VKClient
	Log              Logger
	Debug            bool
	ModelHolder      *modelsconfig.Holder
	SetActiveAgent   func(name string)
	Store            store.Store
	ParentSessionID  string
	ParentAgent      agent.Agent
	AgentSessionID   string
	Chain            []string
	ParentAgentName  string
	AllowedSubagents []string
	SlotManager      *SlotManager
	Slots            *SlotClient
	BGOwner          string
	MaxParallel      int
	NoSlotSave       bool
	spawnNonce       string
	Blocking         bool
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
	b.WriteString("7. If the agent description mentions that it should be used proactively, use it without user asking\n")
	b.WriteString("8. The result is structured: status (success|failure|partial), summary, files, next\n\n")
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
			"subagent_type": tools.CreateStringParameter("subagent_type", nameDesc, false),
			"name":          tools.CreateStringParameter("name", "Alias for subagent_type.", false),
			"prompt":        tools.CreateStringParameter("prompt", "Task for a single agent. Include full context.", false),
			"task":          tools.CreateStringParameter("task", "Alias for prompt.", false),
			"description":   tools.CreateStringParameter("description", "Short (3-5 words) description of the task.", false),
			"context":       tools.CreateStringParameter("context", "Shared context prepended to every task in a batch call.", false),
			"tasks": map[string]interface{}{
				"type":        "array",
				"description": "Run multiple subagents in parallel; results are aggregated. Each item: {subagent_type, prompt}.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"subagent_type": tools.CreateStringParameter("subagent_type", nameDesc, false),
						"prompt":        tools.CreateStringParameter("prompt", "Task for this agent.", true),
					},
					"required": []string{"subagent_type", "prompt"},
				},
			},
		},
	}
}

func (t *SubAgentTool) Execute(ctx context.Context, inputs map[string]string) (tools.ToolResult, error) {
	asyncMode := !t.Blocking
	if v, ok := inputs["async"]; ok && strings.TrimSpace(v) != "" {
		asyncMode = truthy(v)
	}
	if v, ok := inputs["blocking"]; ok && strings.TrimSpace(v) != "" {
		asyncMode = !truthy(v)
	}
	if raw := strings.TrimSpace(inputs["tasks"]); raw != "" {
		if !asyncMode {
			return t.runBatch(ctx, inputs["context"], raw), nil
		}
		return t.startAsyncBatch(ctx, inputs["context"], raw), nil
	}
	name := firstNonEmpty(inputs["subagent_type"], inputs["name"], inputs["agent"], inputs["type"])
	task := firstNonEmpty(inputs["prompt"], inputs["task"], inputs["description"], inputs["instruction"], inputs["title"])
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
	if looksLikePlaceholderTask(task) {
		return tools.ToolResult{Success: false,
			Error: "task parameter looks like a placeholder, not a real instruction for the subagent"}, nil
	}
	if !asyncMode {
		return t.runSpawn(ctx, name, task), nil
	}
	id := t.startAsyncJob(ctx, batchTask{SubagentType: name, Prompt: task}, inputs["context"])
	if id == "" {
		return tools.ToolResult{Success: false,
			Error: fmt.Sprintf("background subagent limit reached (%d running). Wait for a running subagent to finish or cancel one.", maxRunningJobs)}, nil
	}
	return tools.ToolResult{Success: true, Data: map[string]interface{}{
		"status": "started",
		"ids":    []string{id},
		"note":   "subagent launched in the background; its result resumes your turn when it finishes, or use the subagents tool with action=wait and this id",
	}}, nil
}

func (t *SubAgentTool) startAsyncBatch(ctx context.Context, globalContext, rawTasks string) tools.ToolResult {
	var tasks []batchTask
	if err := json.Unmarshal([]byte(rawTasks), &tasks); err != nil {
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("failed to parse tasks array: %v", err)}
	}
	if len(tasks) == 0 {
		return tools.ToolResult{Success: false, Error: "tasks array is empty"}
	}
	if bad := firstBadTask(tasks); bad != "" {
		return tools.ToolResult{Success: false, Error: bad}
	}
	ids := make([]string, 0, len(tasks))
	skipped := 0
	for _, item := range tasks {
		id := t.startAsyncJob(ctx, item, globalContext)
		if id == "" {
			skipped++
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return tools.ToolResult{Success: false,
			Error: fmt.Sprintf("background subagent limit reached (%d running). Wait for a running subagent to finish or cancel one.", maxRunningJobs)}
	}
	data := map[string]interface{}{"status": "started", "count": len(ids), "ids": ids}
	if skipped > 0 {
		data["skipped"] = skipped
		data["note"] = fmt.Sprintf("%d task(s) not started: background subagent limit reached (%d running). Spawn the rest once capacity frees.", skipped, maxRunningJobs)
	} else {
		data["note"] = "subagents launched in the background; results resume your turn as they finish, or use the subagents tool with action=wait to collect them"
	}
	return tools.ToolResult{Success: true, Data: data}
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes":
		return true
	}
	return false
}

func (t *SubAgentTool) runSpawn(ctx context.Context, rawName, task string) tools.ToolResult {
	if looksLikePlaceholderTask(task) {
		return tools.ToolResult{Success: false,
			Error: "task parameter looks like a placeholder, not a real instruction for the subagent"}
	}
	name, err := t.resolveAgentName(rawName)
	if err != nil {
		return tools.ToolResult{Success: false, Error: err.Error()}
	}
	if t.CurrentDepth >= t.MaxDepth {
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("max recursion depth (%d) reached", t.MaxDepth)}
	}
	systemPrompt, err := t.loadSystemPrompt(name)
	if err != nil {
		return tools.ToolResult{Success: false, Error: err.Error()}
	}
	a, err := t.createAgent(name, systemPrompt, task)
	if err != nil {
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("failed to create sub-agent %q: %v", name, err)}
	}
	if t.Store != nil {
		t.saveParentHistory()
	}
	t.applyAgentPermissions(name, a)
	t.registerChildTools(name, a)
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
	spawnCtx, cancelSpawn := context.WithCancel(ctx)
	defer cancelSpawn()
	spawnID := subagentRegistry.register(name, t.PeerID, t.CurrentDepth, cancelSpawn)
	defer subagentRegistry.finish(spawnID)
	response, err := a.ProcessMessage(spawnCtx, task, t.PeerID)
	if err != nil {
		if t.Store != nil {
			t.saveSessionHistory(a, t.AgentSessionID, task)
		}
		t.cancelAgentSession()
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("sub-agent %q failed: %v", name, err)}
	}
	if t.Store != nil {
		t.saveSessionHistory(a, t.AgentSessionID, task)
	}
	t.completeAgentSession()
	return t.buildResult(name, response)
}

func (t *SubAgentTool) registerChildTools(name string, a agent.Agent) {
	if t.isReviewAgent(name) {
		t.registerReadOnlyTools(a)
		t.registerReviewTool(name, a)
		return
	}
	t.registerMainTools(a)
	t.registerSubAgentTool(name, a)
	t.registerReviewTool(name, a)
}

func (t *SubAgentTool) buildResult(name, response string) tools.ToolResult {
	res := ParseSubAgentResult(response)
	if res.Status == "failure" {
		return tools.ToolResult{
			Success: false,
			Error:   fmt.Sprintf("sub-agent %q reported failure: %s", name, res.Summary),
			Data: map[string]interface{}{
				"status":  res.Status,
				"summary": res.Summary,
				"files":   res.Files,
				"next":    res.Next,
			},
		}
	}
	return tools.ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"status":   res.Status,
			"summary":  res.Summary,
			"files":    res.Files,
			"next":     res.Next,
			"response": response,
		},
	}
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
	roReg.Register(&tools.ShellBackgroundTool{})
	roReg.Register(&tools.ShellCheckTool{})
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
	cfg.BGOwner = sessionID
	cfg.SubagentWatch = jobWatcher{}
	if t.BGOwner != "" {
		cfg.BGParentOwner = t.BGOwner
	}

	if t.ModelHolder != nil && t.ModelHolder.GetCurrentSlotSave() && !t.NoSlotSave {
		cfg.SlotSave = true
		if slotID := AssignSessionSlot(t.SlotManager, t.Slots, t.ModelHolder, sessionID, t.Log); slotID >= 0 {
			cfg.SlotID = slotID
			cfg.SlotSaver = NewSlotSaver(t.SlotManager, t.Slots, t.ModelHolder, sessionID, t.Log)
			if t.Log != nil {
				t.Log.InfoLogf("[SLOT] assigned slot %d to sub-agent %s (session %s)", slotID, name, sessionID)
			}
		}
	}

	var a agent.Agent = agent.NewAgent(cfg)
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

	if t.Store != nil && t.AgentSessionID != "" {
		if cs, ok := a.(agent.CheckpointSetter); ok {
			cs.SetCheckpoint(func(lastToolCall string) { t.persistChildCheckpoint(a, lastToolCall) })
		}
	}

	t.registerBGDelivery(name, a)

	return a, nil
}

func (t *SubAgentTool) persistChildCheckpoint(a agent.Agent, lastToolCall string) {
	data, err := json.Marshal(a.GetSession(t.PeerID).GetHistory())
	if err != nil {
		if t.Log != nil {
			t.Log.DebugLogf("[SUBAGENT] checkpoint marshal for %s failed: %v", t.AgentSessionID, err)
		}
		return
	}
	if err := t.Store.SaveAgentCheckpoint(t.AgentSessionID, lastToolCall, string(data)); err != nil {
		if t.Log != nil {
			t.Log.DebugLogf("[SUBAGENT] checkpoint save for %s failed: %v", t.AgentSessionID, err)
		}
	}
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

var placeholderTaskWords = map[string]bool{
	"placeholder": true,
	"todo":        true,
	"tbd":         true,
	"fixme":       true,
}

func looksLikePlaceholderTask(task string) bool {
	trimmed := strings.TrimSpace(task)
	if trimmed == "" {
		return true
	}
	if utf8.RuneCountInString(trimmed) <= 1 {
		return true
	}
	if placeholderTaskWords[strings.ToLower(trimmed)] {
		return true
	}
	for _, r := range trimmed {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func (t *SubAgentTool) TargetsReviewAgent(args map[string]string) bool {
	name := args["subagent_type"]
	if name == "" {
		name = args["name"]
	}
	if name == "" {
		name = args["agent"]
	}
	if name == "" {
		name = args["type"]
	}
	if name == "" {
		return false
	}
	if strings.EqualFold(name, "reviewer") {
		return true
	}
	return t.isReviewAgent(name)
}
func (t *SubAgentTool) registerSubAgentTool(name string, a agent.Agent) {
	if t.isLeafAgent(name) {
		return
	}
	subReg := tools.NewRegistry()
	subReg.Register(&SubAgentTool{
		AgentConfig:      t.AgentConfig,
		ContextResolver:  t.ContextResolver,
		MainTools:        t.MainTools,
		SystemPromptDir:  t.SystemPromptDir,
		AgentManager:     t.AgentManager,
		CurrentDepth:     t.CurrentDepth + 1,
		MaxDepth:         t.MaxDepth,
		Blocking:         t.Blocking,
		PeerID:           t.PeerID,
		ThinkingPeerID:   t.ThinkingPeerID,
		VKClient:         t.VKClient,
		Log:              t.Log,
		Debug:            t.Debug,
		ModelHolder:      t.ModelHolder,
		SetActiveAgent:   t.SetActiveAgent,
		Store:            t.Store,
		ParentSessionID:  t.AgentSessionID,
		ParentAgent:      a,
		Chain:            t.Chain,
		ParentAgentName:  name,
		AllowedSubagents: t.AgentManager.SubagentTypesFor(name),
		SlotManager:      t.SlotManager,
		Slots:            t.Slots,
		BGOwner:          t.AgentSessionID,
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

func (t *SubAgentTool) registerBGDelivery(name string, a agent.Agent) {
	hub := tools.GetBackgroundHub()
	if hub == nil || t.AgentSessionID == "" {
		return
	}
	hub.SetDelivery(t.AgentSessionID, t.makeBGDelivery(name, a))
}

func (t *SubAgentTool) makeBGDelivery(name string, a agent.Agent) func(peerID int64, text string) {
	return func(peerID int64, text string) {
		if sess := a.GetSession(t.PeerID); sess != nil {
			if in := sess.GetPeerInput(); in != nil {
				in.Admit(text)
			}
		}
		if t.VKClient != nil && t.ThinkingPeerID > 0 {
			if _, err := t.VKClient.SendThinking(t.ThinkingPeerID, "["+name+"] "+text); err != nil && t.Log != nil {
				t.Log.DebugLogf("[BG] thinking delivery for sub-agent %s failed: %v", name, err)
			}
		}
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
	return newSessionUUID(t.ParentSessionID, strconv.Itoa(t.CurrentDepth), strconv.FormatInt(t.PeerID, 10), t.spawnNonce)
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
	if t.AgentSessionID == "" {
		return
	}
	if hub := tools.GetBackgroundHub(); hub != nil {
		hub.ReleasePending(t.AgentSessionID)
		hub.UnregisterDelivery(t.AgentSessionID)
	}
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
