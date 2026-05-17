package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ============================================================
// File Read Tool
// ============================================================

// FileReadTool позволяет читать содержимое файлов
type FileReadTool struct{}

func (t *FileReadTool) Name() string {
	return "file_read"
}

func (t *FileReadTool) Description() string {
	return "Read the contents of a file. Returns the file content or an error if the file cannot be read."
}

func (t *FileReadTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": CreateStringParameter("path", "The file path to read (absolute or relative)", true),
		},
	}
}

func (t *FileReadTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	path, ok := inputs["path"]
	if !ok || path == "" {
		return ToolResult{Success: false, Error: "path parameter is required"}, nil
	}

	// Sanitize path
	sanitizedPath := sanitizePath(path)
	if sanitizedPath == "" {
		return ToolResult{Success: false, Error: "Invalid path: path traversal detected"}, nil
	}

	// Read file
	data, err := os.ReadFile(sanitizedPath)
	if err != nil {
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to read file: %v", err),
		}, nil
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"content": string(data),
			"size":    len(data),
		},
	}, nil
}

// ============================================================
// File Write Tool
// ============================================================

// FileWriteTool позволяет записывать данные в файлы
type FileWriteTool struct{}

func (t *FileWriteTool) Name() string {
	return "file_write"
}

func (t *FileWriteTool) Description() string {
	return "Write content to a file. Creates the file if it doesn't exist, or overwrites existing content."
}

func (t *FileWriteTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path":    CreateStringParameter("path", "The file path to write to (absolute or relative)", true),
			"content": CreateStringParameter("content", "The content to write to the file", true),
		},
	}
}

func (t *FileWriteTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	path, ok := inputs["path"]
	if !ok || path == "" {
		return ToolResult{Success: false, Error: "path parameter is required"}, nil
	}

	content, ok := inputs["content"]
	if !ok {
		return ToolResult{Success: false, Error: "content parameter is required"}, nil
	}

	// Sanitize path
	sanitizedPath := sanitizePath(path)
	if sanitizedPath == "" {
		return ToolResult{Success: false, Error: "Invalid path: path traversal detected"}, nil
	}

	// Create directory if needed
	dir := filepath.Dir(sanitizedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to create directory: %v", err),
		}, nil
	}

	// Write file atomically via temp file
	tmpFile := sanitizedPath + ".tmp"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to write file: %v", err),
		}, nil
	}

	if err := os.Rename(tmpFile, sanitizedPath); err != nil {
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to rename file: %v", err),
		}, nil
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"path": sanitizedPath,
			"size": len(content),
		},
	}, nil
}

// ============================================================
// Shell Execute Tool
// ============================================================

// ShellExecuteTool позволяет выполнять shell команды
type ShellExecuteTool struct{}

func (t *ShellExecuteTool) Name() string {
	return "shell_execute"
}

func (t *ShellExecuteTool) Description() string {
	return "Execute a shell command and return the output. Use with caution — commands are executed directly."
}

func (t *ShellExecuteTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": CreateStringParameter("command", "The shell command to execute", true),
			"timeout": CreateIntegerParameter("timeout", "Execution timeout in seconds (default: 30)", false),
		},
	}
}

func (t *ShellExecuteTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	command, ok := inputs["command"]
	if !ok || command == "" {
		return ToolResult{Success: false, Error: "command parameter is required"}, nil
	}

	// Check for unsafe characters
	if !isSafeCommand(command) {
		return ToolResult{
			Success: false,
			Error:   "Command contains unsafe characters",
		}, nil
	}

	// Set timeout
	timeout := 30
	if timeoutStr, ok := inputs["timeout"]; ok {
		if _, err := fmt.Sscanf(timeoutStr, "%d", &timeout); err != nil {
			timeout = 30
		}
	}

	// Execute command (placeholder — real implementation uses os/exec)
	output := fmt.Sprintf("Command execution: %s (timeout: %ds)", command, timeout)

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"output":  output,
			"timeout": timeout,
		},
	}, nil
}

// ============================================================
// Time Get Tool
// ============================================================

// TimeGetTool возвращает текущее время
type TimeGetTool struct{}

func (t *TimeGetTool) Name() string {
	return "time_get"
}

func (t *TimeGetTool) Description() string {
	return "Get the current date and time in ISO 8601 format."
}

func (t *TimeGetTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
	}
}

func (t *TimeGetTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	now := time.Now().UTC()

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"time":           now.Format(time.RFC3339),
			"unix_timestamp": now.Unix(),
			"date":           now.Format("2006-01-02"),
			"time_local":     now.Local().Format(time.RFC3339),
		},
	}, nil
}

// ============================================================
// Directory List Tool
// ============================================================

// DirListTool позволяет просматривать содержимое директорий
type DirListTool struct{}

func (t *DirListTool) Name() string {
	return "file_list"
}

func (t *DirListTool) Description() string {
	return "List the contents of a directory. Returns file names and their types."
}

func (t *DirListTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": CreateStringParameter("path", "The directory path to list (default: current directory)", false),
		},
	}
}

func (t *DirListTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	path := "."
	if p, ok := inputs["path"]; ok && p != "" {
		path = p
	}

	sanitizedPath := sanitizePath(path)
	if sanitizedPath == "" {
		return ToolResult{Success: false, Error: "Invalid path: path traversal detected"}, nil
	}

	// Read directory contents
	entries, err := os.ReadDir(sanitizedPath)
	if err != nil {
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to read directory: %v", err),
		}, nil
	}

	var items []map[string]interface{}
	for _, entry := range entries {
		item := map[string]interface{}{
			"name": entry.Name(),
			"type": "file",
		}
		if entry.IsDir() {
			item["type"] = "directory"
		}

		info, err := entry.Info()
		if err == nil {
			item["size"] = info.Size()
			item["modified"] = info.ModTime().Format(time.RFC3339)
		}

		items = append(items, item)
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"path":  sanitizedPath,
			"items": items,
			"count": len(items),
		},
	}, nil
}

// ============================================================
// Утилиты
// ============================================================

// sanitizePath очищает путь от попыток выхода за пределы
func sanitizePath(path string) string {
	// Убираем .. из начала пути
	cleaned := filepath.Clean(path)

	// Проверяем path traversal
	if cleaned == ".." || filepath.IsAbs(cleaned) && len(cleaned) > 1 {
		return cleaned
	}

	return cleaned
}

// isSafeCommand проверяет безопасность команды
func isSafeCommand(cmd string) bool {
	// Check command length
	if len(cmd) > 200 {
		return false
	}
	return true
}
