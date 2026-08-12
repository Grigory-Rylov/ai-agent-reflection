package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/permission"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/util/stringutil"
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
	logger.DebugToFile(e.agent.agentPrefix()+"[executeAllTools] Starting with %d tool calls", len(toolCalls))
	result := FunctionCallResult{
		Success:   true,
		ToolCalls: make([]ToolCallResult, 0),
	}

	for i, tc := range toolCalls {
		logger.DebugToFile(e.agent.agentPrefix()+"[executeAllTools] Executing tool %d/%d: %s", i+1, len(toolCalls), ToolCallName(tc))
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
		argsStr := ToolCallArgumentsStr(toolCall)
		truncationHint := ""
		if strings.Contains(err.Error(), "unexpected end of JSON input") {
			if argsStr == "" {
				truncationHint = " (arguments are empty — stream was truncated)"
			} else {
				truncationHint = " (JSON arguments truncated — stream was cut off, incomplete arguments)"
			}
		}
		errMsg := fmt.Sprintf("Invalid arguments for '%s': %v.%s Expected schema: %v", toolName, err, truncationHint, schema)
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

	// Проверяем доступ к путям: если запрещено — спрашиваем пользователя
	if !e.checkPathAccess(ctx, toolName, args, peerID) {
		errMsg := fmt.Sprintf("Access denied for tool '%s' by user", toolName)
		e.agent.sendThinking(peerID, "[TOOL] Denied: "+toolName)
		return e.agent.createErrorResult(toolCall.ID, toolName, errMsg), fmt.Errorf("%s", errMsg)
	}

	brief := briefToolCall(toolName, args)
	e.agent.debugLog.Debug("%sCall: %s", e.agent.agentPrefix(), brief)
	e.agent.sendThinking(peerID, "[TOOL] Call: "+brief)

	result, err := tool.Execute(ctx, args)
	if err != nil {
		errMsg := fmt.Sprintf("Execution error for %s: %v", toolName, err)
		e.agent.debugLog.Error("%s", errMsg)
		e.agent.sendThinking(peerID, "[TOOL] Error: "+errMsg)
		return e.agent.createErrorResult(toolCall.ID, toolName, errMsg), err
	}

	content := tools.MarshalToolResult(result)
	content = e.truncateToolOutput(peerID, content)
	if result.Success {
		e.agent.debugLog.Debug(e.agent.agentPrefix()+"Result: %s success", toolName)
		e.agent.sendThinking(peerID, "[TOOL] Result: "+toolName+" success")
	} else {
		resultMsg := fmt.Sprintf("[TOOL] Result: %s failed - %s", toolName, stringutil.Truncate(content, 200, "..."))
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

// truncateToolOutput обрезает вывод инструмента в стиле opencode перед
// отправкой в LLM: полный вывод сохраняется в файл, в ответ уходит превью.
func (e *agentToolExecutor) truncateToolOutput(peerID int64, content string) string {
	opts := tools.TruncateOptions{
		Dir:         filepath.Join(tools.WorkingDir, "tool-output"),
		MaxLines:    e.agent.config.ToolOutputMaxLines,
		MaxBytes:    e.agent.config.ToolOutputMaxBytes,
		HasTaskTool: e.agent.toolsRegistry.IsRegistered("task"),
	}

	res, err := tools.TruncateToolResult(content, opts)
	if err != nil {
		e.agent.debugLog.Error("%s tool output truncation failed: %v", e.agent.agentPrefix(), err)
		return content
	}
	if res.Truncated {
		e.agent.debugLog.Debug("%sResult: tool output truncated, full output saved to %s", e.agent.agentPrefix(), res.OutputPath)
		e.agent.sendThinking(peerID, "[TOOL] Result: output truncated, full output saved to "+res.OutputPath)
	}
	return res.Content
}

func (e *agentToolExecutor) checkShellPermission(ctx context.Context, checker permissionChecker, command string, peerID int64) bool {
	scan := permission.ScanCommand(command)
	if len(scan.Patterns) == 0 {
		logger.DebugToFile("[checkPermissionAsk] shell_execute: no patterns (cd-only), allow")
		return true
	}

	needsAsk := false
	for _, pattern := range scan.Patterns {
		action := checker.Evaluate("bash", pattern)
		logger.DebugToFile("[checkPermissionAsk] shell_execute: evaluate bash %q -> %s", pattern, action)
		switch action {
		case "deny":
			logger.DebugToFile("[checkPermissionAsk] shell_execute: denied pattern %q", pattern)
			e.agent.debugLog.Info("Permission denied for bash command '%s'", pattern)
			e.agent.sendThinking(peerID, fmt.Sprintf("[TOOL] Denied: bash %s (permission)", pattern))
			return false
		case "allow":
			continue
		default:
			needsAsk = true
		}
	}

	if !needsAsk {
		logger.DebugToFile("[checkPermissionAsk] shell_execute: all patterns allowed, skip ask")
		return true
	}

	// Команда не трогает файлы вне разрешённых директорий
	// (нет хостовых путей или все они в allowed_dirs) — не спрашиваем.
	if tools.ShellCommandFilesystemSafe(command) {
		logger.DebugToFile("[checkPermissionAsk] shell_execute: filesystem-safe command, skip ask")
		return true
	}

	e.agent.sendThinking(peerID, fmt.Sprintf("[PERMISSION] Asking user for bash command '%s'...", command))
	return askShellPermission(ctx, checker, scan, peerID)
}

func (e *agentToolExecutor) checkPermissionAsk(ctx context.Context, toolName string, args map[string]string, peerID int64) bool {
	logger.DebugToFile("[checkPermissionAsk] enter: tool=%s, peer=%d, args=%v", toolName, peerID, args)

	// Проверяем path grant — если путь разрешён, любой инструмент на нём проходим
	if toolPath := extractToolPath(toolName, args); toolPath != "" {
		if tools.IsPathGranted(peerID, toolPath) {
			logger.DebugToFile("[checkPermissionAsk] path=%s granted for peer %d, allow all tools", toolPath, peerID)
			return true
		}
	}

	checker := e.agent.getPermissionChecker()
	if checker == nil {
		logger.DebugToFile("[checkPermissionAsk] no checker, allow")
		return true
	}

	decision := checker.Check(toolName)
	logger.DebugToFile("[checkPermissionAsk] decision=%s for %s", decision, toolName)
	switch decision {
	case "deny":
		e.agent.debugLog.Info("Permission denied for tool '%s'", toolName)
		e.agent.sendThinking(peerID, fmt.Sprintf("[TOOL] Denied: %s (permission)", toolName))
		return false
	case "ask":
		// Для файловых инструментов: не спрашиваем, если все пути
		// находятся в разрешённых директориях (рабочая папка сессии и др.)
		if paths := tools.FileToolPaths(toolName, args); len(paths) > 0 {
			if tools.PathsAllAllowed(paths) {
				logger.DebugToFile("[checkPermissionAsk] %s: all %d paths in allowed dirs, skip ask", toolName, len(paths))
				return true
			}
		}
		// Для shell_execute: оцениваем каждую подкоманду по паттернам
		// правил (opencode-модель): все allow -> allow, любой deny -> deny,
		// иначе спрашиваем пользователя.
		if toolName == "shell_execute" || toolName == "shell" {
			if cmd, ok := args["command"]; ok {
				return e.checkShellPermission(ctx, checker, cmd, peerID)
			}
		}
		// ask user below
	default:
		return true // "allow" or unknown
	}

	e.agent.sendThinking(peerID, fmt.Sprintf("[PERMISSION] Asking user for tool '%s'...", toolName))

	result := askUserPermission(ctx, peerID, toolName, args)
	logger.DebugToFile("[checkPermissionAsk] askUserPermission returned %v for tool=%s, args=%v", result, toolName, args)
	return result
}

func (e *agentToolExecutor) checkPathAccess(ctx context.Context, toolName string, args map[string]string, peerID int64) bool {
	paths := tools.FileToolPaths(toolName, args)
	if len(paths) == 0 {
		return true
	}

	ctrl := tools.GetAccessController()
	if ctrl == nil {
		return true
	}

	for _, rawPath := range paths {
		resolved, err := resolveToolPath(rawPath)
		if err != nil {
			continue
		}

		if err := tools.CheckPathAllowed(resolved); err != nil {
			e.agent.sendThinking(peerID, fmt.Sprintf("[ACCESS] Need permission to access path: %s", resolved))

			cb, _ := getQuestionState()
			if cb == nil {
				return true
			}

			q := map[string]interface{}{
				"question": fmt.Sprintf("Allow access to path '%s'?", resolved),
				"header":   "Access Permission",
				"options": []map[string]interface{}{
					{"label": "Allow", "description": "Allow access this one time"},
					{"label": "Allow always", "description": "Always allow for this session"},
					{"label": "Deny", "description": "Deny access"},
				},
			}

			answer, err := cb(peerID, q)
			if err != nil {
				return false
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
			case "Allow", "allow", "Allow always", "allow always":
				ctrl.GrantPath(resolved)
				e.agent.sendThinking(peerID, fmt.Sprintf("[ACCESS] Access granted to: %s", resolved))
			default:
				return false
			}
		}
	}

	return true
}

// resolveToolPath приводит путь к абсолютному без проверки доступа.
func resolveToolPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		cleaned = filepath.Join(tools.WorkingDir, cleaned)
	}
	return filepath.Clean(cleaned), nil
}

type permissionChecker interface {
	Check(toolName string) string
	Evaluate(permission, pattern string) string
	Approve(permission, pattern string)
}

func (a *agentImpl) getPermissionChecker() permissionChecker {
	if a.permissionChecker == nil {
		return nil
	}
	return a.permissionChecker
}

func askUserPermission(ctx context.Context, peerID int64, toolName string, args map[string]string) bool {
	cb, _ := getQuestionState()
	if cb == nil {
		logger.DebugToFile("[askUserPermission] cb is nil, allowing tool=%s without asking", toolName)
		return true
	}

	logger.DebugToFile("[askUserPermission] asking user for tool=%s, args=%v, peer=%d", toolName, args, peerID)

	// Собираем подробное описание операции
	detail := buildToolPermissionDetail(toolName, args)

	// Используем русские подписи для VK клавиатуры
	q := map[string]interface{}{
		"question": fmt.Sprintf("Allow: %s?", detail),
		"header":   "🔐 " + toolName,
		"options": []map[string]interface{}{
			{"label": "✅ Allow", "description": "Allow this one time"},
			{"label": "✅ Always allow", "description": "Always allow for this session"},
			{"label": "❌ Deny", "description": "Deny this time"},
		},
	}

	select {
	case <-ctx.Done():
		logger.DebugToFile("[askUserPermission] ctx cancelled for tool=%s", toolName)
		return false
	default:
	}

	logger.DebugToFile("[askUserPermission] calling callback cb(peerID=%d, q) for tool=%s", peerID, toolName)
	answer, err := cb(peerID, q)
	if err != nil {
		logger.DebugToFile("[askUserPermission] cb returned err=%v for tool=%s", err, toolName)
		return false
	}
	logger.DebugToFile("[askUserPermission] cb returned answer=%v for tool=%s", answer, toolName)

	selected, _ := answer["selected"].([]interface{})
	if len(selected) == 0 {
		rawAnswer, _ := answer["answer"].(string)
		selected = []interface{}{rawAnswer}
	}

	if len(selected) == 0 {
		return false
	}

	choice, _ := selected[0].(string)
	switch {
	case strings.Contains(choice, "Always allow"), strings.Contains(choice, "always allow"), strings.Contains(choice, "Всегда"):
		if p := extractToolPath(toolName, args); p != "" {
			tools.GrantPath(peerID, p)
			logger.DebugToFile("[askUserPermission] path grant for %s on peer %d", toolName, peerID)
		}
		return true
	case strings.Contains(choice, "Allow"), strings.Contains(choice, "allow"), strings.Contains(choice, "Разрешить"):
		return true
	default:
		return false
	}
}

func getQuestionState() (func(int64, map[string]interface{}) (map[string]interface{}, error), int64) {
	return tools.GetQuestionState()
}

// askShellPermission спрашивает пользователя о разрешении shell-команды.
// При выборе "Always allow" запоминаются always-префиксы (например "git *")
// в виде правил allow для текущей сессии.
func askShellPermission(ctx context.Context, checker permissionChecker, scan permission.Scan, peerID int64) bool {
	cb, _ := getQuestionState()
	if cb == nil {
		logger.DebugToFile("[askShellPermission] cb is nil, allowing command without asking")
		return true
	}

	detail := strings.Join(scan.Patterns, " && ")
	q := map[string]interface{}{
		"question": fmt.Sprintf("Allow shell command: %s?", stringutil.Truncate(detail, 200, "...")),
		"header":   "🔐 bash",
		"options": []map[string]interface{}{
			{"label": "✅ Allow", "description": "Allow this one time"},
			{"label": "✅ Always allow", "description": "Always allow this command for this session"},
			{"label": "❌ Deny", "description": "Deny this time"},
		},
	}

	select {
	case <-ctx.Done():
		return false
	default:
	}

	answer, err := cb(peerID, q)
	if err != nil {
		return false
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
	switch {
	case strings.Contains(choice, "Always allow"), strings.Contains(choice, "always allow"), strings.Contains(choice, "Всегда"):
		for _, prefix := range scan.Always {
			checker.Approve("bash", prefix)
			logger.DebugToFile("[askShellPermission] approved always rule bash %q", prefix)
		}
		return true
	case strings.Contains(choice, "Allow"), strings.Contains(choice, "allow"), strings.Contains(choice, "Разрешить"):
		return true
	default:
		return false
	}
}

func (a *agentImpl) executeAllTools(ctx context.Context, toolCalls []ToolCall, peerID int64) FunctionCallResult {
	if a.toolExecutor != nil {
		return a.toolExecutor.ExecuteAll(ctx, toolCalls, peerID)
	}
	return newAgentToolExecutor(a).ExecuteAll(ctx, toolCalls, peerID)
}

func extractToolPath(toolName string, args map[string]string) string {
	if p, ok := args["path"]; ok && p != "" {
		return p
	}
	if cmd, ok := args["command"]; ok && cmd != "" {
		// Extract path from common shell commands
		parts := strings.Fields(cmd)
		for i, part := range parts {
			if strings.HasPrefix(part, "/") || strings.HasPrefix(part, "~") || strings.HasPrefix(part, ".") || strings.HasPrefix(part, "$") {
				return part
			}
			if i < len(parts)-1 && (part == "cd" || part == "mkdir" || part == "rm" || part == "cp" || part == "mv") {
				return parts[i+1]
			}
		}
	}
	return ""
}

func buildToolPermissionDetail(toolName string, args map[string]string) string {
	switch toolName {
	case "shell_execute":
		if cmd, ok := args["command"]; ok && cmd != "" {
			return fmt.Sprintf("run shell command: %s", stringutil.Truncate(cmd, 200, "..."))
		}
	case "file_write":
		detail := "write file"
		if path, ok := args["path"]; ok && path != "" {
			detail += " " + path
		}
		return detail
	case "file_read":
		if path, ok := args["path"]; ok && path != "" {
			return "read file " + path
		}
	case "edit":
		if path, ok := args["path"]; ok && path != "" {
			return "edit file " + path
		}
	case "dir_list":
		if path, ok := args["path"]; ok && path != "" {
			return "list directory " + path
		}
	case "glob":
		if pattern, ok := args["pattern"]; ok && pattern != "" {
			return "find files by pattern: " + pattern
		}
	case "search_code":
		if pattern, ok := args["pattern"]; ok && pattern != "" {
			return "search code for: " + stringutil.Truncate(pattern, 100, "...")
		}
	case "web_fetch":
		if url, ok := args["url"]; ok && url != "" {
			return "fetch URL: " + stringutil.Truncate(url, 200, "...")
		}
	case "web_search":
		if query, ok := args["query"]; ok && query != "" {
			return "web search: " + stringutil.Truncate(query, 200, "...")
		}
	}
	return fmt.Sprintf("use tool '%s'", toolName)
}

var toolAliases = map[string]string{
	// opencode PascalCase aliases
	"WebFetch":  "web_fetch",
	"WebSearch": "web_search",
	"Glob":      "glob",
	"Grep":      "search_code",
	"Read":      "file_read",
	"Edit":      "edit",
	"Write":     "file_write",
	"Bash":      "shell_execute",
	"Task":      "task",
	"TodoWrite": "todowrite",
	"TodoRead":  "todoread",
	// legacy aliases
	"grep":        "search_code",
	"grep_search": "search_code",
	"read_file":   "file_read",
	"write_file":  "file_write",
	"list_dir":    "file_list",
	"dir_list":    "file_list",
	"shell":       "shell_execute",
	"bash":        "shell_execute",
	"fetch":       "web_fetch",
	"search":      "web_search",
	"find_files":  "glob",
	"calculate":   "calc",
	"edit_file":   "edit",
	"patch_apply": "apply_patch",
	"subagent":    "task",
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
			return fmt.Sprintf("read_file(%q, offset=%s, limit=%s)", stringutil.Truncate(path, 60, "..."), offset, limit)
		}
		return fmt.Sprintf("read_file(%q)", stringutil.Truncate(path, 60, "..."))
	case "file_write", "write_file":
		if path, ok := args["path"]; ok {
			return fmt.Sprintf("write_file(%q)", stringutil.Truncate(path, 80, "..."))
		}
	case "file_list", "list_dir", "dir_list":
		if path, ok := args["path"]; ok {
			return fmt.Sprintf("list_dir(%q)", stringutil.Truncate(path, 80, "..."))
		}
	case "shell_execute", "shell":
		if cmd, ok := args["command"]; ok {
			return fmt.Sprintf("shell(%q)", stringutil.Truncate(cmd, 60, "..."))
		}
	case "web_fetch", "fetch":
		if url, ok := args["url"]; ok {
			return fmt.Sprintf("web_fetch(%q)", stringutil.Truncate(url, 80, "..."))
		}
	case "web_search", "search":
		if q, ok := args["query"]; ok {
			return fmt.Sprintf("web_search(%q)", stringutil.Truncate(q, 60, "..."))
		}
	case "search_code", "grep", "grep_search":
		if p, ok := args["pattern"]; ok {
			return fmt.Sprintf("search_code(%q)", stringutil.Truncate(p, 60, "..."))
		}
	case "glob", "find_files":
		if p, ok := args["pattern"]; ok {
			return fmt.Sprintf("glob(%q)", stringutil.Truncate(p, 60, "..."))
		}
	case "calc", "calculate":
		if e, ok := args["expression"]; ok {
			return fmt.Sprintf("calc(%q)", stringutil.Truncate(e, 60, "..."))
		}
	case "time_get":
		return "time_get()"
	case "todo":
		op := args["operation"]
		task := args["task"]
		status := args["status"]
		id := args["id"]
		switch op {
		case "add":
			return fmt.Sprintf("todo add(%q, agent=%q)", stringutil.Truncate(task, 60, "..."), args["agent"])
		case "update":
			return fmt.Sprintf("todo update(id=%s, status=%q, agent=%q)", id, status, args["agent"])
		case "list":
			return "todo list()"
		default:
			return fmt.Sprintf("todo(%q)", op)
		}
	case "todowrite", "todoread":
		return toolName
	case "task", "subagent":
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
			return fmt.Sprintf("subagent(%q, prompt=%q)", ag, stringutil.Truncate(t, 60, "..."))
		}
		if ag != "" {
			return fmt.Sprintf("subagent(%q)", ag)
		}
		return "subagent(...)"
	case "edit", "edit_file":
		if path, ok := args["path"]; ok {
			oldStr := args["old_string"]
			if oldStr != "" {
				return fmt.Sprintf("edit(%q, old=%q)", stringutil.Truncate(path, 60, "..."), stringutil.Truncate(oldStr, 40, "..."))
			}
			return fmt.Sprintf("edit(%q)", stringutil.Truncate(path, 80, "..."))
		}
	}
	return toolName
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

// sendThinkingTokens отправляет в thinking чат количество токенов
// (подано/ответ) после ответа LLM.
func (a *agentImpl) sendThinkingTokens(peerID int64, promptTokens, completionTokens int) {
	if a.thinkingCallback == nil || (promptTokens <= 0 && completionTokens <= 0) {
		return
	}
	a.thinkingCallback(peerID, fmt.Sprintf("[TOKENS] in: %d, out: %d", promptTokens, completionTokens))
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
