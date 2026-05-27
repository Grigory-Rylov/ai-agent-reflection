package agent

import (
	"context"
	"os"
	"testing"
)

func TestStubToolExecutor_RemovesLogOnCreate(t *testing.T) {
	logPath := "/tmp/stub_executor_test.log"
	os.WriteFile(logPath, []byte("old content"), 0644)

	executor := NewStubToolExecutor(logPath)
	defer os.Remove(logPath)

	// После создания файл должен быть пустым (удалён)
	if _, err := os.Stat(logPath); err == nil {
		data, _ := os.ReadFile(logPath)
		if len(data) > 0 {
			t.Errorf("log file should be empty after creation, got: %s", string(data))
		}
	}
	_ = executor
}

func TestStubToolExecutor_LogsToolCalls(t *testing.T) {
	logPath := "/tmp/stub_executor_test.log"
	executor := NewStubToolExecutor(logPath)
	defer os.Remove(logPath)

	toolCalls := []ToolCall{
		{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "time_get", Arguments: []byte("{}")}},
		{ID: "call_2", Type: "function", Function: ToolCallFunction{Name: "calc", Arguments: []byte(`{"expression":"2+2"}`)}},
	}

	result := executor.ExecuteAll(context.Background(), toolCalls, 12345)

	if !result.Success {
		t.Fatal("expected success")
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool call results, got %d", len(result.ToolCalls))
	}

	if !executor.Contains("time_get") {
		t.Error("expected log to contain time_get")
	}
	if !executor.Contains("calc") {
		t.Error("expected log to contain calc")
	}
	if !executor.Contains("[TOOL] Call:") {
		t.Error("expected log to contain [TOOL] Call:")
	}
	if !executor.Contains("[TOOL] Result:") {
		t.Error("expected log to contain [TOOL] Result:")
	}

	lines := executor.ReadLog()
	if len(lines) != 4 {
		t.Errorf("expected 4 log lines (2 calls + 2 results), got %d", len(lines))
	}
}

func TestStubToolExecutor_CanBeInjected(t *testing.T) {
	logPath := "/tmp/stub_executor_inject_test.log"
	defer os.Remove(logPath)

	config := DefaultConfig()
	config.EnableTools = true
	config.LlamaServerURL = "http://localhost:99999" // not used when stub is set

	agent := NewAgent(config)
	executor := NewStubToolExecutor(logPath)
	agent.SetToolExecutor(executor)

	if agent.toolExecutor != executor {
		t.Error("toolExecutor should be set on agent")
	}
}
