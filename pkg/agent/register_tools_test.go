package agent

import (
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

// TestRegisterTools_MergesSchemasAcrossCalls — регрессионный тест на баг:
// RegisterTools затирал toolSchemas схемами только последнего реестра, поэтому
// агент, которому инструменты регистрируют двумя вызовами (основные инструменты
// + subagent/task-инструмент — как воркеру в multi-agent режиме), видел в LLM
// только последний набор. Воркер терял shell/file и не мог ни реализовать
// задачу, ни дойти до reviewer'а.
func TestRegisterTools_MergesSchemasAcrossCalls(t *testing.T) {
	a := NewAgent(Config{EnableTools: false})

	mainReg := tools.NewRegistry()
	mainReg.Register(&tools.ShellExecuteTool{})
	mainReg.Register(&tools.FileWriteTool{})

	taskReg := tools.NewRegistry()
	// SubAgentTool требует много контекста — используем простой инструмент,
	// чтобы проверить именно логику слияния schema в RegisterTools.
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
