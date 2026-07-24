package agent

import (
	"fmt"
	"sync"
	"time"
)

// BackgroundTask представляет фоновую задачу
type BackgroundTask struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Prompt      string    `json:"prompt"`
	AgentType   string    `json:"agent_type"`
	Status      string    `json:"status"` // running, completed, error
	Output      string    `json:"output"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

// TaskManager управляет фоновыми задачами
type TaskManager struct {
	tasks    map[string]*BackgroundTask
	mu       sync.RWMutex
}

// NewTaskManager создает менеджер задач
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasks: make(map[string]*BackgroundTask),
	}
}

// CreateTask создает новую фоновую задачу
func (tm *TaskManager) CreateTask(description, prompt, agentType string) *BackgroundTask {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	task := &BackgroundTask{
		ID:          fmt.Sprintf("task_%d", time.Now().UnixMilli()),
		Description: description,
		Prompt:      prompt,
		AgentType:   agentType,
		Status:      "running",
		StartedAt:   time.Now(),
	}
	
	tm.tasks[task.ID] = task
	return task
}

// GetTask возвращает задачу по ID
func (tm *TaskManager) GetTask(id string) *BackgroundTask {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.tasks[id]
}

// CompleteTask завершает задачу
func (tm *TaskManager) CompleteTask(id, output string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	if task, ok := tm.tasks[id]; ok {
		task.Status = "completed"
		task.Output = output
		task.CompletedAt = time.Now()
	}
}

// FailTask помечает задачу как errored
func (tm *TaskManager) FailTask(id, output string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	
	if task, ok := tm.tasks[id]; ok {
		task.Status = "error"
		task.Output = output
		task.CompletedAt = time.Now()
	}
}

// ListTasks возвращает все задачи
func (tm *TaskManager) ListTasks() []*BackgroundTask {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	var result []*BackgroundTask
	for _, task := range tm.tasks {
		result = append(result, task)
	}
	return result
}

// ListRunningTasks возвращает запущенные задачи
func (tm *TaskManager) ListRunningTasks() []*BackgroundTask {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	
	var result []*BackgroundTask
	for _, task := range tm.tasks {
		if task.Status == "running" {
			result = append(result, task)
		}
	}
	return result
}
