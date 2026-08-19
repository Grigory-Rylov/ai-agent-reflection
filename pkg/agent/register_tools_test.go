package agent

import (
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)


func TestRegisterTools_MergesSchemasAcrossCalls(t *testing.T) {
	a := NewAgent(Config{EnableTools: false})

	mainReg := tools.NewRegistry()
	mainReg.Register(&tools.ShellExecuteTool{})
	mainReg.Register(&tools.FileWriteTool{})

	taskReg := tools.NewRegistry()
	
	
	taskReg.Register(&tools.TimeGetTool{})

	a.RegisterTools(mainReg)
	a.RegisterTools(taskReg)

	names := map[string]bool{}
	for _, s := range a.toolSchemas {
		fn, ok := s["function"].(map[string]interface{})
		if !ok {
			continue
		}
		if n, ok := fn["name"].(string); ok {
			names[n] = true
		}
	}

	for _, want := range []string{shellExecuteToolName, fileWriteToolName, timeGetToolName} {
		if !names[want] {
			t.Errorf("tool %q missing from toolSchemas after two RegisterTools calls; got %v", want, names)
		}
	}
}

const (
	shellExecuteToolName = "shell_execute"
	fileWriteToolName    = "file_write"
	timeGetToolName      = "time_get"
)
