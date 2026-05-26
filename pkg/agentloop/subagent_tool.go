package agentloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencode/llama-client/pkg/agent"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

type SubAgentTool struct {
	AgentConfig     agent.Config
	MainTools       *tools.Registry
	SystemPromptDir string
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
	return "subagent"
}

func (t *SubAgentTool) Description() string {
	return "Delegate a task to a sub-agent (worker or qa). The sub-agent will complete the task and return the result."
}

func (t *SubAgentTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": tools.CreateStringParameter("name",
				"Agent name: 'worker' (implements tasks) or 'qa' (reviews code, calls worker for fixes)", true),
			"task": tools.CreateStringParameter("task",
				"The task description or code to review. Include full context for the sub-agent.", true),
		},
		"required": []string{"name", "task"},
	}
}

func (t *SubAgentTool) Execute(ctx context.Context, inputs map[string]string) (tools.ToolResult, error) {
	name, ok := inputs["name"]
	if !ok || name == "" {
		return tools.ToolResult{Success: false, Error: "name parameter is required"}, nil
	}
	task, ok := inputs["task"]
	if !ok || task == "" {
		return tools.ToolResult{Success: false, Error: "task parameter is required"}, nil
	}

	if name != "worker" && name != "qa" {
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("unknown agent name: %q, use 'worker' or 'qa'", name)}, nil
	}

	if t.CurrentDepth >= t.MaxDepth {
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("max recursion depth (%d) reached", t.MaxDepth)}, nil
	}

	systemPrompt, err := t.loadSystemPrompt(name)
	if err != nil {
		return tools.ToolResult{Success: false, Error: err.Error()}, nil
	}

	a := t.createAgent(name, systemPrompt)
	t.registerMainTools(a)
	t.registerSubAgentTool(name, a)
	t.registerReviewTool(name, a)
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

func (t *SubAgentTool) loadSystemPrompt(name string) (string, error) {
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
	cfg.EnableContextCompression = false
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

func (t *SubAgentTool) registerSubAgentTool(name string, a agent.Agent) {
	if name == "worker" {
		return
	}
	subReg := tools.NewRegistry()
	subReg.Register(&SubAgentTool{
		AgentConfig:     t.AgentConfig,
		MainTools:       t.MainTools,
		SystemPromptDir: t.SystemPromptDir,
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
	if name != "qa" {
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
