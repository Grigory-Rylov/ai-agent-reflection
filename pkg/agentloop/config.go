package agentloop

import (
	"time"

	"github.com/opencode/llama-client/pkg/modelsconfig"
	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

type LoopConfig struct {
	ModelHolder     *modelsconfig.Holder
	ContextResolver *ModelContextResolver
	MaxTokens       int
	Temperature     float64

	SessionConfig                  session.Config
	SystemPromptFile               string
	EnableLoopDetection            bool
	LoopThreshold                  float64
	EnableTools                    bool
	MaxToolCalls                   int
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
	EnablePruning                  bool
	ToolOutputMaxLines             int
	ToolOutputMaxBytes             int
	SkipShellPermissionForPathless bool
}

func DefaultLoopConfig() LoopConfig {
	return LoopConfig{
		MaxTokens:                      4096,
		Temperature:                    0.7,
		SessionConfig:                  session.DefaultConfig(),
		EnableLoopDetection:            true,
		LoopThreshold:                  0.85,
		EnableTools:                    true,
		MaxToolCalls:                   5,
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
