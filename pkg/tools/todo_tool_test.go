package tools

import (
	"context"
	"strings"
	"testing"
)

func TestTodoToolAdd(t *testing.T) {
	tool := &TodoTool{}

	result, err := tool.Execute(context.Background(), map[string]string{
		"operation": "add",
		"task":      "Write tests for the module",
		"agent":     "developer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("add failed: %s", result.Error)
	}
	data := result.Data.(map[string]interface{})
	if data["id"].(int) != 1 {
		t.Errorf("expected id=1, got %d", data["id"])
	}
	if data["status"] != "pending" {
		t.Errorf("expected status=pending, got %v", data["status"])
	}
	if data["task"] != "Write tests for the module" {
		t.Errorf("unexpected task: %v", data["task"])
	}
}

func TestTodoToolAddMissingTask(t *testing.T) {
	tool := &TodoTool{}

	result, err := tool.Execute(context.Background(), map[string]string{
		"operation": "add",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Fatal("expected failure for missing task")
	}
}

func TestTodoToolUpdateStatus(t *testing.T) {
	tool := &TodoTool{}

	tool.Execute(context.Background(), map[string]string{
		"operation": "add",
		"task":      "Implement feature X",
	})

	result, err := tool.Execute(context.Background(), map[string]string{
		"operation": "update",
		"id":        "1",
		"status":    "in_progress",
		"agent":     "developer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("update failed: %s", result.Error)
	}
	data := result.Data.(map[string]interface{})
	if data["status"] != "in_progress" {
		t.Errorf("expected status=in_progress, got %v", data["status"])
	}
}

func TestTodoToolUpdateNotFound(t *testing.T) {
	tool := &TodoTool{}

	result, err := tool.Execute(context.Background(), map[string]string{
		"operation": "update",
		"id":        "999",
		"status":    "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Fatal("expected failure for non-existent task")
	}
}

func TestTodoToolUpdateMissingID(t *testing.T) {
	tool := &TodoTool{}

	result, err := tool.Execute(context.Background(), map[string]string{
		"operation": "update",
		"status":    "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Fatal("expected failure for missing id")
	}
}

func TestTodoToolList(t *testing.T) {
	tool := &TodoTool{}

	tool.Execute(context.Background(), map[string]string{
		"operation": "add",
		"task":      "Task 1",
	})
	tool.Execute(context.Background(), map[string]string{
		"operation": "add",
		"task":      "Task 2",
	})

	result, err := tool.Execute(context.Background(), map[string]string{
		"operation": "list",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("list failed: %s", result.Error)
	}
	data := result.Data.(map[string]interface{})
	if data["count"].(int) != 2 {
		t.Errorf("expected count=2, got %d", data["count"])
	}
	tasks, ok := data["tasks"].(string)
	if !ok || tasks == "" {
		t.Error("expected non-empty tasks string")
	}
}

func TestTodoToolListEmpty(t *testing.T) {
	tool := &TodoTool{}

	result, err := tool.Execute(context.Background(), map[string]string{
		"operation": "list",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("list failed: %s", result.Error)
	}
	data := result.Data.(map[string]interface{})
	if data["count"].(int) != 0 {
		t.Errorf("expected count=0, got %d", data["count"])
	}
}

func TestTodoToolUnknownOperation(t *testing.T) {
	tool := &TodoTool{}

	result, err := tool.Execute(context.Background(), map[string]string{
		"operation": "delete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Fatal("expected failure for unknown operation")
	}
}

func TestTodoToolFullPipeline(t *testing.T) {
	tool := &TodoTool{}

	
	r1, _ := tool.Execute(context.Background(), map[string]string{
		"operation": "add",
		"task":      "Write code",
		"agent":     "developer",
	})
	if !r1.Success {
		t.Fatal("add 1 failed")
	}

	r2, _ := tool.Execute(context.Background(), map[string]string{
		"operation": "add",
		"task":      "Review code",
		"agent":     "reviewer",
	})
	if !r2.Success {
		t.Fatal("add 2 failed")
	}

	r3, _ := tool.Execute(context.Background(), map[string]string{
		"operation": "add",
		"task":      "Run tests",
		"agent":     "qa",
	})
	if !r3.Success {
		t.Fatal("add 3 failed")
	}

	
	tool.Execute(context.Background(), map[string]string{
		"operation": "update",
		"id":        "1",
		"status":    "in_progress",
	})

	
	tool.Execute(context.Background(), map[string]string{
		"operation": "update",
		"id":        "1",
		"status":    "completed",
	})

	
	listResult, _ := tool.Execute(context.Background(), map[string]string{
		"operation": "list",
	})
	data := listResult.Data.(map[string]interface{})
	count := data["count"].(int)
	if count != 3 {
		t.Errorf("expected 3 tasks, got %d", count)
	}
	tasks := data["tasks"].(string)
	if !contains(tasks, "[1] completed") {
		t.Error("task 1 should be completed")
	}
	if !contains(tasks, "[2] pending") {
		t.Error("task 2 should be pending")
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestGlobalTodoIsSingleton(t *testing.T) {
	if GlobalTodo == nil {
		t.Fatal("GlobalTodo should not be nil")
	}
	if GlobalTodo.Name() != "todo" {
		t.Errorf("Name: got %q, want 'todo'", GlobalTodo.Name())
	}
}

func TestTodoToolReset(t *testing.T) {
	tool := &TodoTool{}

	tool.Execute(context.Background(), map[string]string{
		"operation": "add",
		"task":      "Task 1",
	})
	tool.Execute(context.Background(), map[string]string{
		"operation": "add",
		"task":      "Task 2",
	})

	
	listResult, _ := tool.Execute(context.Background(), map[string]string{"operation": "list"})
	if listResult.Data.(map[string]interface{})["count"].(int) != 2 {
		t.Fatal("expected 2 tasks before reset")
	}

	tool.Reset()

	
	listResult, _ = tool.Execute(context.Background(), map[string]string{"operation": "list"})
	data := listResult.Data.(map[string]interface{})
	if data["count"].(int) != 0 {
		t.Errorf("expected 0 tasks after reset, got %d", data["count"])
	}

	
	addResult, _ := tool.Execute(context.Background(), map[string]string{
		"operation": "add",
		"task":      "New task after reset",
	})
	if addResult.Data.(map[string]interface{})["id"].(int) != 1 {
		t.Errorf("expected id=1 after reset, got %d", addResult.Data.(map[string]interface{})["id"])
	}
}

func TestGlobalTodoReset(t *testing.T) {
	GlobalTodo.Execute(context.Background(), map[string]string{
		"operation": "add",
		"task":      "Test task",
	})

	GlobalTodo.Reset()

	listResult, _ := GlobalTodo.Execute(context.Background(), map[string]string{"operation": "list"})
	count := listResult.Data.(map[string]interface{})["count"].(int)
	if count != 0 {
		t.Errorf("GlobalTodo should be empty after Reset, got %d tasks", count)
	}
}
