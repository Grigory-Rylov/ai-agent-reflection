package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)


type WorkflowEngine struct {
	mgr          *WorkflowManager
	notifier     Notifier
	agentFactory AgentFactory
	mu           sync.Mutex
	running      map[string]bool 
}


type Notifier interface {
	SendMessage(peerID int64, message string) (int, error)
}


type Session struct {
	SystemPrompt string
	Role         Role
	Messages     []string
}


func (s *Session) AddUserMessage(msg string) {
	s.Messages = append(s.Messages, msg)
}


type AgentFactory interface {
	CreateAgent(role Role, systemPrompt string) RoleAgent
}


type RoleAgent interface {
	ProcessMessage(ctx context.Context, message string, peerID int64) (string, error)
}


func NewWorkflowEngine(mgr *WorkflowManager, notifier Notifier, factory AgentFactory) *WorkflowEngine {
	return &WorkflowEngine{
		mgr:          mgr,
		notifier:     notifier,
		agentFactory: factory,
		running:      make(map[string]bool),
	}
}


func (e *WorkflowEngine) StartWorkflow(workflowID string) error {
	e.mu.Lock()
	if e.running[workflowID] {
		e.mu.Unlock()
		return fmt.Errorf("workflow already running: %s", workflowID)
	}
	e.running[workflowID] = true
	e.mu.Unlock()

	workflow, err := e.mgr.LoadWorkflow(workflowID)
	if err != nil {
		e.mu.Lock()
		delete(e.running, workflowID)
		e.mu.Unlock()
		return fmt.Errorf("load workflow: %w", err)
	}

	
	go func() {
		defer func() {
			e.mu.Lock()
			delete(e.running, workflowID)
			e.mu.Unlock()
		}()

		e.runCoordinatorLoop(workflow)
	}()

	return nil
}


func (e *WorkflowEngine) runCoordinatorLoop(workflow *Workflow) {
	peerID := workflow.UserPeerID

	
	coordSession := e.createRoleSession(CoordinatorRole, workflow)

	
	breakdownMsg := fmt.Sprintf("Пользовательская задача: %s\n\nРазбей эту задачу на независимые этапы работы. Для каждого этапа определи: описание, какие файлы трогать, какие инструменты использовать. Верни JSON массив задач.\n\nФормат: [{\"id\": \"task_1\", \"title\": \"...\", \"description\": \"...\", \"assignee\": \"developer\"}, ...]", workflow.UserOriginal)

	coordResult := e.callLLM(context.Background(), coordSession, breakdownMsg)

	if strings.HasPrefix(coordResult, "ERROR:") {
		
		e.notifyPeer(peerID, fmt.Sprintf("⚠ Тимлид не смог проанализировать задачу: %s", coordResult[6:]))
		return
	}

	
	tasks := e.parseTasksResponse(coordResult)
	if len(tasks) == 0 {
		e.notifyPeer(peerID, "⚠ Тимлид не смог разбить задачу на этапы.")
		return
	}

	
	for _, t := range tasks {
		workflow.Tasks = append(workflow.Tasks, t)
	}
	workflow.Status = WorkflowInProgress
	workflow.UpdatedAt = time.Now()

	e.saveWorkflow(workflow)
	e.notifyPeer(peerID, fmt.Sprintf("📋 Тимлид разбил задачу на %d этапов. Начинаю выполнение...", len(tasks)))

	
	for workflow.Status == WorkflowInProgress {
		
		nextTask := e.findNextTask(workflow)
		if nextTask == nil {
			
			break
		}

		
		devSession := e.createRoleSession(DeveloperRole, workflow)
		devPrompt := e.buildDevPrompt(workflow, *nextTask)
		devResult := e.callLLM(context.Background(), devSession, devPrompt)

		if strings.HasPrefix(devResult, "ERROR:") {
			e.notifyPeer(peerID, fmt.Sprintf("⚠ Разработка этапа %s провалилась: %s", nextTask.ID, devResult[6:]))
			break
		}

		
		reviewSession := e.createRoleSession(ReviewerRole, workflow)
		reviewPrompt := e.buildReviewPrompt(workflow, *nextTask, devResult)
		reviewResult := e.callLLM(context.Background(), reviewSession, reviewPrompt)

		if strings.Contains(reviewResult, "APPROVED") {
			
			coordPrompt := fmt.Sprintf("Этап %s выполнен и одобрен ревьюером.\n\n%s\n\nВыбери следующий этап или заверши workflow.", nextTask.ID, reviewResult)
			coordResult2 := e.callLLM(context.Background(), coordSession, coordPrompt)

			if strings.Contains(coordResult2, "COMPLETE") || strings.Contains(coordResult2, "END") {
				
				e.notifyPeer(peerID, "✅ Workflow завершён:")
				e.notifyPeer(peerID, coordResult2)
				break
			}
		} else {
			
			e.notifyPeer(peerID, fmt.Sprintf("🔄 Этап %s требует доработки.\n\nРевьюер:\n%s", nextTask.ID, reviewResult))

			
			fixPrompt := fmt.Sprintf("Ревьюер нашёл замечания:\n\n%s\n\nИсправь по каждому пункту.", reviewResult)
			devResult2 := e.callLLM(context.Background(), devSession, fixPrompt)

			
			reviewResult2 := e.callLLM(context.Background(), reviewSession, reviewPrompt+"\n\n\n\nПЕРЕВЕРЬ ИСПРАВЛЕНИЯ:\n"+devResult2)

			if !strings.Contains(reviewResult2, "APPROVED") {
				e.notifyPeer(peerID, fmt.Sprintf("⚠ Этап %s не прошёл повторный ревью. Обратись к тимлиду вручную.", nextTask.ID))
				break
			}
		}
	}

	workflow.Status = WorkflowCompleted
	workflow.UpdatedAt = time.Now()
	e.saveWorkflow(workflow)
	e.notifyPeer(peerID, "🏁 Workflow завершён.")
}


func (e *WorkflowEngine) findNextTask(workflow *Workflow) *Task {
	for i := 0; i < len(workflow.Tasks); i++ {
		t := workflow.Tasks[i]
		if t.Status == TaskPending {
			return &t
		}
	}
	return nil
}


func (e *WorkflowEngine) createRoleSession(role Role, workflow *Workflow) *Session {
	var systemPrompt string
	switch role {
	case CoordinatorRole:
		systemPrompt = CoordinatorPrompt
	case DeveloperRole:
		systemPrompt = DeveloperPrompt
	case ReviewerRole:
		systemPrompt = ReviewerPrompt
	}

	
	return &Session{
		SystemPrompt: systemPrompt,
		Role:         role,
	}
}


func (e *WorkflowEngine) callLLM(ctx context.Context, sess *Session, prompt string) string {
	
	
	sess.AddUserMessage(prompt)
	return "[COPILOT] " + prompt[:min(60, len(prompt))] + "... [END]"
}


func (e *WorkflowEngine) buildDevPrompt(workflow *Workflow, task Task) string {
	
	var prevResults strings.Builder
	for _, t := range workflow.Tasks {
		if t.Status == TaskApproved && t.ID != task.ID {
			prevResults.WriteString(fmt.Sprintf("\n\n### Этап %s (готов):\n%s\n", t.ID, t.Result))
		}
	}

	return fmt.Sprintf("%s\n\n=== ТЕКУЩАЯ ЗАДАЧА ===\nID: %s\nTitle: %s\nDescription: %s\n\n=== КОНТЕКСТ (предыдущие этапы) ===\n%s\n\nРеализуй задачу. Используй доступные инструменты (file_write, edit, shell_execute). Не пиши рассуждений — только действия.",
		DeveloperPrompt, task.ID, task.Title, task.Description, prevResults.String())
}


func (e *WorkflowEngine) buildReviewPrompt(workflow *Workflow, task Task, devResult string) string {
	return fmt.Sprintf("%s\n\n=== ЗАДАЧА ===\n%s\n\n=== РЕЗУЛЬТАТ РАЗРАБОТКИ ===\n%s\n\n=== ТРЕБОВАНИЯ К РЕВЬЮ ===\n1. Соответствие ТЗ\n2. Безопасность\n3. Качество кода\n4. Обработка ошибок\n\nВерни вердикт: APPROVED или NEEDS_REVISION + конкретные замечания.",
		ReviewerPrompt, task.Description, devResult)
}


func (e *WorkflowEngine) parseTasksResponse(response string) []Task {
	var tasks []Task

	
	if strings.Contains(response, "[") {
		idx := strings.Index(response, "[")
		if err := json.Unmarshal([]byte(response[idx:]), &tasks); err == nil {
			return tasks
		}
	}

	
	return []Task{
		{
			ID:          "task_1",
			Title:       "Основная задача",
			Description: response,
			Status:      TaskPending,
		},
	}
}


func (e *WorkflowEngine) notifyPeer(peerID int64, message string) {
	fmt.Printf("[WORKFLOW notify peer=%d] %s\n", peerID, message)
	if e.notifier != nil {
		e.notifier.SendMessage(peerID, message)
	}
}


func (e *WorkflowEngine) saveWorkflow(wf *Workflow) {
	data, _ := json.MarshalIndent(wf, "", "  ")
	dir := e.mgr.GetWorkflowDir(wf.ID)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "workflow.json"), data, 0644)
}


