package agent

import (
	"context"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

type recordingTool struct {
	name string
	hits int
}

func (t *recordingTool) Name() string        { return t.name }
func (t *recordingTool) Description() string { return "recording stub" }
func (t *recordingTool) Schema() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (t *recordingTool) Execute(ctx context.Context, inputs map[string]string) (tools.ToolResult, error) {
	t.hits++
	return tools.ToolResult{Success: true, Data: "ok"}, nil
}

func TestToolAliasesResolveToCanonicalTargets(t *testing.T) {
	tools.SetAccessController(nil)
	reg := tools.NewRegistry()
	rec := map[string]*recordingTool{}
	for _, name := range []string{"read", "write", "bash", "grep", "ask"} {
		rec[name] = &recordingTool{name: name}
		reg.Register(rec[name])
	}
	a := NewAgent(DefaultConfig())
	a.ReplaceTools(reg)
	e := newAgentToolExecutor(a)

	cases := map[string]string{
		"file_read":     "read",
		"read_file":     "read",
		"Read":          "read",
		"read":          "read",
		"file_write":    "write",
		"write_file":    "write",
		"Write":         "write",
		"write":         "write",
		"shell_execute": "bash",
		"Bash":          "bash",
		"shell":         "bash",
		"bash":          "bash",
		"search_code":   "grep",
		"grep_search":   "grep",
		"Grep":          "grep",
		"grep":          "grep",
		"question":      "ask",
	}
	for call, want := range cases {
		e.ExecuteAll(context.Background(), []ToolCall{{
			ID:       "id_" + call,
			Type:     "function",
			Function: ToolCallFunction{Name: call, Arguments: []byte(`{"path":"/tmp/zz-proof","command":"true","pattern":"x","question":"q"}`)},
		}}, 1)
		if rec[want].hits == 0 {
			t.Errorf("call %q did not reach canonical tool %q", call, want)
		}
	}
}

func TestCanonicalNamesAreNotAliasKeys(t *testing.T) {
	for _, canonical := range []string{"read", "write", "bash", "grep", "ask"} {
		if _, isAlias := toolAliases[canonical]; isAlias {
			t.Errorf("%q must be canonical, not an alias key", canonical)
		}
		if got := resolveToolAlias(canonical); got != canonical {
			t.Errorf("resolveToolAlias(%q) = %q, want identity", canonical, got)
		}
	}
}
