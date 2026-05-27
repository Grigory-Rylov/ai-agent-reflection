package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/pkg/tools"
)

// MaxToolResultSize — максимальный размер результата инструмента в символах
const MaxToolResultSize = 50000

// ToolExecutor определяет интерфейс для выполнения инструментов
type ToolExecutor interface {
	ExecuteAll(ctx context.Context, toolCalls []ToolCall, peerID int64) FunctionCallResult
}

// agentToolExecutor реализует ToolExecutor через реестр инструментов агента
type agentToolExecutor struct {
	agent *agentImpl
}

func newAgentToolExecutor(a *agentImpl) *agentToolExecutor {
	return &agentToolExecutor{agent: a}
}

func (e *agentToolExecutor) ExecuteAll(ctx context.Context, toolCalls []ToolCall, peerID int64) FunctionCallResult {
	logger.DebugToFile("[executeAllTools] Starting with %d tool calls", len(toolCalls))
	result := FunctionCallResult{
		Success:   true,
		ToolCalls: make([]ToolCallResult, 0),
	}

	for i, tc := range toolCalls {
		logger.DebugToFile("[executeAllTools] Executing tool %d/%d: %s", i+1, len(toolCalls), ToolCallName(tc))
		toolResult, execErr := e.executeTool(ctx, tc, peerID)
		if execErr != nil {
			result.ToolCalls = append(result.ToolCalls, ToolCallResult{
				ToolCallID: tc.ID,
				ToolName:   ToolCallName(tc),
				Content:    fmt.Sprintf("Error: %v", execErr),
				IsError:    true,
			})
			continue
		}
		result.ToolCalls = append(result.ToolCalls, toolResult)
	}

	return result
}

func (e *agentToolExecutor) executeTool(ctx context.Context, toolCall ToolCall, peerID int64) (ToolCallResult, error) {
	toolName := ToolCallName(toolCall)

	tool, ok := e.agent.toolsRegistry.Get(toolName)
	if !ok {
		availableTools := e.agent.getAvailableToolsList()
		errMsg := fmt.Sprintf("Tool '%s' not found. Available tools: %s", toolName, availableTools)
		fmt.Printf("[TOOL] Error: %s\n", errMsg)
		e.agent.sendThinking(peerID, "[TOOL] Error: "+errMsg)
		return e.agent.createErrorResult(toolCall.ID, toolName, errMsg), fmt.Errorf("%s", errMsg)
	}

	args, err := parseToolArguments(toolCall)
	if err != nil {
		schema := tool.Schema()
		errMsg := fmt.Sprintf("Invalid arguments for '%s': %v. Expected schema: %v", toolName, err, schema)
		fmt.Printf("[TOOL] Error: %s\n", errMsg)
		e.agent.sendThinking(peerID, "[TOOL] Error: "+errMsg)
		return e.agent.createErrorResult(toolCall.ID, toolName, errMsg), err
	}

	brief := briefToolCall(toolName, args)
	fmt.Printf("[TOOL] Call: %s\n", brief)
	e.agent.sendThinking(peerID, "[TOOL] Call: "+brief)

	// Синхронизируем рабочую директорию сессии перед вызовом инструмента
	sess := e.agent.getSession(peerID)
	if wd := sess.GetWorkingDir(); wd != "" {
		tools.SetWorkingDir(wd)
	}

	result, err := tool.Execute(ctx, args)
	if err != nil {
		errMsg := fmt.Sprintf("Execution error for %s: %v", toolName, err)
		fmt.Printf("[TOOL] Error: %s\n", errMsg)
		e.agent.sendThinking(peerID, "[TOOL] Error: "+errMsg)
		return e.agent.createErrorResult(toolCall.ID, toolName, errMsg), err
	}

	content := tools.MarshalToolResult(result)
	if result.Success {
		fmt.Printf("[TOOL] Result: %s success\n", toolName)
		e.agent.sendThinking(peerID, "[TOOL] Result: "+toolName+" success")
	} else {
		resultMsg := fmt.Sprintf("[TOOL] Result: %s failed - %s", toolName, truncateStr(content, 200))
		fmt.Println(resultMsg)
		e.agent.sendThinking(peerID, resultMsg)
	}

	return ToolCallResult{
		ToolCallID: toolCall.ID,
		ToolName:   toolName,
		Content:    content,
		IsError:    !result.Success,
	}, nil
}

func (a *agentImpl) executeAllTools(ctx context.Context, toolCalls []ToolCall, peerID int64) FunctionCallResult {
	if a.toolExecutor != nil {
		return a.toolExecutor.ExecuteAll(ctx, toolCalls, peerID)
	}
	return newAgentToolExecutor(a).ExecuteAll(ctx, toolCalls, peerID)
}

func briefToolCall(toolName string, args map[string]string) string {
	switch toolName {
	case "file_read", "read_file":
		path := args["path"]
		offset := args["offset"]
		limit := args["limit"]
		if offset != "" || limit != "" {
			return fmt.Sprintf("read_file(%q, offset=%s, limit=%s)", truncateStr(path, 60), offset, limit)
		}
		return fmt.Sprintf("read_file(%q)", truncateStr(path, 60))
	case "file_write", "write_file":
		if path, ok := args["path"]; ok {
			return fmt.Sprintf("write_file(%q)", truncateStr(path, 80))
		}
	case "file_list", "list_dir", "dir_list":
		if path, ok := args["path"]; ok {
			return fmt.Sprintf("list_dir(%q)", truncateStr(path, 80))
		}
	case "edit", "edit_file":
		if path, ok := args["path"]; ok {
			return fmt.Sprintf("edit(%q)", truncateStr(path, 80))
		}
	case "shell_execute", "shell":
		if cmd, ok := args["command"]; ok {
			return fmt.Sprintf("shell(%q)", truncateStr(cmd, 60))
		}
	case "web_fetch", "fetch":
		if url, ok := args["url"]; ok {
			return fmt.Sprintf("web_fetch(%q)", truncateStr(url, 80))
		}
	case "web_search", "search":
		if q, ok := args["query"]; ok {
			return fmt.Sprintf("web_search(%q)", truncateStr(q, 60))
		}
	case "search_code", "grep", "grep_search":
		if p, ok := args["pattern"]; ok {
			return fmt.Sprintf("search_code(%q)", truncateStr(p, 60))
		}
	case "glob", "find_files":
		if p, ok := args["pattern"]; ok {
			return fmt.Sprintf("glob(%q)", truncateStr(p, 60))
		}
	case "calc", "calculate":
		if e, ok := args["expression"]; ok {
			return fmt.Sprintf("calc(%q)", truncateStr(e, 60))
		}
	case "time_get":
		return "time_get()"
	}
	return toolName
}

func truncateStr(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

func (a *agentImpl) agentPrefix() string {
	if a.config.AgentName == "" {
		return ""
	}
	return "[" + a.config.AgentName + "] "
}

func (a *agentImpl) sendThinking(peerID int64, content string) {
	if a.thinkingCallback != nil {
		a.thinkingCallback(peerID, content)
	}
}

func (a *agentImpl) getAvailableToolsList() string {
	tools := a.toolsRegistry.GetAll()
	if len(tools) == 0 {
		return "no tools registered"
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name())
	}
	return strings.Join(names, ", ")
}

func (a *agentImpl) createErrorResult(toolCallID, toolName, errorMsg string) ToolCallResult {
	return ToolCallResult{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Content:    errorMsg,
		IsError:    true,
	}
}
