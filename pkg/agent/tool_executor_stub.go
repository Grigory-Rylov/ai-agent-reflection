package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
)

// StubToolExecutor — заглушка для ToolExecutor, пишет вызовы в лог-файл
// вместо реального выполнения инструментов. Потокобезопасен.
// Всегда возвращает успешный результат с {"stub":true,"tool":"<name>"}.
type StubToolExecutor struct {
	LogPath string
	mu      sync.Mutex
}

// NewStubToolExecutor создаёт StubToolExecutor и очищает лог-файл.
func NewStubToolExecutor(logPath string) *StubToolExecutor {
	os.Remove(logPath)
	return &StubToolExecutor{LogPath: logPath}
}

// ExecuteAll логирует каждый tool call и возвращает заглушечный успешный результат.
func (e *StubToolExecutor) ExecuteAll(ctx context.Context, toolCalls []ToolCall, peerID int64) FunctionCallResult {
	results := make([]ToolCallResult, len(toolCalls))
	for i, tc := range toolCalls {
		name := ToolCallName(tc)
		args := ToolCallArgumentsStr(tc)

		e.writeLog("[TOOL] Call: %s(%s)", name, args)
		e.writeLog("[TOOL] Result: %s success (stub)", name)

		results[i] = ToolCallResult{
			ToolCallID: tc.ID,
			ToolName:   name,
			Content:    fmt.Sprintf(`{"stub":true,"tool":"%s"}`, name),
			IsError:    false,
		}
	}
	return FunctionCallResult{
		Success:   true,
		ToolCalls: results,
	}
}

func (e *StubToolExecutor) writeLog(format string, args ...interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()

	line := fmt.Sprintf(format, args...)
	f, err := os.OpenFile(e.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// ReadLog возвращает все строки из лог-файла.
func (e *StubToolExecutor) ReadLog() []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	data, err := os.ReadFile(e.LogPath)
	if err != nil {
		return nil
	}
	content := strings.TrimRight(string(data), "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

// Contains проверяет, содержит ли лог-файл строку с указанным текстом.
func (e *StubToolExecutor) Contains(substr string) bool {
	for _, line := range e.ReadLog() {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// Count возвращает количество строк в логе, содержащих указанный текст.
func (e *StubToolExecutor) Count(substr string) int {
	count := 0
	for _, line := range e.ReadLog() {
		if strings.Contains(line, substr) {
			count++
		}
	}
	return count
}
