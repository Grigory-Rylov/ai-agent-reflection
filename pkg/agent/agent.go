package agent

import (
	"context"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)


type Agent interface {
	
	ProcessMessage(ctx context.Context, message string, peerID int64) (string, error)

	
	ResetSession(peerID int64)

	
	GetSession(peerID int64) *session.Session

	
	SetThinkingCallback(cb ThinkingCallback)

	
	SetTools(tools []map[string]interface{})

	
	SetToolExecutor(executor ToolExecutor)
}


type Config struct {
	
	LlamaServerURL string

	EngineType string

	Model string
	
	MaxTokens int
	
	Temperature float64
	
	SessionConfig session.Config
	
	SystemPromptFile string
	
	EnableLoopAlert bool
	
	EnableTools bool
	
	EnableCompression bool

	SummarizeReasoning bool

	TailTurns int

	PreserveRecentTokens *int

	CompactionReserved *int
	
	ModelLimitInput int
	
	EnablePruning bool
	
	ToolOutputMaxLines int
	
	ToolOutputMaxBytes int
	
	Debug bool
	
	AgentName string
	
	PromptsDir string
	
	Mode string
	
	ToolsList []string
	
	
	SkipShellPermissionForPathless bool
	
	RetryDelay time.Duration
	
	StreamIdleTimeout time.Duration
	
	MaxToolCallDepth int
	
	SlotID int
	
	SlotSave bool
	
	
	
	
	SlotSaver SlotSaver

	BGOwner       string
	BGParentOwner string
}


type SlotSaver interface {
	SaveSlot(ctx context.Context)
}


func DefaultConfig() Config {
	return Config{
		LlamaServerURL:    "127.0.0.1:8081",
		Model:             "local-model",
		MaxTokens:         4096,
		Temperature:       0.7,
		SessionConfig:     session.DefaultConfig(),
		EnableLoopAlert:   true,
		EnableTools:       true,
		EnableCompression: true,
		TailTurns:         2,
		EnablePruning:     true,
		RetryDelay:        5 * time.Second,
		StreamIdleTimeout: DefaultStreamIdleTimeout,
		MaxToolCallDepth:  maxToolCallDepth,
		SlotID:            -1,
	}
}
