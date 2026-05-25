package agentloop

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/opencode/llama-client/pkg/agent"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

type OrchestratorConfig struct {
	LlamaServerURL  string
	Model           string
	MaxTokens       int
	Temperature     float64
	ToolRegistry    *tools.Registry
	Debug           bool
	Logger          Logger
	ThinkingPeerID  int64
	VKClient        VKClient
	SystemPromptDir string // path to system_prompt/ directory (default: "system_prompt")
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

	o.setActiveAgent("coordinator")
	coordinator := agent.NewAgent(o.makeAgentConfig())
	coordinator.SetThinkingCallback(o.makeThinkingCallback("coordinator"))
	o.addMainTools(coordinator)
	o.registerSubAgentTool(coordinator, peerID)

	result, err := coordinator.ProcessMessage(ctx, task, peerID)
	if err != nil {
		return "", fmt.Errorf("coordinator failed: %w", err)
	}

	elapsed := time.Since(startTime)
	o.debugLog("Agent mode completed. Duration: %v", elapsed)
	return result, nil
}

func (o *Orchestrator) systemPromptDir() string {
	if o.config.SystemPromptDir != "" {
		return o.config.SystemPromptDir
	}
	return "system_prompt"
}

func (o *Orchestrator) makeAgentConfig() agent.Config {
	return agent.Config{
		LlamaServerURL: o.config.LlamaServerURL,
		Model:          o.config.Model,
		MaxTokens:      o.config.MaxTokens,
		Temperature:    o.config.Temperature,
		SystemPromptFile: o.systemPromptDir() + "/coordinator.txt",
		EnableTools:    true,
		MaxToolCalls:   10,
		EnableLoopAlert: false,
		EnableContextCompression: false,
		Debug:          o.config.Debug,
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

type toolInserter interface {
	RegisterTools(registry *tools.Registry)
}

func (o *Orchestrator) registerSubAgentTool(a agent.Agent, peerID int64) {
	subReg := tools.NewRegistry()
	subReg.Register(&SubAgentTool{
		AgentConfig:     o.makeAgentConfig(),
		MainTools:       o.config.ToolRegistry,
		SystemPromptDir: o.systemPromptDir(),
		CurrentDepth:    0,
		MaxDepth:        2,
		PeerID:          peerID,
		ThinkingPeerID:  o.thoughtPeer,
		VKClient:        o.config.VKClient,
		Log:             o.config.Logger,
		Debug:           o.config.Debug,
		SetActiveAgent:  func(name string) { o.setActiveAgent(name) },
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
