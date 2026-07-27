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

	toolName = resolveToolAlias(toolName)

	tool, ok := e.agent.toolsRegistry.Get(toolName)
	if !ok {
		availableTools := e.agent.getAvailableToolsList()
		errMsg := fmt.Sprintf("Tool '%s' not found. Available tools: %s", toolName, availableTools)
		e.agent.debugLog.Error("%s", errMsg)
		e.agent.sendThinking(peerID, "[TOOL] Error: "+errMsg)
		return e.agent.createErrorResult(toolCall.ID, toolName, errMsg), fmt.Errorf("%s", errMsg)
	}

	args, err := parseToolArguments(toolCall)
	if err != nil {
		schema := tool.Schema()
		errMsg := fmt.Sprintf("Invalid arguments for '%s': %v. Expected schema: %v", toolName, err, schema)
		e.agent.debugLog.Error("%s", errMsg)
		e.agent.sendThinking(peerID, "[TOOL] Error: "+errMsg)
		return e.agent.createErrorResult(toolCall.ID, toolName, errMsg), err
	}

	// Проверяем permission: если "ask" — спрашиваем пользователя
	if !e.checkPermissionAsk(ctx, toolName, args, peerID) {
		errMsg := fmt.Sprintf("Permission denied for tool '%s' by user", toolName)
		e.agent.sendThinking(peerID, "[TOOL] Denied: "+toolName)
		return e.agent.createErrorResult(toolCall.ID, toolName, errMsg), fmt.Errorf("%s", errMsg)
	}

	brief := briefToolCall(toolName, args)
	e.agent.debugLog.Debug("Call: %s", brief)
	e.agent.sendThinking(peerID, "[TOOL] Call: "+brief)

	result, err := tool.Execute(ctx, args)
	if err != nil {
		errMsg := fmt.Sprintf("Execution error for %s: %v", toolName, err)
		e.agent.debugLog.Error("%s", errMsg)
		e.agent.sendThinking(peerID, "[TOOL] Error: "+errMsg)
		return e.agent.createErrorResult(toolCall.ID, toolName, errMsg), err
	}

	content := tools.MarshalToolResult(result)
	if result.Success {
		e.agent.debugLog.Debug("Result: %s success", toolName)
		e.agent.sendThinking(peerID, "[TOOL] Result: "+toolName+" success")
	} else {
		resultMsg := fmt.Sprintf("[TOOL] Result: %s failed - %s", toolName, truncateStr(content, 200))
		e.agent.debugLog.Info("%s", resultMsg)
		e.agent.sendThinking(peerID, resultMsg)
	}

	return ToolCallResult{
		ToolCallID: toolCall.ID,
		ToolName:   toolName,
		Content:    content,
		IsError:    !result.Success,
	}, nil
}

func (e *agentToolExecutor) checkPermissionAsk(ctx context.Context, toolName string, args map[string]string, peerID int64) bool {
	agentConfig := e.agent.config
	if agentConfig.AgentName == "" {
		return true
	}

	checker := e.agent.getPermissionChecker()
	if checker == nil {
		return true
	}

	decision := checker.Check(toolName)
	if decision != "ask" {
		return true
	}

	e.agent.sendThinking(peerID, fmt.Sprintf("[PERMISSION] Asking user for tool '%s'...", toolName))

	resource := ""
	if path, ok := args["path"]; ok {
		resource = path
	}

	return askUserPermission(ctx, peerID, toolName, resource)
}

func (e *agentToolExecutor) getPermissionChecker() permissionChecker {
	if e.agent == nil {
		return nil
	}
	return e.agent.permissionChecker
}

type permissionChecker interface {
	Check(toolName string) string
}

func (a *agentImpl) getPermissionChecker() permissionChecker {
	if a.permissionChecker == nil {
		return nil
	}
	return a.permissionChecker
}

func askUserPermission(ctx context.Context, peerID int64, toolName, resource string) bool {
	cb, _ := getQuestionState()
	if cb == nil {
		return true
	}

	q := map[string]interface{}{
		"question": fmt.Sprintf("Allow tool '%s'?", toolName),
		"header":   "Permission",
		"options": []map[string]interface{}{
			{"label": "Allow", "description": "Allow this one time"},
			{"label": "Deny", "description": "Deny this time"},
			{"label": "Always allow", "description": "Always allow for this session"},
		},
	}

	if resource != "" {
		q["question"] = fmt.Sprintf("Allow tool '%s' on '%s'?", toolName, resource)
	}

	select {
	case <-ctx.Done():
		return false
	default:
	}

	answer, err := cb(peerID, q)
	if err != nil {
		return true
	}

	selected, _ := answer["selected"].([]interface{})
	if len(selected) == 0 {
		rawAnswer, _ := answer["answer"].(string)
		selected = []interface{}{rawAnswer}
	}

	if len(selected) == 0 {
		return false
	}

	choice, _ := selected[0].(string)
	switch choice {
	case "Allow", "allow":
		return true
	case "Always allow", "always allow":
		return true
	default:
		return false
	}
}

func getQuestionState() (func(int64, map[string]interface{}) (map[string]interface{}, error), int64) {
	return tools.GetQuestionState()
}

func (a *agentImpl) executeAllTools(ctx context.Context, toolCalls []ToolCall, peerID int64) FunctionCallResult {
	if a.toolExecutor != nil {
		return a.toolExecutor.ExecuteAll(ctx, toolCalls, peerID)
	}
	return newAgentToolExecutor(a).ExecuteAll(ctx, toolCalls, peerID)
}

var toolAliases = map[string]string{
	// opencode PascalCase aliases
	"WebFetch":     "web_fetch",
	"WebSearch":    "web_search",
	"Glob":         "glob",
	"Grep":         "search_code",
	"Read":         "file_read",
	"Edit":         "edit",
	"Write":        "file_write",
	"Bash":         "shell_execute",
	"Task":         "task",
	"TodoWrite":    "todowrite",
	"TodoRead":     "todoread",
	// legacy aliases
	"grep":         "search_code",
	"grep_search":  "search_code",
	"read_file":    "file_read",
	"write_file":   "file_write",
	"list_dir":     "file_list",
	"dir_list":     "file_list",
	"shell":        "shell_execute",
	"bash":         "shell_execute",
	"fetch":        "web_fetch",
	"search":       "web_search",
	"find_files":   "glob",
	"calculate":    "calc",
	"edit_file":    "edit",
	"patch_apply":  "apply_patch",
	"subagent":     "task",
}

func resolveToolAlias(name string) string {
	if resolved, ok := toolAliases[name]; ok {
		return resolved
	}
	return name
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
	case "subagent":
		ag := args["subagent_type"]
		if ag == "" {
			ag = args["name"]
		}
		t := args["prompt"]
		if t == "" {
			t = args["task"]
		}
		if t == "" {
			t = args["description"]
		}
		if ag != "" && t != "" {
			return fmt.Sprintf("subagent(%q, prompt=%q)", ag, truncateStr(t, 60))
		}
		if ag != "" {
			return fmt.Sprintf("subagent(%q)", ag)
		}
		return "subagent(...)"
	case "edit", "edit_file":
		if path, ok := args["path"]; ok {
			oldStr := args["old_string"]
			if oldStr != "" {
				return fmt.Sprintf("edit(%q, old=%q)", truncateStr(path, 60), truncateStr(oldStr, 40))
			}
			return fmt.Sprintf("edit(%q)", truncateStr(path, 80))
		}
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
