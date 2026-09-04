package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type BackgroundTask struct {
	ID          string
	Name        string
	Command     string
	PeerID      int64
	Owner       string
	ParentOwner string
	Notify      bool
	LogPath     string
	StartedAt   time.Time

	mu       sync.Mutex
	cmd      *exec.Cmd
	status   string
	exitCode int
}

func (t *BackgroundTask) setStatus(status string, exitCode int) {
	t.mu.Lock()
	t.status = status
	t.exitCode = exitCode
	t.mu.Unlock()
}

func (t *BackgroundTask) info() (string, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status, t.exitCode
}

type BackgroundHub struct {
	mu      sync.Mutex
	tasks   map[string]*BackgroundTask
	max     int
	logDir  string
	defaultPeer int64

	notifyMu sync.RWMutex
	notify   func(peerID int64, text string)

	deliveryMu sync.RWMutex
	deliveries map[string]func(peerID int64, text string)
}

func NewBackgroundHub(max int) *BackgroundHub {
	if max <= 0 {
		max = 4
	}
	return &BackgroundHub{
		tasks:       map[string]*BackgroundTask{},
		max:         max,
		logDir:      filepath.Join(os.TempDir(), "ai-agent-background"),
		deliveries:  map[string]func(peerID int64, text string){},
	}
}

func (h *BackgroundHub) SetLogDir(dir string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logDir = dir
}

func (h *BackgroundHub) SetNotifyFunc(f func(peerID int64, text string)) {
	h.notifyMu.Lock()
	h.notify = f
	h.notifyMu.Unlock()
}

func (h *BackgroundHub) SetDefaultPeer(peerID int64) {
	h.mu.Lock()
	h.defaultPeer = peerID
	h.mu.Unlock()
}

func (h *BackgroundHub) SetDelivery(owner string, fn func(peerID int64, text string)) {
	h.deliveryMu.Lock()
	defer h.deliveryMu.Unlock()
	h.deliveries[owner] = fn
}

func (h *BackgroundHub) UnregisterDelivery(owner string) {
	h.deliveryMu.Lock()
	defer h.deliveryMu.Unlock()
	delete(h.deliveries, owner)
}

func (h *BackgroundHub) deliveryFor(owner string) func(peerID int64, text string) {
	h.deliveryMu.RLock()
	defer h.deliveryMu.RUnlock()
	return h.deliveries[owner]
}

func (h *BackgroundHub) defaultPeerLocked() int64 {
	return h.defaultPeer
}

func (h *BackgroundHub) notifyFn() func(int64, string) {
	h.notifyMu.RLock()
	defer h.notifyMu.RUnlock()
	return h.notify
}

func (h *BackgroundHub) Start(command, name string, notify bool, peerID int64) (string, error) {
	return h.StartFor(command, name, notify, peerID, "main", "")
}

func (h *BackgroundHub) StartFor(command, name string, notify bool, peerID int64, owner, parentOwner string) (string, error) {
	h.mu.Lock()
	running := 0
	for _, t := range h.tasks {
		if s, _ := t.info(); s == "running" {
			running++
		}
	}
	if running >= h.max {
		h.mu.Unlock()
		return "", fmt.Errorf("background task limit reached (%d)", h.max)
	}
	logDir := h.logDir
	h.mu.Unlock()

	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", fmt.Errorf("create log dir: %w", err)
	}
	logPath := filepath.Join(logDir, fmt.Sprintf("%s.log", time.Now().Format("20060102-150405")))
	logFile, err := os.Create(logPath)
	if err != nil {
		return "", fmt.Errorf("create log file: %w", err)
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return "", fmt.Errorf("start process: %w", err)
	}

	if peerID == 0 {
		h.mu.Lock()
		peerID = h.defaultPeer
		h.mu.Unlock()
	}
	id := fmt.Sprintf("bg-%d", time.Now().UnixNano())
	task := &BackgroundTask{
		ID:          id,
		Name:        name,
		Command:     command,
		PeerID:      peerID,
		Owner:       owner,
		ParentOwner: parentOwner,
		Notify:      notify,
		LogPath:     logPath,
		StartedAt:   time.Now(),
		cmd:         cmd,
		status:      "running",
	}
	h.mu.Lock()
	h.tasks[id] = task
	h.mu.Unlock()

	go h.watch(task, logFile)
	return id, nil
}

func (h *BackgroundHub) watch(task *BackgroundTask, logFile *os.File) {
	defer logFile.Close()
	err := task.cmd.Wait()
	duration := time.Since(task.StartedAt).Round(time.Second)
	if err == nil {
		task.setStatus("finished", 0)
	} else if ee, ok := err.(*exec.ExitError); ok {
		if ee.ExitCode() == -1 {
			task.setStatus("killed", -1)
		} else {
			task.setStatus("finished", ee.ExitCode())
		}
	} else {
		task.setStatus("failed", -1)
	}
	if !task.Notify {
		return
	}
	label := task.Name
	if label == "" {
		label = task.ID
	}
	_, code := task.info()
	text := fmt.Sprintf("[BG] task %s (id %s) finished (exit %d, %s) — details via shell_check", label, task.ID, code, duration)
	h.deliver(task, text)
}

func (h *BackgroundHub) deliver(task *BackgroundTask, text string) {
	if fn := h.deliveryFor(task.Owner); fn != nil {
		fn(task.PeerID, text)
		return
	}
	if task.ParentOwner != "" {
		if fn := h.deliveryFor(task.ParentOwner); fn != nil {
			fn(task.PeerID, text)
			return
		}
	}
	if fn := h.deliveryFor("main"); fn != nil {
		fn(task.PeerID, text)
		return
	}
	if fn := h.notifyFn(); fn != nil {
		fn(task.PeerID, text)
	}
}

func (h *BackgroundHub) DeliveryFor(owner string) func(peerID int64, text string) {
	return h.deliveryFor(owner)
}

func (h *BackgroundHub) hasDelivery(owner string) bool {
	h.deliveryMu.RLock()
	defer h.deliveryMu.RUnlock()
	_, ok := h.deliveries[owner]
	return ok
}

func (h *BackgroundHub) ReleasePending(owner string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, t := range h.tasks {
		if t.Owner != owner {
			continue
		}
		next := t.ParentOwner
		if next == "" || !h.hasDelivery(next) {
			next = "main"
		}
		t.Owner = next
	}
}

func (h *BackgroundHub) Owner(id string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if task, ok := h.tasks[id]; ok {
		return task.Owner
	}
	return ""
}

func (h *BackgroundHub) Status(id string) string {
	h.mu.Lock()
	task, ok := h.tasks[id]
	h.mu.Unlock()
	if !ok {
		return "unknown"
	}
	s, _ := task.info()
	return s
}

func (h *BackgroundHub) Output(id string, tailLines int) string {
	h.mu.Lock()
	task, ok := h.tasks[id]
	h.mu.Unlock()
	if !ok {
		return "task not found"
	}
	if tailLines <= 0 {
		tailLines = 20
	}
	data, err := os.ReadFile(task.LogPath)
	if err != nil {
		return fmt.Sprintf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	return strings.Join(lines, "\n")
}

func (h *BackgroundHub) LogPath(id string) string {
	h.mu.Lock()
	task, ok := h.tasks[id]
	h.mu.Unlock()
	if !ok {
		return ""
	}
	return task.LogPath
}

func (h *BackgroundHub) Kill(id string) error {
	h.mu.Lock()
	task, ok := h.tasks[id]
	h.mu.Unlock()
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	task.mu.Lock()
	cmd := task.cmd
	task.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("task %s is not running", id)
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return fmt.Errorf("kill process group: %w", err)
	}
	return nil
}

var backgroundHubMu sync.RWMutex
var backgroundHub *BackgroundHub

func SetBackgroundHub(h *BackgroundHub) {
	backgroundHubMu.Lock()
	backgroundHub = h
	backgroundHubMu.Unlock()
}

func GetBackgroundHub() *BackgroundHub {
	backgroundHubMu.RLock()
	defer backgroundHubMu.RUnlock()
	return backgroundHub
}

type ShellBackgroundTool struct{}

func (t *ShellBackgroundTool) Name() string {
	return "shell_background"
}

func (t *ShellBackgroundTool) Description() string {
	return "Start a long-running shell command in the background and return a task_id immediately. You are notified automatically when the task finishes (in your context and the reasoning chat). Check status and output later with shell_check."
}

func (t *ShellBackgroundTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": CreateStringParameter("command", "The shell command to run in the background", true),
			"name":    CreateStringParameter("name", "Short human-readable task name (optional)", false),
			"notify":  CreateStringParameter("notify", "Notify the model when the task finishes (default: true)", false),
		},
		"required": []string{"command"},
	}
}

const (
	BGOwnerContextKey       = "ai-agent.bg-owner"
	BGParentOwnerContextKey = "ai-agent.bg-parent-owner"
)

func BGOwnerFromContext(ctx context.Context) string {
	if ctx == nil {
		return "main"
	}
	if owner, ok := ctx.Value(BGOwnerContextKey).(string); ok && owner != "" {
		return owner
	}
	return "main"
}

func BGParentOwnerFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if owner, ok := ctx.Value(BGParentOwnerContextKey).(string); ok {
		return owner
	}
	return ""
}

func (t *ShellBackgroundTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	command := inputs["command"]
	if command == "" {
		return ToolResult{Success: false, Error: "command parameter is required"}, nil
	}
	notify := true
	if v := inputs["notify"]; v == "false" {
		notify = false
	}
	h := GetBackgroundHub()
	if h == nil {
		return ToolResult{Success: false, Error: "background hub is not initialized"}, nil
	}
	id, err := h.StartFor(command, inputs["name"], notify, 0, BGOwnerFromContext(ctx), BGParentOwnerFromContext(ctx))
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}, nil
	}
	return ToolResult{Success: true, Data: map[string]interface{}{
		"task_id": id,
		"log":     h.LogPath(id),
	}}, nil
}

type ShellCheckTool struct{}

func (t *ShellCheckTool) Name() string {
	return "shell_check"
}

func (t *ShellCheckTool) Description() string {
	return "Check the status of a background shell task started via shell_background. Returns status, exit code, and the tail of the task output."
}

func (t *ShellCheckTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_id": CreateStringParameter("task_id", "The task_id returned by shell_background", true),
			"tail":    CreateStringParameter("tail", "Number of output lines to return (default: 20)", false),
		},
		"required": []string{"task_id"},
	}
}

func (t *ShellCheckTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	id := inputs["task_id"]
	if id == "" {
		return ToolResult{Success: false, Error: "task_id parameter is required"}, nil
	}
	tail := 20
	if v := inputs["tail"]; v != "" {
		fmt.Sscanf(v, "%d", &tail)
	}
	h := GetBackgroundHub()
	if h == nil {
		return ToolResult{Success: false, Error: "background hub is not initialized"}, nil
	}
	status := h.Status(id)
	if status == "unknown" {
		return ToolResult{Success: false, Error: fmt.Sprintf("task %s not found", id)}, nil
	}
	return ToolResult{Success: true, Data: map[string]interface{}{
		"task_id": id,
		"status":  status,
		"output":  h.Output(id, tail),
		"log":     h.LogPath(id),
	}}, nil
}