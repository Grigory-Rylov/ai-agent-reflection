package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ============================================================
// Типы и константы
// ============================================================

// Role определяет роль агента в workflow
type Role string

const (
	CoordinatorRole Role = "coordinator"
	DeveloperRole   Role = "developer"
	ReviewerRole    Role = "reviewer"
)

// WorkflowStatus определяет статус текущего workflow
type WorkflowStatus string

const (
	WorkflowPending    WorkflowStatus = "pending"     // создан, но не запущен
	WorkflowInProgress WorkflowStatus = "in_progress" // выполняется
	WorkflowCompleted  WorkflowStatus = "completed"   // все задачи выполнены
	WorkflowFailed     WorkflowStatus = "failed"      // произошла ошибка
	WorkflowCancelled  WorkflowStatus = "cancelled"   // отменён
)

// TaskStatus определяет статус конкретной задачи
type TaskStatus string

const (
	TaskPending         TaskStatus = "pending"          // ожидает выполнения
	TaskInProgress      TaskStatus = "in_progress"      // выполняется
	TaskApproved        TaskStatus = "approved"         // одобрена ревьюером
	TaskNeedsRevision   TaskStatus = "needs_revision"   // требует доработки
	TaskCoordReview     TaskStatus = "coord_review"     // на проверке у координатора
)

// ============================================================
// Структуры данных
// ============================================================

// Workflow представляет полный цикл работы над задачей пользователя
type Workflow struct {
	ID             string         `json:"id"`
	UserPeerID     int64          `json:"user_peer_id"`
	UserOriginal   string         `json:"user_original"`       // исходный запрос пользователя
	Status         WorkflowStatus `json:"status"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	CurrentTaskIdx int            `json:"current_task_idx"`  // индекс текущей задачи
	TotalTasks     int            `json:"-"`
	Tasks          []Task         `json:"tasks"`
	Artifacts      []Artifact     `json:"artifacts"`
	LastStatus     string         `json:"last_status"`    // для уведомления пользователя
	Summary        string         `json:"summary"`        // финальная сводка
}

// Task представляет отдельный этап работы
type Task struct {
	ID             string      `json:"id"`
	Title          string      `json:"title"`
	Description    string      `json:"description"`
	Assignee       Role        `json:"assignee"`
	Status         TaskStatus  `json:"status"`
	Feedback       string      `json:"feedback,omitempty"` // замечания от ревьюера
	Result         string      `json:"result,omitempty"`   // результат выполнения
	Artifacts      []Artifact  `json:"artifacts,omitempty"`
	ReviewedBy     string      `json:"reviewed_by,omitempty"`
	ApprovedBy     string      `json:"approved_by,omitempty"`
	CreatedAt      time.Time   `json:"created_at"`
	StartedAt      *time.Time  `json:"started_at,omitempty"`
	CompletedAt    *time.Time  `json:"completed_at,omitempty"`
	ApprovedAt     *time.Time  `json:"approved_at,omitempty"`
}

// Artifact представляет результат работы (файл/код)
type Artifact struct {
	Name    string `json:"name"`
	Path    string `json:"path"`      // относительный путь внутри workflow dir
	Type    string `json:"type"`      // code, doc, config
	Content string `json:"content"`   // контент если небольшой
	SHA256  string `json:"sha256"`    // хеш для отслеживания изменений
}

// ============================================================
// WorkflowManager — управляет жизненным циклом workflow
// ============================================================

// WorkflowManager — центральный компонент мульти-агентной системы
type WorkflowManager struct {
	mu        sync.RWMutex
	workflows map[string]*Workflow // ID -> Workflow
	peers     map[int64][]*Workflow // peerID -> список workflow'ов пользователя
	baseDir   string                // где хранить файлы workflow'ов
}

// NewWorkflowManager создаёт новый менеджер workflow
func NewWorkflowManager(baseDir string) *WorkflowManager {
	if baseDir == "" {
		baseDir = "./workflows"
	}
	return &WorkflowManager{
		workflows: make(map[string]*Workflow),
		peers:     make(map[int64][]*Workflow),
		baseDir:   baseDir,
	}
}

// CreateWorkflow создаёт новый workflow и возвращает его
func (m *WorkflowManager) CreateWorkflow(peerID int64, userOriginal string) *Workflow {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := generateID()
	workflow := &Workflow{
		ID:           id,
		UserPeerID:   peerID,
		UserOriginal: userOriginal,
		Status:       WorkflowPending,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		CurrentTaskIdx: 0,
		Tasks:        make([]Task, 0),
		Artifacts:    make([]Artifact, 0),
	}

	m.workflows[id] = workflow
	m.peers[peerID] = append(m.peers[peerID], workflow)

	return workflow
}

// GetWorkflow возвращает workflow по ID или nil
func (m *WorkflowManager) GetWorkflow(id string) *Workflow {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.workflows[id]
}

// GetUserWorkflows возвращает все workflow'ы пользователя
func (m *WorkflowManager) GetUserWorkflows(peerID int64) []*Workflow {
	m.mu.RLock()
	defer m.mu.RUnlock()
	wfs := make([]*Workflow, len(m.peers[peerID]))
	copy(wfs, m.peers[peerID])
	return wfs
}

// StartWorkflow начинает выполнение workflow
func (m *WorkflowManager) StartWorkflow(id string) error {
	m.mu.Lock()
	workflow, ok := m.workflows[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("workflow not found: %s", id)
	}
	if workflow.Status != WorkflowPending {
		m.mu.Unlock()
		return fmt.Errorf("workflow already started: %s", id)
	}
	workflow.Status = WorkflowInProgress
	workflow.UpdatedAt = time.Now()
	m.mu.Unlock()

	// TODO: запустить координатора
	// go m.runCoordinator(workflow)

	return nil
}

// CancelWorkflow отменяет workflow
func (m *WorkflowManager) CancelWorkflow(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	workflow, ok := m.workflows[id]
	if !ok {
		return fmt.Errorf("workflow not found: %s", id)
	}

	workflow.Status = WorkflowCancelled
	workflow.UpdatedAt = time.Now()
	return nil
}

// AddTask добавляет новую задачу в workflow
func (m *WorkflowManager) AddTask(workflowID string, task Task) error {
	m.mu.Lock()
	workflow, ok := m.workflows[workflowID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("workflow not found: %s", workflowID)
	}

	task.ID = generateID()
	if task.Status == "" {
		task.Status = TaskPending
	}
	task.CreatedAt = time.Now()

	workflow.Tasks = append(workflow.Tasks, task)
	workflow.UpdatedAt = time.Now()
	m.mu.Unlock()
	return nil
}

// UpdateTaskStatus обновляет статус задачи
func (m *WorkflowManager) UpdateTaskStatus(workflowID, taskID string, status TaskStatus, feedback, result string) error {
	m.mu.Lock()
	workflow, ok := m.workflows[workflowID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("workflow not found: %s", workflowID)
	}

	for i := range workflow.Tasks {
		if workflow.Tasks[i].ID == taskID {
			workflow.Tasks[i].Status = status
			workflow.Tasks[i].Feedback = feedback
			workflow.Tasks[i].Result = result
			now := time.Now()
			if status == TaskApproved {
				workflow.Tasks[i].ApprovedAt = &now
			} else {
				workflow.Tasks[i].CompletedAt = &now
			}
			workflow.UpdatedAt = now

			if status == TaskNeedsRevision {
				workflow.CurrentTaskIdx = i // вернуться к этой задаче
			}
			m.mu.Unlock()
			return nil
		}
	}

	m.mu.Unlock()
	return fmt.Errorf("task not found: %s in workflow %s", taskID, workflowID)
}

// GetNextTaskToExecute возвращает следующую невыполненную задачу или nil
func (m *WorkflowManager) GetNextTaskToExecute(workflowID string) (*Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	workflow, ok := m.workflows[workflowID]
	if !ok {
		return nil, fmt.Errorf("workflow not found: %s", workflowID)
	}

	for i := 0; i < len(workflow.Tasks); i++ {
		t := workflow.Tasks[i]
		if t.Status == TaskPending || t.Status == TaskNeedsRevision {
			return &t, nil
		}
	}

	return nil, nil // все задачи выполнены
}

// GetAllApprovedTasks возвращает список одобренных задач
func (m *WorkflowManager) GetAllApprovedTasks(workflowID string) []Task {
	m.mu.RLock()
	defer m.mu.RUnlock()

	workflow, ok := m.workflows[workflowID]
	if !ok {
		return nil
	}

	var approved []Task
	for _, t := range workflow.Tasks {
		if t.Status == TaskApproved {
			approved = append(approved, t)
		}
	}
	return approved
}

// CompleteWorkflow помечает workflow как завершённый
func (m *WorkflowManager) CompleteWorkflow(workflowID, summary string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	workflow, ok := m.workflows[workflowID]
	if !ok {
		return fmt.Errorf("workflow not found: %s", workflowID)
	}

	workflow.Status = WorkflowCompleted
	workflow.Summary = summary
	workflow.UpdatedAt = time.Now()
	return nil
}

// ============================================================
// Работа с файлами workflow
// ============================================================

// GetWorkflowDir возвращает директорию workflow
func (m *WorkflowManager) GetWorkflowDir(workflowID string) string {
	return filepath.Join(m.baseDir, workflowID)
}

// SaveWorkflow сохраняет workflow в JSON
func (m *WorkflowManager) SaveWorkflow(workflow *Workflow) error {
	data, err := json.MarshalIndent(workflow, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workflow: %w", err)
	}

	dir := m.GetWorkflowDir(workflow.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create workflow dir: %w", err)
	}

	file := filepath.Join(dir, "workflow.json")
	if err := os.WriteFile(file, data, 0644); err != nil {
		return fmt.Errorf("write workflow file: %w", err)
	}

	return nil
}

// LoadWorkflow загружает workflow из JSON
func (m *WorkflowManager) LoadWorkflow(workflowID string) (*Workflow, error) {
	dir := m.GetWorkflowDir(workflowID)
	file := filepath.Join(dir, "workflow.json")

	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read workflow file: %w", err)
	}

	var workflow Workflow
	if err := json.Unmarshal(data, &workflow); err != nil {
		return nil, fmt.Errorf("unmarshal workflow: %w", err)
	}

	return &workflow, nil
}

// SaveArtifact сохраняет артефакт в директорию workflow
func (m *WorkflowManager) SaveArtifact(workflowID, name, content, taskID string) error {
	dir := m.GetWorkflowDir(workflowID)
	subdir := filepath.Join(dir, "artifacts", taskID)
	if err := os.MkdirAll(subdir, 0755); err != nil {
		return fmt.Errorf("create artifact dir: %w", err)
	}

	filename := filepath.Join(subdir, sanitizeFilename(name))
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		return fmt.Errorf("write artifact: %w", err)
	}

	return nil
}

// ReadArtifact загружает артефакт из директории workflow
func (m *WorkflowManager) ReadArtifact(workflowID, taskID, filename string) (string, error) {
	dir := m.GetWorkflowDir(workflowID)
	path := filepath.Join(dir, "artifacts", taskID, sanitizeFilename(filename))

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read artifact: %w", err)
	}

	return string(content), nil
}

// ListWorkflowArtifacts возвращает список файлов артефактов для задачи
func (m *WorkflowManager) ListWorkflowArtifacts(workflowID, taskID string) ([]string, error) {
	dir := m.GetWorkflowDir(workflowID)
	path := filepath.Join(dir, "artifacts", taskID)

	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("list artifacts: %w", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	return files, nil
}

// ============================================================
// Вспомогательные функции
// ============================================================

func generateID() string {
	return fmt.Sprintf("wf_%d_%d", time.Now().UnixNano(), time.Now().Nanosecond())
}

func sanitizeFilename(name string) string {
	if name == "" {
		return ""
	}
	s := filepath.Base(name)
	for _, r := range s {
		if r < '!' || r > '~' {
			s = "artifact_"
			break
		}
	}
	return s
}
