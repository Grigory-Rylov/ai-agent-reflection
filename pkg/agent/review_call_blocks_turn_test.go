package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/debug"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type fakeTaskTool struct {
	targetReview bool
	calls        []map[string]string
}

func (f *fakeTaskTool) Name() string        { return "task" }
func (f *fakeTaskTool) Description() string { return "fake task tool" }
func (f *fakeTaskTool) Schema() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}

func (f *fakeTaskTool) Execute(ctx context.Context, inputs map[string]string) (tools.ToolResult, error) {
	f.calls = append(f.calls, inputs)
	return tools.ToolResult{Success: true, Data: "done"}, nil
}

func (f *fakeTaskTool) TargetsReviewAgent(args map[string]string) bool {
	return f.targetReview
}

type fakeShellTool struct{ calls int }

func (f *fakeShellTool) Name() string        { return "bash" }
func (f *fakeShellTool) Description() string { return "fake shell" }
func (f *fakeShellTool) Schema() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}

func (f *fakeShellTool) Execute(ctx context.Context, inputs map[string]string) (tools.ToolResult, error) {
	f.calls++
	return tools.ToolResult{Success: true, Data: "ok"}, nil
}

func newExecutorWithTools(t *testing.T, taskTool *fakeTaskTool, shellTool *fakeShellTool) *agentToolExecutor {
	t.Helper()
	registry := tools.NewRegistry()
	registry.Register(taskTool)
	registry.Register(shellTool)
	executor := &agentToolExecutor{agent: &agentImpl{
		toolsRegistry:    registry,
		thinkingCallback: func(peerID int64, content string) error { return nil },
		debugLog:         debug.NewLogger(false),
		sessions:         make(map[int64]*session.Session),
	}}
	return executor
}

func makeToolCall(id, name string, args map[string]string) ToolCall {
	raw, _ := json.Marshal(args)
	return ToolCall{ID: id, Type: "function", Function: ToolCallFunction{Name: name, Arguments: raw}}
}

func TestExecuteAllSkipsCallsAfterReviewSubagent(t *testing.T) {
	taskTool := &fakeTaskTool{targetReview: true}
	shellTool := &fakeShellTool{}
	executor := newExecutorWithTools(t, taskTool, shellTool)

	calls := []ToolCall{
		makeToolCall("c1", "task", map[string]string{"subagent_type": "reviewer", "prompt": "review the diff"}),
		makeToolCall("c2", "bash", map[string]string{"command": "go build ./..."}),
	}

	result := executor.ExecuteAll(context.Background(), calls, 1)

	if shellTool.calls != 0 {
		t.Errorf("bash ran %d times after review call, want 0", shellTool.calls)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected 2 results (review + skipped), got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[1].ToolCallID != "c2" || !result.ToolCalls[1].IsError {
		t.Errorf("expected skipped error result for c2, got %+v", result.ToolCalls[1])
	}
}

func TestExecuteAllRunsCallsAfterNonReviewSubagent(t *testing.T) {
	taskTool := &fakeTaskTool{targetReview: false}
	shellTool := &fakeShellTool{}
	executor := newExecutorWithTools(t, taskTool, shellTool)

	calls := []ToolCall{
		makeToolCall("c1", "task", map[string]string{"subagent_type": "explore", "prompt": "find files"}),
		makeToolCall("c2", "bash", map[string]string{"command": "go build ./..."}),
	}

	result := executor.ExecuteAll(context.Background(), calls, 1)

	if shellTool.calls != 1 {
		t.Errorf("bash ran %d times, want 1", shellTool.calls)
	}
	if len(result.ToolCalls) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result.ToolCalls))
	}
	for _, r := range result.ToolCalls {
		if r.IsError {
			t.Errorf("unexpected error result: %+v", r)
		}
	}
}
