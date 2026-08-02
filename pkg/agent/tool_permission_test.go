package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencode/llama-client/pkg/access"
	"github.com/opencode/llama-client/pkg/tools"
)

type mockPermissionChecker struct {
	decision string
}

func (m *mockPermissionChecker) Check(toolName string) string {
	return m.decision
}

func (m *mockPermissionChecker) Evaluate(permission, pattern string) string {
	return m.decision
}

func (m *mockPermissionChecker) Approve(permission, pattern string) {}

// mockToolPermissionChecker returns different decisions based on tool name
type mockToolPermissionChecker struct {
	decisions map[string]string
}

func (m *mockToolPermissionChecker) Check(toolName string) string {
	if d, ok := m.decisions[toolName]; ok {
		return d
	}
	return "allow"
}

func (m *mockToolPermissionChecker) Evaluate(permission, pattern string) string {
	if d, ok := m.decisions[permission]; ok {
		return d
	}
	return "allow"
}

func (m *mockToolPermissionChecker) Approve(permission, pattern string) {}

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

	t.Run("allow when no checker even with empty agent name", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.AgentName = ""
		a := NewAgent(cfg)
		e := newAgentToolExecutor(a)
		result := e.checkPermissionAsk(context.Background(), "file_write", nil, 12345)
		if !result {
			t.Error("expected allow when no checker set (backward compat)")
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

func TestShellExecutePermissionGrantedPath(t *testing.T) {
	config := DefaultConfig()
	config.AgentName = "test-agent"

	const peerID int64 = 777777
	tools.GrantPath(peerID, "/home/user/granted")

	askChecker := &mockToolPermissionChecker{
		decisions: map[string]string{
			"shell_execute": "ask",
		},
	}

	t.Run("shell command on granted path bypasses ask", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(askChecker)
		e := newAgentToolExecutor(a)

		asked := false
		tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
			asked = true
			return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
		})
		defer tools.SetQuestionCallback(nil)

		result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
			"command": "ls /home/user/granted",
		}, peerID)

		if !result {
			t.Error("expected allow for shell command on granted path")
		}
		if asked {
			t.Error("expected NO permission ask for granted path")
		}
	})
}

func TestShellExecutePermissionAsksForUnmatched(t *testing.T) {
	config := DefaultConfig()
	config.AgentName = "test-agent"

	askChecker := &mockToolPermissionChecker{
		decisions: map[string]string{
			"shell_execute": "ask",
			"bash":          "ask",
		},
	}

	allowedDir, err := os.MkdirTemp("", "shell_perm_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(allowedDir)

	oldWorkingDir := tools.WorkingDir
	tools.WorkingDir = allowedDir
	defer func() { tools.WorkingDir = oldWorkingDir }()

	ctrl := access.NewController([]string{allowedDir})
	tools.SetAccessController(ctrl)
	defer tools.SetAccessController(nil)

	t.Run("shell cat /etc/passwd asks when path outside allowed dirs", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(askChecker)
		e := newAgentToolExecutor(a)

		asked := false
		tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
			asked = true
			return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
		})
		defer tools.SetQuestionCallback(nil)

		result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
			"command": "cat /etc/passwd",
		}, 12345)

		if !result {
			t.Error("expected allow after user approved")
		}
		if !asked {
			t.Error("expected permission ask for path outside allowed dirs")
		}
	})

	t.Run("shell redirect outside allowed dirs asks", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(askChecker)
		e := newAgentToolExecutor(a)

		asked := false
		tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
			asked = true
			return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
		})
		defer tools.SetQuestionCallback(nil)

		result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
			"command": "echo hello > /tmp/outside",
		}, 12345)

		if !result {
			t.Error("expected allow after user approved")
		}
		if !asked {
			t.Error("expected permission ask for redirect outside allowed dirs")
		}
	})

	t.Run("filesystem-safe command skips ask", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(askChecker)
		e := newAgentToolExecutor(a)

		asked := false
		tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
			asked = true
			return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
		})
		defer tools.SetQuestionCallback(nil)

		result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
			"command": "echo hello world",
		}, 12345)

		if !result {
			t.Error("expected allow for filesystem-safe command")
		}
		if asked {
			t.Error("expected NO permission ask for filesystem-safe command")
		}
	})

	t.Run("docker ps skips ask", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(askChecker)
		e := newAgentToolExecutor(a)

		asked := false
		tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
			asked = true
			return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
		})
		defer tools.SetQuestionCallback(nil)

		result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
			"command": "docker ps",
		}, 12345)

		if !result {
			t.Error("expected allow for filesystem-safe command")
		}
		if asked {
			t.Error("expected NO permission ask for filesystem-safe command")
		}
	})
}

func TestFileToolPermissionWithinAllowedDir(t *testing.T) {
	config := DefaultConfig()
	config.AgentName = "test-agent"

	allowedDir, err := os.MkdirTemp("", "file_perm_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(allowedDir)

	oldWorkingDir := tools.WorkingDir
	tools.WorkingDir = allowedDir
	defer func() { tools.WorkingDir = oldWorkingDir }()

	ctrl := access.NewController([]string{allowedDir})
	tools.SetAccessController(ctrl)
	defer tools.SetAccessController(nil)

	askChecker := &mockToolPermissionChecker{
		decisions: map[string]string{
			"edit":       "ask",
			"file_write": "ask",
		},
	}

	t.Run("edit file in allowed dir bypasses ask", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(askChecker)
		e := newAgentToolExecutor(a)

		asked := false
		tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
			asked = true
			return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
		})
		defer tools.SetQuestionCallback(nil)

		result := e.checkPermissionAsk(context.Background(), "edit", map[string]string{
			"path":       "avito_bot.py",
			"old_string": "old",
			"new_string": "new",
		}, 12345)

		if !result {
			t.Error("expected allow for edit inside allowed dir")
		}
		if asked {
			t.Error("expected NO permission ask for edit inside allowed dir")
		}
	})

	t.Run("file_write in allowed dir bypasses ask", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(askChecker)
		e := newAgentToolExecutor(a)

		asked := false
		tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
			asked = true
			return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
		})
		defer tools.SetQuestionCallback(nil)

		result := e.checkPermissionAsk(context.Background(), "file_write", map[string]string{
			"path":    "new_bot.py",
			"content": "print('hello')",
		}, 12345)

		if !result {
			t.Error("expected allow for file_write inside allowed dir")
		}
		if asked {
			t.Error("expected NO permission ask for file_write inside allowed dir")
		}
	})

	t.Run("edit absolute path in allowed dir bypasses ask", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(askChecker)
		e := newAgentToolExecutor(a)

		asked := false
		tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
			asked = true
			return map[string]interface{}{"selected": []interface{}{"✅ Allow"}}, nil
		})
		defer tools.SetQuestionCallback(nil)

		target := filepath.Join(allowedDir, "main.py")
		result := e.checkPermissionAsk(context.Background(), "edit", map[string]string{
			"path": target,
		}, 12345)

		if !result {
			t.Error("expected allow for edit with absolute path inside allowed dir")
		}
		if asked {
			t.Error("expected NO permission ask for edit with absolute path inside allowed dir")
		}
	})

	t.Run("edit outside allowed dir MUST ask", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(askChecker)
		e := newAgentToolExecutor(a)

		asked := false
		tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
			asked = true
			return map[string]interface{}{"selected": []interface{}{"❌ Deny"}}, nil
		})
		defer tools.SetQuestionCallback(nil)

		result := e.checkPermissionAsk(context.Background(), "edit", map[string]string{
			"path": "/etc/hosts",
		}, 12345)

		if result {
			t.Error("expected deny for edit outside allowed dir")
		}
		if !asked {
			t.Error("expected permission ask for edit outside allowed dir")
		}
	})
}

func TestShellExecutePathOutsideAllowed(t *testing.T) {
	config := DefaultConfig()
	config.AgentName = "test-agent"

	dir := t.TempDir()
	tools.SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() { tools.SetAccessController(nil) })

	askChecker := &mockToolPermissionChecker{
		decisions: map[string]string{
			"shell_execute": "ask",
			"bash":          "ask",
		},
	}

	t.Run("shell cat outside allowed dir asks", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(askChecker)
		e := newAgentToolExecutor(a)

		asked := false
		tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
			asked = true
			return map[string]interface{}{"selected": []interface{}{"❌ Deny"}}, nil
		})
		defer tools.SetQuestionCallback(nil)

		result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
			"command": "cat /etc/passwd",
		}, 12345)

		if result {
			t.Error("expected deny for cat /etc/passwd when bash permission is ask")
		}
		if !asked {
			t.Error("expected permission ask for cat /etc/passwd when bash permission is ask")
		}
	})

	t.Run("shell rm outside allowed dir asks", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(askChecker)
		e := newAgentToolExecutor(a)

		asked := false
		tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
			asked = true
			return map[string]interface{}{"selected": []interface{}{"❌ Deny"}}, nil
		})
		defer tools.SetQuestionCallback(nil)

		result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
			"command": "rm -rf /important/data",
		}, 12345)

		if result {
			t.Error("expected deny for rm /important/data when bash permission is ask")
		}
		if !asked {
			t.Error("expected permission ask for rm /important/data when bash permission is ask")
		}
	})

	t.Run("shell cp asks when bash permission is ask", func(t *testing.T) {
		a := NewAgent(config)
		a.SetPermissionChecker(askChecker)
		e := newAgentToolExecutor(a)

		asked := false
		tools.SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
			asked = true
			return map[string]interface{}{"selected": []interface{}{"❌ Deny"}}, nil
		})
		defer tools.SetQuestionCallback(nil)

		result := e.checkPermissionAsk(context.Background(), "shell_execute", map[string]string{
			"command": "cp /etc/shadow /tmp/stolen.txt",
		}, 12345)

		if result {
			t.Error("expected deny for cp when bash permission is ask")
		}
		if !asked {
			t.Error("expected permission ask for cp when bash permission is ask")
		}
	})
}
