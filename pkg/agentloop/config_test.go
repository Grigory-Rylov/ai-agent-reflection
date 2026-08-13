package agentloop

import (
	"testing"
	"time"
)

func TestDefaultLoopConfig(t *testing.T) {
	config := DefaultLoopConfig()

	if config.ModelHolder != nil {
		t.Errorf("expected ModelHolder to be nil by default")
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
	if config.ToolTimeout != 30*time.Second {
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
	if config.TailTurns != 2 {
		t.Errorf("expected TailTurns 2, got %d", config.TailTurns)
	}
	if !config.EnablePruning {
		t.Error("expected EnablePruning to be true")
	}
}

func TestDefaultLoopConfigSessionConfig(t *testing.T) {
	config := DefaultLoopConfig()

	if config.SessionConfig.PeerID != 0 {
		t.Errorf("expected SessionConfig.PeerID 0, got %d", config.SessionConfig.PeerID)
	}
}
