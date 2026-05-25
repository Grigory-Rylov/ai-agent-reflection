package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/opencode/llama-client/pkg/workflow"
)

// ============================================================
// Agent state management — injected dependencies for agent_result tool
// ============================================================

// agentStateManager предоставляет доступ к состоянию agent mode
type agentStateManager interface {
	GetWorkflowID(peerID int64) string
	ExitMode(peerID int64)
}

var (
	agentMu    sync.RWMutex
	agentWfMgr *workflow.WorkflowManager
	agentStMgr agentStateManager
)

// SetAgentDependencies устанавливает зависимости для agent_result инструмента
func SetAgentDependencies(wfMgr *workflow.WorkflowManager, stMgr agentStateManager) {
	agentMu.Lock()
	defer agentMu.Unlock()
	agentWfMgr = wfMgr
	agentStMgr = stMgr
}

func getWorkflowMgr() *workflow.WorkflowManager {
	agentMu.RLock()
	defer agentMu.RUnlock()
	return agentWfMgr
}

func getAgentStMgr() agentStateManager {
	agentMu.RLock()
	defer agentMu.RUnlock()
	return agentStMgr
}

// ============================================================
// agent_result — завершает workflow и возвращает результат пользователю
// ============================================================

// AgentResultTool вызывает координатор когда задача решена
type AgentResultTool struct{}

// Name возвращает имя инструмента
func (t *AgentResultTool) Name() string {
	return "agent_result"
}

// Description возвращает описание инструмента
func (t *AgentResultTool) Description() string {
	return "Вызываете этот инструмент когда задача полностью решена. " +
		"Это завершает workflow и отправляет пользователю итоговый результат. " +
		"Используйте только если уверены что вся задача выполнена."
}

// Schema возвращает схему инструмента
func (t *AgentResultTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"peer_id": CreateIntegerParameter("peer_id", "VK peer ID of the user", true),
			"summary": CreateStringParameter("summary", "Final result of the workflow (code, text, report)", true),
		},
		"required": []string{"peer_id", "summary"},
	}
}

// Execute выполняет инструмент
func (t *AgentResultTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	peerIDStr, ok := inputs["peer_id"]
	if !ok || peerIDStr == "" {
		return ToolResult{Success: false, Error: "peer_id required"}, fmt.Errorf("peer_id required")
	}
	peerID := int64(0)
	fmt.Sscanf(peerIDStr, "%d", &peerID)

	summary, ok := inputs["summary"]
	if !ok || summary == "" {
		return ToolResult{Success: false, Error: "summary required"}, fmt.Errorf("summary required")
	}

	// Находим workflow manager для этого пользователя
	wfMgr := getWorkflowMgr()
	if wfMgr == nil {
		return ToolResult{Success: false}, fmt.Errorf("workflow manager not initialized")
	}

	// Получаем active workflow пользователя
	agentStMgr := getAgentStMgr()
	if agentStMgr == nil {
		return ToolResult{Success: false}, fmt.Errorf("agent state not found")
	}

	wfID := agentStMgr.GetWorkflowID(peerID)
	if wfID == "" {
		return ToolResult{Success: false}, fmt.Errorf("no active workflow for peer %d", peerID)
	}

	wf := wfMgr.GetWorkflow(wfID)
	if wf == nil {
		return ToolResult{Success: false}, fmt.Errorf("workflow %s not found", wfID)
	}

	// Архивируем артефакты в рабочую директорию пользователя
	artifactsPath := filepath.Join(WorkingDir, ".workflow_artifacts", wfID)
	os.MkdirAll(artifactsPath, 0755)

	// Копируем артефакты из workflow dir
	artifactsPath = filepath.Join(artifactsPath, "artifacts")
	os.MkdirAll(artifactsPath, 0755)

	// Копируем файлы артефактов
	files, err := wfMgr.ListWorkflowArtifacts(wfID, "")
	if err == nil && len(files) > 0 {
		srcDir := wfMgr.GetWorkflowDir(wfID)
		for _, f := range files {
			src := filepath.Join(srcDir, "artifacts", f)
			dest := filepath.Join(artifactsPath, f)
			data, err := os.ReadFile(src)
			if err == nil {
				os.MkdirAll(filepath.Dir(dest), 0755)
				os.WriteFile(dest, data, 0644)
			}
		}
	}

	// Формируем финальный отчёт
	report := fmt.Sprintf("✅ Workflow %s завершён.\n\n", wfID)
	report += fmt.Sprintf("**Результат:**\n%s\n\n", summary)

	// Список одобренных этапов
	approved := wfMgr.GetAllApprovedTasks(wfID)
	report += fmt.Sprintf("**Этапы (%d):**\n", len(approved))
	for _, task := range approved {
		report += fmt.Sprintf("✅ [%s] %s\n", task.ID, task.Title)
	}
	report += fmt.Sprintf("\n**Всего задач:** %d\n", len(wf.Tasks))

	// Сохраняем результат
	wfMgr.CompleteWorkflow(wfID, summary)

	// Уведомляем пользователя (записываем в файл — получит agent loop)
	notifDir := filepath.Join(WorkingDir, ".agent_notifications")
	os.MkdirAll(notifDir, 0755)
	os.WriteFile(
		filepath.Join(notifDir, fmt.Sprintf("%d.result", peerID)),
		[]byte(report),
		0644,
	)

	// Отключаем агентный режим
	agentStMgr.ExitMode(peerID)

	return ToolResult{
		Success: true,
		Data:    report,
	}, nil
}
