package agentloop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencode/llama-client/pkg/tools"
)

func setupSystemPromptDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "system_prompt")
	if err := os.MkdirAll(promptDir, 0755); err != nil {
		t.Fatalf("mkdir system_prompt: %v", err)
	}
	files := map[string]string{
		"coordinator.txt": "You are a coordinator. Delegate tasks to worker or qa via subagent tool.",
		"worker.txt":      "You are a worker. Implement the task. You cannot delegate.",
		"qa.txt":          "You are a QA. Review code. Use subagent(worker) for fixes and review_approve.",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(promptDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return promptDir
}

func runScenario(t *testing.T, scenarioName string) {
	t.Helper()
	promptDir := setupSystemPromptDir(t)

	scenario, err := LoadScenarioDir(filepath.Join("testdata", "scenarios", scenarioName))
	if err != nil {
		t.Fatalf("load scenario %s: %v", scenarioName, err)
	}

	server := scenario.MockServer()
	defer server.Close()

	reg := tools.NewRegistry()
	reg.Register(&tools.FileReadTool{})
	reg.Register(&tools.TimeGetTool{})

	orchestrator := NewOrchestrator(OrchestratorConfig{
		LlamaServerURL:  server.URL,
		Model:           "test-model",
		MaxTokens:       100,
		Temperature:     0.7,
		ToolRegistry:    reg,
		Debug:           false,
		SystemPromptDir: promptDir,
	})

	ctx := context.Background()
	result, err := orchestrator.ExecuteTask(ctx, scenario.Prompt, 99999)
	if err != nil {
		t.Fatalf("ExecuteTask failed: %v", err)
	}

	scenario.AssertResult(t, result)
}

func TestScenario_SimpleApprove(t *testing.T) {
	runScenario(t, "simple_approve")
}

func TestScenario_WorkerTask(t *testing.T) {
	runScenario(t, "worker_task")
}

func TestScenario_FullPipeline(t *testing.T) {
	runScenario(t, "full_pipeline")
}

func TestScenario_FullPipeline_JSON(t *testing.T) {
	runScenario(t, "full_pipeline_json")
}
