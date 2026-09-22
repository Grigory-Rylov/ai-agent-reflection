package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func requireEvalRuntime(t *testing.T, bin string) {
	t.Helper()
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("%s not available: %v", bin, err)
	}
}

func evalCall(t *testing.T, tool *EvalTool, inputs map[string]string) ToolResult {
	t.Helper()
	result, err := tool.Execute(context.Background(), inputs)
	if err != nil {
		t.Fatalf("Execute returned go error: %v", err)
	}
	return result
}

func evalOutput(t *testing.T, result ToolResult) string {
	t.Helper()
	data, ok := result.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("result data is not a map: %#v", result.Data)
	}
	output, ok := data["output"].(string)
	if !ok {
		t.Fatalf("result output is not a string: %#v", data["output"])
	}
	return output
}

func TestEvalPythonPersistsState(t *testing.T) {
	requireEvalRuntime(t, "python3")
	tool := &EvalTool{}

	first := evalCall(t, tool, map[string]string{"language": "py", "code": "x = 41"})
	if !first.Success {
		t.Fatalf("first call failed: %s", first.Error)
	}

	second := evalCall(t, tool, map[string]string{"language": "py", "code": "x + 1"})
	if !second.Success {
		t.Fatalf("second call failed: %s", second.Error)
	}
	if got := evalOutput(t, second); got != "42" {
		t.Fatalf("expected persisted state output 42, got %q", got)
	}
}

func TestEvalPythonStdout(t *testing.T) {
	requireEvalRuntime(t, "python3")
	tool := &EvalTool{}

	result := evalCall(t, tool, map[string]string{"language": "py", "code": "print('hello eval')"})
	if !result.Success {
		t.Fatalf("call failed: %s", result.Error)
	}
	if got := evalOutput(t, result); !strings.Contains(got, "hello eval") {
		t.Fatalf("expected stdout in output, got %q", got)
	}
}

func TestEvalPythonSyntaxError(t *testing.T) {
	requireEvalRuntime(t, "python3")
	tool := &EvalTool{}

	result := evalCall(t, tool, map[string]string{"language": "py", "code": "def broken("})
	if result.Success {
		t.Fatalf("expected failure for syntax error, got %#v", result)
	}
	if !strings.Contains(result.Error, "SyntaxError") {
		t.Fatalf("expected SyntaxError in error, got %q", result.Error)
	}
}

func TestEvalPythonTimeoutRestartsKernel(t *testing.T) {
	requireEvalRuntime(t, "python3")
	tool := &EvalTool{}

	hang := evalCall(t, tool, map[string]string{
		"language":    "py",
		"code":        "while True:\n    pass",
		"timeout_sec": "2",
	})
	if hang.Success {
		t.Fatalf("expected timeout failure, got %#v", hang)
	}
	if !strings.Contains(hang.Error, "timed out") {
		t.Fatalf("expected timeout message, got %q", hang.Error)
	}

	next := evalCall(t, tool, map[string]string{"language": "py", "code": "1 + 1"})
	if !next.Success {
		t.Fatalf("call after timeout failed: %s", next.Error)
	}
	got := evalOutput(t, next)
	if !strings.Contains(got, "2") {
		t.Fatalf("expected 2 after restart, got %q", got)
	}
	if !strings.Contains(got, "kernel restarted") {
		t.Fatalf("expected kernel restarted note, got %q", got)
	}
}

func TestEvalJSPersistsState(t *testing.T) {
	requireEvalRuntime(t, "node")
	tool := &EvalTool{}

	first := evalCall(t, tool, map[string]string{"language": "js", "code": "let y = 1"})
	if !first.Success {
		t.Fatalf("first call failed: %s", first.Error)
	}

	second := evalCall(t, tool, map[string]string{"language": "js", "code": "y = 2; y"})
	if !second.Success {
		t.Fatalf("second call failed: %s", second.Error)
	}
	if got := evalOutput(t, second); got != "2" {
		t.Fatalf("expected completion value 2, got %q", got)
	}
}

func TestEvalJSTopLevelAwait(t *testing.T) {
	requireEvalRuntime(t, "node")
	tool := &EvalTool{}

	result := evalCall(t, tool, map[string]string{"language": "js", "code": "await Promise.resolve(7)"})
	if !result.Success {
		t.Fatalf("top-level await call failed: %s", result.Error)
	}
	if got := evalOutput(t, result); got != "7" {
		t.Fatalf("expected awaited value 7, got %q", got)
	}
}

func TestEvalJSConsoleCapture(t *testing.T) {
	requireEvalRuntime(t, "node")
	tool := &EvalTool{}

	result := evalCall(t, tool, map[string]string{"language": "js", "code": "console.log('js out'); 5"})
	if !result.Success {
		t.Fatalf("call failed: %s", result.Error)
	}
	got := evalOutput(t, result)
	if !strings.Contains(got, "js out") || !strings.Contains(got, "5") {
		t.Fatalf("expected console output and completion value, got %q", got)
	}
}

func TestEvalInvalidLanguage(t *testing.T) {
	tool := &EvalTool{}

	result := evalCall(t, tool, map[string]string{"language": "rb", "code": "puts 1"})
	if result.Success {
		t.Fatalf("expected failure for unknown language, got %#v", result)
	}
}
