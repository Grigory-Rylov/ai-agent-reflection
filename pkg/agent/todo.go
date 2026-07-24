package agent

import (
	"fmt"
	"sync"
)

// TodoItem - элемент списка задач
type TodoItem struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
}

// TodoManager управляет списком задач
type TodoManager struct {
	todos []TodoItem
	mu    sync.RWMutex
}

// NewTodoManager создает менеджер задач
func NewTodoManager() *TodoManager {
	return &TodoManager{
		todos: make([]TodoItem, 0),
	}
}

// UpdateTodos обновляет весь список задач
func (tm *TodoManager) UpdateTodos(todos []TodoItem) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.todos = todos
}

// GetTodos возвращает текущий список
func (tm *TodoManager) GetTodos() []TodoItem {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.todos
}

// FormatTodos форматирует список для вывода
func (tm *TodoManager) FormatTodos() string {
	todos := tm.GetTodos()
	if len(todos) == 0 {
		return "Задач пока нет."
	}
	
	var result string
	for _, t := range todos {
		status := "○"
		switch t.Status {
		case "completed":
			status = "●"
		case "in_progress":
			status = "◐"
		case "cancelled":
			status = "◌"
		}
		result += fmt.Sprintf("%s %s\n", status, t.Content)
	}
	return result
}
