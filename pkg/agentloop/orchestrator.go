package agentloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/opencode/llama-client/pkg/agent"
	"github.com/opencode/llama-client/pkg/agentpolicy"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

type OrchestratorConfig struct {
	LlamaServerURL       string
	Model                string
	MaxTokens            int
	Temperature          float64
	ToolRegistry         *tools.Registry
	Debug                bool
	Logger               Logger
	ThinkingPeerID       int64
	VKClient             VKClient
	SystemPromptDir      string
	MaxReviewIterations  int
	AgentManager         *agentpolicy.AgentManager
}

type Orchestrator struct {
	config      OrchestratorConfig
	thoughtPeer int64
	activeAgent string
	activeMu    sync.RWMutex
}

func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	return &Orchestrator{
		config:      cfg,
		thoughtPeer: cfg.ThinkingPeerID,
	}
}

func (o *Orchestrator) GetCurrentAgent() string {
	o.activeMu.RLock()
	defer o.activeMu.RUnlock()
	return o.activeAgent
}

func (o *Orchestrator) setActiveAgent(name string) {
	o.activeMu.Lock()
	o.activeAgent = name
	o.activeMu.Unlock()
}

func (o *Orchestrator) ExecuteTask(ctx context.Context, task string, peerID int64) (string, error) {
	o.debugLog("Mode activated. Task: %s", truncate(task, 200))
	startTime := time.Now()
	defer o.setActiveAgent("")

	o.debugLog("Starting worker...")
	o.setActiveAgent("worker")
	workerResult, err := o.runWorker(ctx, task, peerID)
	if err != nil {
		return "", fmt.Errorf("worker failed: %w", err)
	}
	o.debugLog("Worker completed: %d chars", len(workerResult))

	o.debugLog("Starting QA review...")
	o.setActiveAgent("qa")
	qaPrompt := fmt.Sprintf("Review the following implementation result:\n\n%s\n\nBuild and test the code. If issues found, fix them and approve when done.", workerResult)
	qaResult, err := o.runQA(ctx, qaPrompt, peerID)
	if err != nil {
		o.debugLog("QA failed, returning worker result: %v", err)
		return workerResult, nil
	}
	o.debugLog("QA completed: %d chars", len(qaResult))

	elapsed := time.Since(startTime)
	o.debugLog("Agent mode completed. Duration: %v", elapsed)
	return qaResult, nil
}

func (o *Orchestrator) RunAgent(ctx context.Context, agentName, task string, peerID int64) (string, error) {
	o.debugLog("RunAgent: %s. Task: %s", agentName, truncate(task, 200))
	o.setActiveAgent(agentName)
	defer o.setActiveAgent("")

	prompt, err := o.loadSystemPrompt(agentName)
	if err != nil {
		return "", fmt.Errorf("failed to load prompt for agent %q: %w", agentName, err)
	}

	if o.config.MaxReviewIterations > 0 {
		prompt += fmt.Sprintf("\n\nMaximum review iterations: %d. After this many developer↔reviewer cycles, move forward regardless.", o.config.MaxReviewIterations)
	}

	a := o.makeSubAgent(agentName, prompt, peerID)

	switch {
	case o.isCoordinator(agentName):
		o.debugLog("[TOOL] Coordinator mode for %s: read-only + task tool", agentName)
		o.addReadOnlyTools(a)
		o.registerSubAgentTool(agentName, a, peerID)
	
	case o.isReviewAgent(agentName):
		o.debugLog("[TOOL] Review mode for %s: read-only + approve tool", agentName)
		o.addReadOnlyTools(a)
		o.registerReviewTool(a)
	default:
		o.debugLog("[TOOL] Full mode for %s: all tools", agentName)
		o.addMainTools(a)
		if !o.isLeafAgent(agentName) {
			o.debugLog("[TOOL] Adding subagent tool for %s", agentName)
			o.registerSubAgentTool(agentName, a, peerID)
		} else {
			o.debugLog("[TOOL] %s is leaf — no subagent tool", agentName)
		}
	}

	response, err := a.ProcessMessage(ctx, task, peerID)
	if err != nil {
		return "", fmt.Errorf("agent %q failed: %w", agentName, err)
	}

	return response, nil
}

func (o *Orchestrator) ListAgentNames() []string {
	if o.config.AgentManager != nil {
		return o.config.AgentManager.ListAgentNames()
	}
	return nil
}

func (o *Orchestrator) isLeafAgent(name string) bool {
	if o.config.AgentManager != nil {
		if info, err := o.config.AgentManager.GetAgent(name); err == nil {
			return info.Leaf
		}
	}
	return false
}

func (o *Orchestrator) isReviewAgent(name string) bool {
	if o.config.AgentManager != nil {
		if info, err := o.config.AgentManager.GetAgent(name); err == nil {
			return info.Review
		}
	}
	return false
}

func (o *Orchestrator) isCoordinator(name string) bool {
	if o.config.AgentManager != nil {
		info, err := o.config.AgentManager.GetAgent(name)
		if err != nil {
			o.debugLog("isCoordinator(%q): GetAgent error: %v", name, err)
			return false
		}
		o.debugLog("isCoordinator(%q): Coordinator=%v Leaf=%v Review=%v", name, info.Coordinator, info.Leaf, info.Review)
		return info.Coordinator
	}
	o.debugLog("isCoordinator(%q): AgentManager is nil", name)
	return false
}

func (o *Orchestrator) runWorker(ctx context.Context, task string, peerID int64) (string, error) {
	prompt, err := o.loadSystemPrompt("worker")
	if err != nil {
		return "", err
	}
	a := o.makeSubAgent("worker", prompt, peerID)
	o.addMainTools(a)
	return a.ProcessMessage(ctx, task, peerID)
}

func (o *Orchestrator) runQA(ctx context.Context, task string, peerID int64) (string, error) {
	prompt, err := o.loadSystemPrompt("qa")
	if err != nil {
		return "", err
	}
	a := o.makeSubAgent("qa", prompt, peerID)
	o.addMainTools(a)
	o.registerSubAgentTool("qa", a, peerID)
	return a.ProcessMessage(ctx, task, peerID)
}

func (o *Orchestrator) makeSubAgent(name, systemPrompt string, peerID int64) agent.Agent {
	cfg := o.makeAgentConfig()
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

	// Устанавливаем permission checker из AgentManager
	if o.config.AgentManager != nil {
		if info, err := o.config.AgentManager.GetAgent(name); err == nil && info.Permission != nil && len(info.Permission) > 0 {
			a.SetPermissionChecker(agentpolicy.NewPermissionAdapter(info.Permission))
		}
	}

	a.SetThinkingCallback(o.makeThinkingCallback(name))
	a.GetSession(peerID).UpdateSystemPrompt(systemPrompt)
	return a
}

func (o *Orchestrator) loadSystemPrompt(name string) (string, error) {
	if o.config.AgentManager != nil {
		if info, err := o.config.AgentManager.GetAgent(name); err == nil && info.Prompt != "" {
			return info.Prompt, nil
		}
	}
	path := filepath.Join(o.systemPromptDir(), name+".txt")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to load prompt for %q: %v", name, err)
	}
	return string(data), nil
}

func (o *Orchestrator) systemPromptDir() string {
	if o.config.SystemPromptDir != "" {
		return o.config.SystemPromptDir
	}
	return "system_prompt"
}

func (o *Orchestrator) makeAgentConfig() agent.Config {
	return agent.Config{
		LlamaServerURL:            o.config.LlamaServerURL,
		Model:                     o.config.Model,
		MaxTokens:                 o.config.MaxTokens,
		Temperature:               o.config.Temperature,
		SystemPromptFile:          o.systemPromptDir() + "/coordinator.txt",
		EnableTools:               true,
		MaxToolCalls:              10,
		EnableLoopAlert:           false,
		EnableContextCompression:  false,
		Debug:                     o.config.Debug,
		AgentName:                 "coordinator",
		SessionConfig: session.Config{
			AutoSave:    false,
			SessionFile: "",
			MaxHistory:  100,
		},
	}
}

func (o *Orchestrator) addMainTools(a agent.Agent) {
	reg := o.config.ToolRegistry
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

func (o *Orchestrator) addReadOnlyTools(a agent.Agent) {
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
	roReg.Register(tools.GlobalTodo)
	if inserter, ok := a.(toolInserter); ok {
		inserter.ReplaceTools(roReg)
	} else {
		schemas := roReg.ToOpenAISchema()
		if len(schemas) > 0 {
			a.SetTools(schemas)
		}
	}
}

type toolInserter interface {
	RegisterTools(registry *tools.Registry)
	ReplaceTools(registry *tools.Registry)
}

func (o *Orchestrator) registerSubAgentTool(name string, a agent.Agent, peerID int64) {
	if o.isLeafAgent(name) {
		return
	}
	subReg := tools.NewRegistry()
	subReg.Register(&SubAgentTool{
		AgentConfig:     o.makeAgentConfig(),
		MainTools:       o.config.ToolRegistry,
		SystemPromptDir: o.systemPromptDir(),
		AgentManager:    o.config.AgentManager,
		CurrentDepth:    0,
		MaxDepth:        2,
		PeerID:          peerID,
		ThinkingPeerID:  o.thoughtPeer,
		VKClient:        o.config.VKClient,
		Log:             o.config.Logger,
		Debug:           o.config.Debug,
		SetActiveAgent:  func(n string) { o.setActiveAgent(n) },
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

func (o *Orchestrator) registerReviewTool(a agent.Agent) {
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

func (o *Orchestrator) makeThinkingCallback(agentName string) func(peerID int64, content string) error {
	return func(peerID int64, content string) error {
		if o.config.VKClient == nil || o.thoughtPeer <= 0 {
			return nil
		}
		prefixed := "[" + agentName + "] " + content
		_, err := o.config.VKClient.SendThinking(o.thoughtPeer, prefixed)
		return err
	}
}

func (o *Orchestrator) debugLog(format string, args ...interface{}) {
	if !o.config.Debug {
		return
	}
	if o.config.Logger != nil {
		o.config.Logger.DebugLogf("[AGENT] "+format, args...)
	}
}

