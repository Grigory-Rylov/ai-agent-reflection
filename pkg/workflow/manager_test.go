package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// ============================================================
// Tests — Workflow Manager
// ============================================================

func TestWorkflowManager_New(t *testing.T) {
	mgr := NewWorkflowManager("")
	if mgr.baseDir != "./workflows" {
		t.Errorf("expected baseDir ./workflows, got %s", mgr.baseDir)
	}
	if mgr.workflows == nil {
		t.Error("expected workflows map to be initialized")
	}
	if mgr.peers == nil {
		t.Error("expected peers map to be initialized")
	}
}

func TestWorkflowManager_NewWithCustomBaseDir(t *testing.T) {
	mgr := NewWorkflowManager("/tmp/test_workflows")
	if mgr.baseDir != "/tmp/test_workflows" {
		t.Errorf("expected baseDir /tmp/test_workflows, got %s", mgr.baseDir)
	}
}

func TestWorkflowManager_CreateWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(12345, "Написать HTTP сервер на Go")

	if wf.ID == "" {
		t.Error("expected non-empty workflow ID")
	}
	if wf.UserPeerID != 12345 {
		t.Errorf("expected peerID 12345, got %d", wf.UserPeerID)
	}
	if wf.Status != WorkflowPending {
		t.Errorf("expected status WorkflowPending, got %s", wf.Status)
	}
	if wf.UserOriginal != "Написать HTTP сервер на Go" {
		t.Error("expected UserOriginal to match")
	}
	if len(wf.Tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(wf.Tasks))
	}
	if wf.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if wf.UpdatedAt.IsZero() {
		t.Error("expected non-zero UpdatedAt")
	}
}

func TestWorkflowManager_GetWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(99, "Задача")

	got := mgr.GetWorkflow(wf.ID)
	if got == nil {
		t.Fatal("expected workflow to exist")
	}
	if got.ID != wf.ID {
		t.Error("expected same workflow")
	}

	nilWf := mgr.GetWorkflow("nonexistent")
	if nilWf != nil {
		t.Error("expected nil for nonexistent workflow")
	}
}

func TestWorkflowManager_GetUserWorkflows(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	_ = mgr.CreateWorkflow(123, "Задача 1")
	_ = mgr.CreateWorkflow(123, "Задача 2")
	_ = mgr.CreateWorkflow(456, "Задача 3")

	wfs := mgr.GetUserWorkflows(123)
	if len(wfs) != 2 {
		t.Errorf("expected 2 workflows for peer 123, got %d", len(wfs))
	}

	wfsOther := mgr.GetUserWorkflows(456)
	if len(wfsOther) != 1 {
		t.Errorf("expected 1 workflow for peer 456, got %d", len(wfsOther))
	}

	wfsOther = mgr.GetUserWorkflows(789)
	if len(wfsOther) != 0 {
		t.Error("expected 0 workflows for unknown peer")
	}

	}

func TestWorkflowManager_AddTask(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")
	task := Task{
		Title:       "Тестовая задача",
		Description: "Описание задачи",
		Assignee:    DeveloperRole,
	}

	err := mgr.AddTask(wf.ID, task)
	if err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}

	wf = mgr.GetWorkflow(wf.ID)
	if len(wf.Tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(wf.Tasks))
	}

	got := wf.Tasks[0]
	if got.Title != "Тестовая задача" {
		t.Errorf("expected title 'Тестовая задача', got %q", got.Title)
	}
	if got.Status != TaskPending {
		t.Errorf("expected status TaskPending, got %s", got.Status)
	}
	if got.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestWorkflowManager_AddTask_FailsForNonexistentWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	task := Task{Title: "T"}
	err := mgr.AddTask("nonexistent", task)
	if err == nil {
		t.Error("expected error for nonexistent workflow")
	}
}

func TestWorkflowManager_UpdateTaskStatus(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")

	task := Task{Title: "Тест", Assignee: DeveloperRole}
	mgr.AddTask(wf.ID, task)

	wf = mgr.GetWorkflow(wf.ID)
	taskID := wf.Tasks[0].ID

	err := mgr.UpdateTaskStatus(wf.ID, taskID, TaskApproved, "", "Результат")
	if err != nil {
		t.Fatalf("UpdateTaskStatus failed: %v", err)
	}

	wf = mgr.GetWorkflow(wf.ID)
	updated := wf.Tasks[0]
	if updated.Status != TaskApproved {
		t.Errorf("expected status TaskApproved, got %s", updated.Status)
	}
	if updated.Result != "Результат" {
		t.Errorf("expected result 'Результат', got %q", updated.Result)
	}
	if updated.ApprovedAt == nil {
		t.Error("expected ApprovedAt to be set")
	}
}

func TestWorkflowManager_UpdateTaskStatus_NeedsRevision(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")
	wf.CurrentTaskIdx = 0

	task := Task{Title: "Тест", Assignee: DeveloperRole}
	mgr.AddTask(wf.ID, task)

	wf = mgr.GetWorkflow(wf.ID)
	taskID := wf.Tasks[0].ID

	err := mgr.UpdateTaskStatus(wf.ID, taskID, TaskNeedsRevision, "Плохо написано", "")
	if err != nil {
		t.Fatalf("UpdateTaskStatus failed: %v", err)
	}

	wf = mgr.GetWorkflow(wf.ID)
	updated := wf.Tasks[0]
	if updated.Status != TaskNeedsRevision {
		t.Errorf("expected status TaskNeedsRevision, got %s", updated.Status)
	}
	if updated.Feedback != "Плохо написано" {
		t.Errorf("expected feedback 'Плохо написано', got %q", updated.Feedback)
	}
}

func TestWorkflowManager_UpdateTaskStatus_FailsForNonexistentWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	err := mgr.UpdateTaskStatus("nonexistent", "task_1", TaskApproved, "", "R")
	if err == nil {
		t.Error("expected error for nonexistent workflow")
	}
}

func TestWorkflowManager_UpdateTaskStatus_FailsForNonexistentTask(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")

	err := mgr.UpdateTaskStatus(wf.ID, "nonexistent", TaskApproved, "", "R")
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestWorkflowManager_GetNextTaskToExecute(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")

	mgr.AddTask(wf.ID, Task{Title: "T1", Status: TaskPending, Assignee: DeveloperRole})
	mgr.AddTask(wf.ID, Task{Title: "T2", Status: TaskPending, Assignee: DeveloperRole})
	mgr.AddTask(wf.ID, Task{Title: "T3", Status: TaskApproved, Assignee: DeveloperRole})

	next, err := mgr.GetNextTaskToExecute(wf.ID)
	if err != nil {
		t.Fatalf("GetNextTaskToExecute failed: %v", err)
	}
	if next == nil {
		t.Fatal("expected next task to exist")
	}
	if next.Title != "T1" {
		t.Errorf("expected task T1, got %q", next.Title)
	}
}

func TestWorkflowManager_GetNextTaskToExecute_NoneLeft(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")

	mgr.AddTask(wf.ID, Task{Title: "T1", Status: TaskApproved, Assignee: DeveloperRole})

	next, err := mgr.GetNextTaskToExecute(wf.ID)
	if err != nil {
		t.Fatalf("GetNextTaskToExecute failed: %v", err)
	}
	if next != nil {
		t.Error("expected nil for no pending tasks")
	}
}

func TestWorkflowManager_GetAllApprovedTasks(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")

	mgr.AddTask(wf.ID, Task{Title: "T1", Status: TaskPending, Assignee: DeveloperRole})
	mgr.AddTask(wf.ID, Task{Title: "T2", Status: TaskApproved, Assignee: DeveloperRole})
	mgr.AddTask(wf.ID, Task{Title: "T3", Status: TaskApproved, Assignee: DeveloperRole})

	approved := mgr.GetAllApprovedTasks(wf.ID)
	if len(approved) != 2 {
		t.Errorf("expected 2 approved tasks, got %d", len(approved))
	}
	for _, ta := range approved {
		if ta.Status != TaskApproved {
			t.Errorf("expected all tasks approved, got %q", ta.Title)
		}
	}
}

func TestWorkflowManager_CompleteWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")

	err := mgr.CompleteWorkflow(wf.ID, "Всё сделано")
	if err != nil {
		t.Fatalf("CompleteWorkflow failed: %v", err)
	}

	wf = mgr.GetWorkflow(wf.ID)
	if wf.Status != WorkflowCompleted {
		t.Errorf("expected WorkflowCompleted, got %s", wf.Status)
	}
	if wf.Summary != "Всё сделано" {
		t.Errorf("expected summary 'Всё сделано', got %q", wf.Summary)
	}
}

func TestWorkflowManager_CompleteWorkflow_FailsForNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	err := mgr.CompleteWorkflow("nonexistent", "")
	if err == nil {
		t.Error("expected error for nonexistent workflow")
	}
}

func TestWorkflowManager_StartWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")

	err := mgr.StartWorkflow(wf.ID)
	if err != nil {
		t.Fatalf("StartWorkflow failed: %v", err)
	}

	wf = mgr.GetWorkflow(wf.ID)
	if wf.Status != WorkflowInProgress {
		t.Errorf("expected WorkflowInProgress, got %s", wf.Status)
	}
}

func TestWorkflowManager_StartWorkflow_FailsForNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	err := mgr.StartWorkflow("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent workflow")
	}
}

func TestWorkflowManager_StartWorkflow_FailsForAlreadyStarted(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")

	mgr.StartWorkflow(wf.ID)

	err := mgr.StartWorkflow(wf.ID)
	if err == nil {
		t.Error("expected error for already started workflow")
	}
}

func TestWorkflowManager_CancelWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")

	err := mgr.CancelWorkflow(wf.ID)
	if err != nil {
		t.Fatalf("CancelWorkflow failed: %v", err)
	}

	wf = mgr.GetWorkflow(wf.ID)
	if wf.Status != WorkflowCancelled {
		t.Errorf("expected WorkflowCancelled, got %s", wf.Status)
	}
}

func TestWorkflowManager_CancelWorkflow_FailsForNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	err := mgr.CancelWorkflow("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent workflow")
	}
}

func TestWorkflowManager_SaveAndLoadWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")
	wf.Tasks = append(wf.Tasks, Task{Title: "Тест", Description: "Описание", Assignee: DeveloperRole})

	err := mgr.SaveWorkflow(wf)
	if err != nil {
		t.Fatalf("SaveWorkflow failed: %v", err)
	}

	loaded, err := mgr.LoadWorkflow(wf.ID)
	if err != nil {
		t.Fatalf("LoadWorkflow failed: %v", err)
	}

	if loaded.ID != wf.ID {
		t.Error("expected same ID")
	}
	if loaded.UserPeerID != wf.UserPeerID {
		t.Error("expected same peerID")
	}
	if len(loaded.Tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(loaded.Tasks))
	}
	if loaded.Tasks[0].Title != "Тест" {
		t.Error("expected same task title")
	}
}

func TestWorkflowManager_SaveAndLoadWorkflow_Nonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	_, err := mgr.LoadWorkflow("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent workflow")
	}
}

func TestWorkflowManager_SaveAndLoadArtifact(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")
	taskID := "task_1"
	content := "package main\nfunc main() {}"

	err := mgr.SaveArtifact(wf.ID, "main.go", content, taskID)
	if err != nil {
		t.Fatalf("SaveArtifact failed: %v", err)
	}

	got, err := mgr.ReadArtifact(wf.ID, taskID, "main.go")
	if err != nil {
		t.Fatalf("ReadArtifact failed: %v", err)
	}
	if got != content {
		t.Errorf("expected same content, got %q", got)
	}
}

func TestWorkflowManager_SaveAndLoadArtifact_SpecialFilename(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")
	taskID := "task_1"
	content := "тест"

	err := mgr.SaveArtifact(wf.ID, "тест.txt", content, taskID)
	if err != nil {
		t.Fatalf("SaveArtifact failed: %v", err)
	}

	// Файл должен быть сохранён (sanitizeFilename заменит спецсимволы)
	files, err := mgr.ListWorkflowArtifacts(wf.ID, taskID)
	if err != nil {
		t.Fatalf("ListWorkflowArtifacts failed: %v", err)
	}
	if len(files) == 0 {
		t.Error("expected at least 1 file")
	}
}

func TestWorkflowManager_ListWorkflowArtifacts_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")

	files, err := mgr.ListWorkflowArtifacts(wf.ID, "task_1")
	if err != nil {
		t.Fatalf("ListWorkflowArtifacts failed: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestWorkflowManager_GetWorkflowDir(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	dir := mgr.GetWorkflowDir("wf_123")
	expected := filepath.Join(tmpDir, "wf_123")
	if dir != expected {
		t.Errorf("expected %s, got %s", expected, dir)
	}
}

func TestWorkflowManager_MultiPeerWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	_ = mgr.CreateWorkflow(111, "Задача 1")
	_ = mgr.CreateWorkflow(222, "Задача 2")
	_ = mgr.CreateWorkflow(111, "Задача 3")

	wfs111 := mgr.GetUserWorkflows(111)
	if len(wfs111) != 2 {
		t.Errorf("expected 2 workflows for peer 111, got %d", len(wfs111))
	}

	wfs222 := mgr.GetUserWorkflows(222)
	if len(wfs222) != 1 {
		t.Errorf("expected 1 workflow for peer 222, got %d", len(wfs222))
	}
}

func TestWorkflowManager_WorkflowJSONRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(456, "Задача с \"кавычками\" и <тегами>")
	wf.Tasks = append(wf.Tasks, Task{
		Title:       "Тест",
		Description: "Описание с JSON { \"key\": \"value\" }",
		Assignee:    DeveloperRole,
		Status:      TaskPending,
	})

	err := mgr.SaveWorkflow(wf)
	if err != nil {
		t.Fatalf("SaveWorkflow failed: %v", err)
	}

	loaded, err := mgr.LoadWorkflow(wf.ID)
	if err != nil {
		t.Fatalf("LoadWorkflow failed: %v", err)
	}

	if loaded.UserOriginal != wf.UserOriginal {
		t.Error("expected exact UserOriginal match")
	}
	if loaded.Tasks[0].Title != "Тест" {
		t.Error("expected same task title")
	}
	if loaded.Tasks[0].Description != "Описание с JSON { \"key\": \"value\" }" {
		t.Error("expected same task description")
	}
}

func TestWorkflowManager_EdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	// Создаём workflow с пустым userOriginal
	wf := mgr.CreateWorkflow(789, "")
	if wf.UserOriginal != "" {
		t.Error("expected empty UserOriginal")
	}

	// Добавляем задачу с пустым описанием
	err := mgr.AddTask(wf.ID, Task{Title: "T", Assignee: DeveloperRole})
	if err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}

	// Обновляем статус
	wf = mgr.GetWorkflow(wf.ID)
	err = mgr.UpdateTaskStatus(wf.ID, wf.Tasks[0].ID, TaskApproved, "", "")
	if err != nil {
		t.Fatalf("UpdateTaskStatus failed: %v", err)
	}
}

func TestSanitizeFilename(t *testing.T) {
	if sanitizeFilename("main.go") != "main.go" {
		t.Error("expected 'main.go'")
	}
	if sanitizeFilename("тест.txt") == "тест.txt" {
		t.Error("expected sanitized filename for non-ASCII")
	}
	if sanitizeFilename("../../../etc/passwd") != "passwd" {
		t.Error("expected path traversal to be stripped")
	}
	if sanitizeFilename("") != "" {
		t.Error("expected empty string for empty input")
	}
}

func TestWorkflowStatus_Constants(t *testing.T) {
	if WorkflowPending != "pending" {
		t.Error("expected 'pending'")
	}
	if WorkflowInProgress != "in_progress" {
		t.Error("expected 'in_progress'")
	}
	if WorkflowCompleted != "completed" {
		t.Error("expected 'completed'")
	}
	if WorkflowFailed != "failed" {
		t.Error("expected 'failed'")
	}
	if WorkflowCancelled != "cancelled" {
		t.Error("expected 'cancelled'")
	}
}

func TestTaskStatus_Constants(t *testing.T) {
	if TaskPending != "pending" {
		t.Error("expected 'pending'")
	}
	if TaskInProgress != "in_progress" {
		t.Error("expected 'in_progress'")
	}
	if TaskApproved != "approved" {
		t.Error("expected 'approved'")
	}
	if TaskNeedsRevision != "needs_revision" {
		t.Error("expected 'needs_revision'")
	}
	if TaskCoordReview != "coord_review" {
		t.Error("expected 'coord_review'")
	}
}

func TestRole_Constants(t *testing.T) {
	if CoordinatorRole != "coordinator" {
		t.Error("expected 'coordinator'")
	}
	if DeveloperRole != "developer" {
		t.Error("expected 'developer'")
	}
	if ReviewerRole != "reviewer" {
		t.Error("expected 'reviewer'")
	}
}

func TestWorkflow_PartialWorkflowCompletion(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")

	// Добавляем 3 задачи
	for i := 1; i <= 3; i++ {
		mgr.AddTask(wf.ID, Task{
			Title:      fmt.Sprintf("Задача %d", i),
			Assignee:   DeveloperRole,
			Status:     TaskPending,
		})
	}

	// Одобрим первую
	wf = mgr.GetWorkflow(wf.ID)
	t0 := wf.Tasks[0].ID
	mgr.UpdateTaskStatus(wf.ID, t0, TaskApproved, "", "Результат 1")

	// Вторая — revision
	t1 := wf.Tasks[1].ID
	mgr.UpdateTaskStatus(wf.ID, t1, TaskNeedsRevision, "Исправь код", "")

	// Получим следующую на выполнение — должна быть вторая
	next, err := mgr.GetNextTaskToExecute(wf.ID)
	if err != nil {
		t.Fatalf("GetNextTaskToExecute failed: %v", err)
	}
	if next == nil {
		t.Fatal("expected second task")
	}
	if next.Title != "Задача 2" {
		t.Errorf("expected 'Задача 2', got %q", next.Title)
	}

	// Первая должна быть одобрена
	approved := mgr.GetAllApprovedTasks(wf.ID)
	if len(approved) != 1 {
		t.Errorf("expected 1 approved task, got %d", len(approved))
	}
}

func TestWorkflowManager_SaveWorkflow_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewWorkflowManager(tmpDir)

	wf := mgr.CreateWorkflow(123, "Задача")
	err := mgr.SaveWorkflow(wf)
	if err != nil {
		t.Fatalf("SaveWorkflow failed: %v", err)
	}

	// Проверяем что директория создана
	dir := mgr.GetWorkflowDir(wf.ID)
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Directory should exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("path should be a directory")
	}

	// Проверяем что файл создан
	data, err := os.ReadFile(filepath.Join(dir, "workflow.json"))
	if err != nil {
		t.Fatalf("workflow.json should exist: %v", err)
	}
	if len(data) == 0 {
		t.Error("workflow.json should not be empty")
	}

	// Проверяем что это валидный JSON
	var decoded Workflow
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("workflow.json should be valid JSON: %v", err)
	}
}
