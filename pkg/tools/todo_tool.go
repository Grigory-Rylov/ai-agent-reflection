package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type TodoItem struct {
	ID      int
	Task    string
	Status  string // pending, in_progress, completed, failed
	Agent   string
}

type TodoTool struct {
	mu      sync.Mutex
	items   []TodoItem
	nextID  int
}

func (t *TodoTool) Name() string {
	return "todo"
}

func (t *TodoTool) Description() string {
	return "Track task status across the pipeline. Operations: add, update, list."
}

func (t *TodoTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"operation": CreateEnumParameter("operation", "Operation: add, update, list", []string{"add", "update", "list"}, true),
			"task":      CreateStringParameter("task", "Task description (required for add/update)", false),
			"status":    CreateEnumParameter("status", "Status: pending, in_progress, completed, failed", []string{"pending", "in_progress", "completed", "failed"}, false),
			"agent":     CreateStringParameter("agent", "Agent name assigned to the task", false),
			"id":        CreateIntegerParameter("id", "Task ID (required for update)", false),
		},
		"required": []string{"operation"},
	}
}

func (t *TodoTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	op := inputs["operation"]
	t.mu.Lock()
	defer t.mu.Unlock()

	switch op {
	case "add":
		task := inputs["task"]
		if task == "" {
			return ToolResult{Success: false, Error: "task is required for add"}, nil
		}
		t.nextID++
		item := TodoItem{ID: t.nextID, Task: task, Status: "pending", Agent: inputs["agent"]}
		t.items = append(t.items, item)
		return ToolResult{Success: true, Data: map[string]interface{}{
			"id": t.nextID, "task": task, "status": "pending",
		}}, nil

	case "update":
		id := 0
		fmt.Sscanf(inputs["id"], "%d", &id)
		if id == 0 {
			return ToolResult{Success: false, Error: "id is required for update"}, nil
		}
		for i := range t.items {
			if t.items[i].ID == id {
				if s, ok := inputs["status"]; ok && s != "" {
					t.items[i].Status = s
				}
				if a, ok := inputs["agent"]; ok {
					t.items[i].Agent = a
				}
				return ToolResult{Success: true, Data: map[string]interface{}{
					"id": id, "task": t.items[i].Task, "status": t.items[i].Status,
				}}, nil
			}
		}
		return ToolResult{Success: false, Error: fmt.Sprintf("task %d not found", id)}, nil

	case "list":
		var b strings.Builder
		if len(t.items) == 0 {
			b.WriteString("No tasks.")
		} else {
			for _, item := range t.items {
				b.WriteString(fmt.Sprintf("[%d] %s | %s | %s\n", item.ID, item.Status, item.Agent, item.Task))
			}
		}
		return ToolResult{Success: true, Data: map[string]interface{}{
			"tasks": b.String(), "count": len(t.items),
		}}, nil
	}

	return ToolResult{Success: false, Error: fmt.Sprintf("unknown operation: %s", op)}, nil
}

var GlobalTodo = &TodoTool{}
