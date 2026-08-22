package agentloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
)

func writeModelsHolder(t *testing.T, cfg *modelsconfig.ModelsConfig) *modelsconfig.Holder {
	t.Helper()
	path := filepath.Join(t.TempDir(), "models.json")
	data, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		t.Fatalf("marshal models config: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write models.json: %v", err)
	}
	h, err := modelsconfig.NewHolder(path)
	if err != nil {
		t.Fatalf("NewHolder: %v", err)
	}
	return h
}

func TestModelSwitchRefreshesTokenizerCompactor(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.ModelHolder = writeModelsHolder(t, &modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test":  {Name: "test-model", Host: "http://localhost:8081"},
			"other": {Name: "other-model", Host: "http://localhost:8082", Context: 8192},
		},
	})

	loop, err := NewAgentLoop(config, vk, reg)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	al := loop.(*agentLoop)

	if got := al.tokenizer.Name(); got != "llama-server-test-model" {
		t.Fatalf("initial tokenizer = %q, want llama-server-test-model", got)
	}

	if err := config.ModelHolder.Switch("other"); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	if err := al.syncCurrentModel(); err != nil {
		t.Fatalf("syncCurrentModel: %v", err)
	}

	if got := al.tokenizer.Name(); got != "llama-server-other-model" {
		t.Errorf("tokenizer after switch = %q, want llama-server-other-model", got)
	}
	if al.config.MaxTokens != 8192 {
		t.Errorf("MaxTokens after switch = %d, want 8192", al.config.MaxTokens)
	}
	llm, ok := al.compactor.LLM().(*compress.LLMCompressor)
	if !ok {
		t.Fatalf("compactor LLM is not *compress.LLMCompressor")
	}
	if llm.Model() != "other-model" {
		t.Errorf("compactor model = %q, want other-model", llm.Model())
	}
	if llm.ServerURL() != "http://localhost:8082" {
		t.Errorf("compactor server = %q, want http://localhost:8082", llm.ServerURL())
	}
}

func TestModelSwitchRefreshIdempotent(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.ModelHolder = writeModelsHolder(t, &modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test":  {Name: "test-model", Host: "http://localhost:8081"},
			"other": {Name: "other-model", Host: "http://localhost:8082", Context: 8192},
		},
	})

	loop, err := NewAgentLoop(config, vk, reg)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	al := loop.(*agentLoop)

	before := al.tokenizer
	if err := al.syncCurrentModel(); err != nil {
		t.Fatalf("syncCurrentModel: %v", err)
	}
	if al.tokenizer != before {
		t.Error("refresh without model switch should not rebuild tokenizer")
	}
}

func TestModelSwitchRefreshNilHolder(t *testing.T) {
	al := &agentLoop{config: DefaultLoopConfig()}
	if err := al.syncCurrentModel(); err != nil {
		t.Fatalf("syncCurrentModel: %v", err)
	}
}

func TestSyncCurrentModel_ResolvingFailsUsesFallback(t *testing.T) {
	vk := &mockVKClient{}
	reg := newMockToolRegistry()
	config := DefaultLoopConfig()
	config.ModelHolder = writeModelsHolder(t, &modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test":        {Name: "test-model", Host: "http://localhost:8081"},
			"unreachable": {Name: "bad-model", Host: "127.0.0.1:1"},
		},
	})
	r := NewModelContextResolver(config.ModelHolder, nil)
	config.ContextResolver = r
	config.MaxTokens = 4096

	loop, err := NewAgentLoop(config, vk, reg)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	al := loop.(*agentLoop)

	if err := config.ModelHolder.Switch("unreachable"); err != nil {
		t.Fatalf("Switch: %v", err)
	}

	if err := al.syncCurrentModel(); err != nil {
		t.Fatalf("syncCurrentModel should not fail (fallback expected), got: %v", err)
	}

	if got := al.tokenizer.Name(); got != "llama-server-bad-model" {
		t.Errorf("tokenizer = %q, want llama-server-bad-model", got)
	}
	if al.config.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096 (fallback)", al.config.MaxTokens)
	}
}
