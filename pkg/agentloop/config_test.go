package agentloop

import (
	"testing"
	"time"

	"github.com/opencode/llama-client/pkg/compress"
)

// ============================================================
// Тесты конфигурации
// ============================================================

func TestDefaultLoopConfig(t *testing.T) {
	config := DefaultLoopConfig()

	// Проверяем значения по умолчанию
	if config.LlamaServerURL != "127.0.0.1:8081" {
		t.Errorf("expected LlamaServerURL '127.0.0.1:8081', got '%s'", config.LlamaServerURL)
	}
	if config.Model != "local-model" {
		t.Errorf("expected Model 'local-model', got '%s'", config.Model)
	}
	if config.MaxTokens != 4096 {
		t.Errorf("expected MaxTokens 4096, got %d", config.MaxTokens)
	}
	if config.Temperature != 0.7 {
		t.Errorf("expected Temperature 0.7, got %f", config.Temperature)
	}
	if !config.EnableLoopDetection {
		t.Error("expected EnableLoopDetection to be true")
	}
	if config.LoopThreshold != 0.85 {
		t.Errorf("expected LoopThreshold 0.85, got %f", config.LoopThreshold)
	}
	if !config.EnableTools {
		t.Error("expected EnableTools to be true")
	}
	if config.MaxToolCalls != 5 {
		t.Errorf("expected MaxToolCalls 5, got %d", config.MaxToolCalls)
	}
	if config.ToolTimeout != 30*time.Second {
		t.Errorf("expected ToolTimeout 30s, got %v", config.ToolTimeout)
	}
	if config.ThinkingPeerID != 0 {
		t.Errorf("expected ThinkingPeerID 0, got %d", config.ThinkingPeerID)
	}
	if config.EnableThinking {
		t.Error("expected EnableThinking to be false")
	}
	if !config.EnableLogging {
		t.Error("expected EnableLogging to be true")
	}
	if !config.EnableCompression {
		t.Error("expected EnableCompression to be true")
	}
	if config.CompressionStrategy != compress.SummarizeStrategy {
		t.Errorf("expected CompressionStrategy SummarizeStrategy, got %s", config.CompressionStrategy)
	}
	if config.CompressionTokenThreshold != 6000 {
		t.Errorf("expected CompressionTokenThreshold 6000, got %d", config.CompressionTokenThreshold)
	}
	if config.CompressionPercentageThreshold != 0.75 {
		t.Errorf("expected CompressionPercentageThreshold 0.75, got %f", config.CompressionPercentageThreshold)
	}
}

func TestDefaultLoopConfigSessionConfig(t *testing.T) {
	config := DefaultLoopConfig()

	if config.SessionConfig.PeerID != 0 {
		t.Errorf("expected SessionConfig.PeerID 0, got %d", config.SessionConfig.PeerID)
	}
}
