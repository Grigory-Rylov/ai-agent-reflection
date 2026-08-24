package agentloop

import (
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agent"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/engine"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type LoopConfig struct {
	ModelHolder     *modelsconfig.Holder
	ContextResolver *ModelContextResolver
	MaxTokens       int
	Temperature     float64
	Engine          engine.Control

	StreamIdleTimeout time.Duration

	SessionConfig                  session.Config
	SystemPromptFile               string
	EnableLoopDetection            bool
	LoopThreshold                  float64
	EnableTools                    bool
	ToolTimeout                    time.Duration
	ThinkingPeerID                 int64
	EnableThinking                 bool
	EnableLogging                  bool
	Logger                         Logger
	Debug                          bool
	EnableCompression              bool
	TailTurns                      int
	PreserveRecentTokens           *int
	CompactionReserved             *int
	ModelLimitInput                int
	MaxToolCallDepth               int
	EnablePruning                  bool
	ToolOutputMaxLines             int
	ToolOutputMaxBytes             int
	SkipShellPermissionForPathless bool
}

func DefaultLoopConfig() LoopConfig {
	return LoopConfig{
		MaxTokens:                      4096,
		Temperature:                    0.7,
		StreamIdleTimeout:              agent.DefaultStreamIdleTimeout,
		SessionConfig:                  session.DefaultConfig(),
		EnableLoopDetection:            true,
		LoopThreshold:                  0.85,
		EnableTools:                    true,
		ToolTimeout:                    30 * time.Second,
		ThinkingPeerID:                 0,
		EnableThinking:                 false,
		EnableLogging:                  true,
		EnableCompression:              true,
		TailTurns:                      2,
		PreserveRecentTokens:           nil,
		CompactionReserved:             nil,
		EnablePruning:                  true,
		ToolOutputMaxLines:             tools.DefaultToolOutputMaxLines,
		ToolOutputMaxBytes:             tools.DefaultToolOutputMaxBytes,
		SkipShellPermissionForPathless: false,
	}
}

type Logger interface {
	DebugLog(msg string, args ...interface{})
	InfoLog(msg string, args ...interface{})
	WarnLog(msg string, args ...interface{})
	ErrorLog(msg string, args ...interface{})
	DebugLogf(format string, args ...interface{})
	InfoLogf(format string, args ...interface{})
	WarnLogf(format string, args ...interface{})
	ErrorLogf(format string, args ...interface{})
}

type VKClient interface {
	SendMessage(peerID int64, text string) (int64, error)
	SendThinking(peerID int64, content string) (int64, error)
}

type ToolRegistry interface {
	Get(name string) (tools.Tool, bool)
	ToOpenAISchema() []map[string]interface{}
}

type Compressor interface {
	Compress(ctx interface{}, req interface{}) (interface{}, error)
	CheckTrigger(currentTokens, maxTokens int) bool
	Name() string
}
