package agent

import (
	"context"
	"testing"
)

type mockPermissionChecker struct {
	decision string
}

func (m *mockPermissionChecker) Check(toolName string) string {
	return m.decision
}

func TestCheckPermissionAsk(t *testing.T) {
	config := DefaultConfig()
	config.AgentName = "test-agent"
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"

	t.Run("allow when no checker", func(t *testing.T) {
		a := NewAgent(config)
		e := newAgentToolExecutor(a)
		result := e.checkPermissionAsk(context.Background(), "file_write", nil, 12345)
		if !result {
			t.Error("expected allow when no checker set")
		}
	})

	t.Run("allow when empty agent name", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.AgentName = ""
		a := NewAgent(cfg)
		e := newAgentToolExecutor(a)
		result := e.checkPermissionAsk(context.Background(), "file_write", nil, 12345)
		if !result {
			t.Error("expected allow when agent name is empty")
		}
	})

	t.Run("deny when permission says deny", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(&mockPermissionChecker{decision: "deny"})
		e := newAgentToolExecutor(a)
		result := e.checkPermissionAsk(context.Background(), "file_write", nil, 12345)
		if result {
			t.Error("expected deny when permission says deny")
		}
	})

	t.Run("allow when permission says allow", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(&mockPermissionChecker{decision: "allow"})
		e := newAgentToolExecutor(a)
		result := e.checkPermissionAsk(context.Background(), "file_write", nil, 12345)
		if !result {
			t.Error("expected allow when permission says allow")
		}
	})
}

func TestExecuteToolWithPermission(t *testing.T) {
	config := DefaultConfig()
	config.AgentName = "test-agent"
	config.EnableTools = true
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"

	t.Run("tool execution denied by permission", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(&mockPermissionChecker{decision: "deny"})

		e := newAgentToolExecutor(a)
		toolCall := ToolCall{
			ID:   "call_1",
			Type: "function",
			Function: ToolCallFunction{
				Name:      "time_get",
				Arguments: []byte("{}"),
			},
		}
		result, err := e.executeTool(context.Background(), toolCall, 12345)
		if err == nil {
			t.Error("expected error for denied tool")
		}
		if result.IsError != true {
			t.Error("expected IsError for denied tool")
		}
	})

	t.Run("tool execution allowed by permission", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(&mockPermissionChecker{decision: "allow"})

		e := newAgentToolExecutor(a)
		toolCall := ToolCall{
			ID:   "call_1",
			Type: "function",
			Function: ToolCallFunction{
				Name:      "time_get",
				Arguments: []byte("{}"),
			},
		}
		result, err := e.executeTool(context.Background(), toolCall, 12345)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Error("expected success for allowed tool")
		}
	})
}
