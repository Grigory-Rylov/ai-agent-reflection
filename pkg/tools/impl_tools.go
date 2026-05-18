package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ============================================================
// File globals & helpers for all tools
// ============================================================

var (
	// WorkingDir — рабочая директория для относительных путей
	WorkingDir string
)

func init() {
	wd, err := os.Getwd()
	if err == nil {
		WorkingDir = wd
	}
}

// SetWorkingDir изменяет рабочую директорию для инструментов
func SetWorkingDir(dir string) {
	if dir != "" {
		WorkingDir = dir
	}
}

// resolvePath приводит путь к абсолютному, защищая от path traversal
func resolvePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is empty")
	}

	cleaned := filepath.Clean(path)

	// Если путь относительный, делаем его абсолютным относительно WorkingDir
	if !filepath.IsAbs(cleaned) {
		cleaned = filepath.Join(WorkingDir, cleaned)
	}

	cleaned = filepath.Clean(cleaned)

	return cleaned, nil
}

// ============================================================
// File Read Tool
// ============================================================

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
		"required": []string{"path"},
	}
}

func (t *FileReadTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	path, ok := inputs["path"]
	if !ok || path == "" {
		return ToolResult{Success: false, Error: "path parameter is required"}, nil
	}

	resolvedPath, err := resolvePath(path)
	if err != nil {
		return ToolResult{Success: false, Error: fmt.Sprintf("Invalid path: %v", err)}, nil
	}

	data, err := os.ReadFile(resolvedPath)
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
			"path":    resolvedPath,
		},
	}, nil
}

// ============================================================
// File Write Tool
// ============================================================

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
		"required": []string{"path", "content"},
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

	resolvedPath, err := resolvePath(path)
	if err != nil {
		return ToolResult{Success: false, Error: fmt.Sprintf("Invalid path: %v", err)}, nil
	}

	dir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to create directory: %v", err),
		}, nil
	}

	tmpFile := resolvedPath + ".tmp"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to write file: %v", err),
		}, nil
	}

	if err := os.Rename(tmpFile, resolvedPath); err != nil {
		os.Remove(tmpFile)
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to rename file: %v", err),
		}, nil
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"path": resolvedPath,
			"size": len(content),
		},
	}, nil
}

// ============================================================
// Shell Execute Tool
// ============================================================

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
		"required": []string{"command"},
	}
}

func (t *ShellExecuteTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	command, ok := inputs["command"]
	if !ok || command == "" {
		return ToolResult{Success: false, Error: "command parameter is required"}, nil
	}

	timeout := 30
	if timeoutStr, ok := inputs["timeout"]; ok {
		if _, err := fmt.Sscanf(timeoutStr, "%d", &timeout); err != nil || timeout <= 0 {
			timeout = 30
		}
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", command)
	output, err := cmd.CombinedOutput()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return ToolResult{
				Success: false,
				Error:   fmt.Sprintf("Failed to execute command: %v", err),
			}, nil
		}
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"output":    string(output),
			"exit_code": exitCode,
			"timeout":   timeout,
		},
	}, nil
}

// ============================================================
// Time Get Tool
// ============================================================

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
		"properties": map[string]interface{}{},
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

	resolvedPath, err := resolvePath(path)
	if err != nil {
		return ToolResult{Success: false, Error: fmt.Sprintf("Invalid path: %v", err)}, nil
	}

	entries, err := os.ReadDir(resolvedPath)
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
			"path":  resolvedPath,
			"items": items,
			"count": len(items),
		},
	}, nil
}

// ============================================================
// Web Fetch Tool
// ============================================================

type WebFetchTool struct{}

func (t *WebFetchTool) Name() string {
	return "web_fetch"
}

func (t *WebFetchTool) Description() string {
	return "Fetch content from a URL. Returns the response body as text."
}

func (t *WebFetchTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url":  CreateStringParameter("url", "The URL to fetch", true),
			"method": CreateEnumParameter("method", "HTTP method", []string{"GET", "POST"}, false),
		},
		"required": []string{"url"},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	urlStr, ok := inputs["url"]
	if !ok || urlStr == "" {
		return ToolResult{Success: false, Error: "url parameter is required"}, nil
	}

	method := "GET"
	if m, ok := inputs["method"]; ok && m != "" {
		method = m
	}

	req, err := NewHTTPRequest(ctx, method, urlStr)
	if err != nil {
		return ToolResult{Success: false, Error: fmt.Sprintf("Failed to create request: %v", err)}, nil
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"url":     urlStr,
			"content": req,
		},
	}, nil
}

// ============================================================
// Web Search Tool
// ============================================================

type WebSearchTool struct{}

func (t *WebSearchTool) Name() string {
	return "web_search"
}

func (t *WebSearchTool) Description() string {
	return "Search the web for information. Returns search results with snippets."
}

func (t *WebSearchTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": CreateStringParameter("query", "The search query", true),
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	query, ok := inputs["query"]
	if !ok || query == "" {
		return ToolResult{Success: false, Error: "query parameter is required"}, nil
	}

	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", urlQueryEscape(query))
	html, err := NewHTTPRequest(ctx, "GET", searchURL)
	if err != nil {
		return ToolResult{Success: false, Error: fmt.Sprintf("Search failed: %v", err)}, nil
	}

	results := parseDuckDuckGoResults(html)
	if len(results) == 0 {
		snippet := html
		if len(snippet) > 500 {
			snippet = snippet[:500] + "..."
		}
		results = append(results, map[string]string{"snippet": snippet})
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"query":   query,
			"results": results,
			"count":   len(results),
		},
	}, nil
}

func urlQueryEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, " ", "+"), "&", "%26")
}

func parseDuckDuckGoResults(html string) []map[string]string {
	var results []map[string]string

	// DuckDuckGo HTML results use class="result" divs
	resultMarkers := strings.Split(html, `<div class="result"`)
	for i := 1; i < len(resultMarkers); i++ {
		block := resultMarkers[i]
		result := map[string]string{}

		result["title"] = extractBetween(block, `result__a"`, `</a>`)
		result["title"] = stripHTMLTags(result["title"])

		result["url"] = extractBetween(block, `href="`, `"`)
		if result["url"] != "" {
			result["url"] = htmlUnescape(result["url"])
		}

		result["snippet"] = extractBetween(block, `class="result__snippet">`, `</`) + extractBetween(block, `class="result__snippet"`, `</`)
		// try different snippet pattern
		if result["snippet"] == "" {
			result["snippet"] = extractBetween(block, `result__snippet">`, `</a>`)
		}
		result["snippet"] = stripHTMLTags(result["snippet"])

		// Skip if completely empty
		if result["title"] == "" && result["snippet"] == "" {
			continue
		}

		results = append(results, result)
		if len(results) >= 8 {
			break
		}
	}

	return results
}

func extractBetween(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	s = s[i+len(start):]
	j := strings.Index(s, end)
	if j < 0 {
		return ""
	}
	return s[:j]
}

func stripHTMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	return strings.TrimSpace(result.String())
}

func htmlUnescape(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&#x2F;", "/")
	s = strings.ReplaceAll(s, "&#39;", "'")
	return s
}

// ============================================================
// Glob Tool — find files by pattern
// ============================================================

type GlobTool struct{}

func (t *GlobTool) Name() string {
	return "glob"
}

func (t *GlobTool) Description() string {
	return "Find files matching a glob pattern. Returns list of matching file paths."
}

func (t *GlobTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": CreateStringParameter("pattern", "The glob pattern to match (e.g. **/*.go)", true),
			"path":    CreateStringParameter("path", "The directory to search in (default: current directory)", false),
		},
		"required": []string{"pattern"},
	}
}

func (t *GlobTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	pattern, ok := inputs["pattern"]
	if !ok || pattern == "" {
		return ToolResult{Success: false, Error: "pattern parameter is required"}, nil
	}

	searchPath := "."
	if p, ok := inputs["path"]; ok && p != "" {
		searchPath = p
	}

	resolvedPath, err := resolvePath(searchPath)
	if err != nil {
		return ToolResult{Success: false, Error: fmt.Sprintf("Invalid path: %v", err)}, nil
	}

	fullPattern := filepath.Join(resolvedPath, pattern)
	matches, err := filepath.Glob(fullPattern)
	if err != nil {
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Glob failed: %v", err),
		}, nil
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"pattern": pattern,
			"path":    resolvedPath,
			"matches": matches,
			"count":   len(matches),
		},
	}, nil
}

// ============================================================
// Grep Tool — search content in files
// ============================================================

type GrepTool struct{}

func (t *GrepTool) Name() string {
	return "search_code"
}

func (t *GrepTool) Description() string {
	return "Search for a pattern in files. Returns matching file paths with line numbers."
}

func (t *GrepTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": CreateStringParameter("pattern", "The regex pattern to search for", true),
			"path":    CreateStringParameter("path", "The directory to search in (default: current directory)", false),
			"include": CreateStringParameter("include", "File pattern to include (e.g. *.go)", false),
		},
		"required": []string{"pattern"},
	}
}

func (t *GrepTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	pattern, ok := inputs["pattern"]
	if !ok || pattern == "" {
		return ToolResult{Success: false, Error: "pattern parameter is required"}, nil
	}

	searchPath := "."
	if p, ok := inputs["path"]; ok && p != "" {
		searchPath = p
	}

	include := ""
	if inc, ok := inputs["include"]; ok {
		include = inc
	}

	resolvedPath, err := resolvePath(searchPath)
	if err != nil {
		return ToolResult{Success: false, Error: fmt.Sprintf("Invalid path: %v", err)}, nil
	}

	var results []map[string]interface{}

	walkFn := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}

		if include != "" {
			match, err := filepath.Match(include, info.Name())
			if err != nil || !match {
				return nil
			}
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if strings.Contains(line, pattern) {
				results = append(results, map[string]interface{}{
					"file":   path,
					"line":   lineNum,
					"content": strings.TrimSpace(line),
				})
			}
		}

		return nil
	}

	filepath.Walk(resolvedPath, walkFn)

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"pattern": pattern,
			"path":    resolvedPath,
			"results": results,
			"count":   len(results),
		},
	}, nil
}

// ============================================================
// Calc Tool — evaluate math expressions
// ============================================================

type CalcTool struct{}

func (t *CalcTool) Name() string {
	return "calc"
}

func (t *CalcTool) Description() string {
	return "Evaluate a mathematical expression and return the result."
}

func (t *CalcTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"expression": CreateStringParameter("expression", "The mathematical expression to evaluate (e.g. 2 + 2 * 3)", true),
		},
		"required": []string{"expression"},
	}
}

func (t *CalcTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	expr, ok := inputs["expression"]
	if !ok || expr == "" {
		return ToolResult{Success: false, Error: "expression parameter is required"}, nil
	}

	result, err := EvaluateExpression(expr)
	if err != nil {
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to evaluate expression: %v", err),
		}, nil
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"expression": expr,
			"result":     result,
		},
	}, nil
}

// ============================================================
// Edit Tool — edit file content with search/replace
// ============================================================

type EditTool struct{}

func (t *EditTool) Name() string {
	return "edit"
}

func (t *EditTool) Description() string {
	return "Edit a file by finding and replacing text. Searches for oldString and replaces it with newString."
}

func (t *EditTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path":      CreateStringParameter("path", "The file path to edit (absolute or relative)", true),
			"old_string": CreateStringParameter("old_string", "The text to search for", true),
			"new_string": CreateStringParameter("new_string", "The replacement text", true),
		},
		"required": []string{"path", "old_string", "new_string"},
	}
}

func (t *EditTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	path, ok := inputs["path"]
	if !ok || path == "" {
		return ToolResult{Success: false, Error: "path parameter is required"}, nil
	}

	oldString, ok := inputs["old_string"]
	if !ok || oldString == "" {
		return ToolResult{Success: false, Error: "old_string parameter is required"}, nil
	}

	newString := inputs["new_string"]

	resolvedPath, err := resolvePath(path)
	if err != nil {
		return ToolResult{Success: false, Error: fmt.Sprintf("Invalid path: %v", err)}, nil
	}

	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to read file: %v", err),
		}, nil
	}

	content := string(data)
	if !strings.Contains(content, oldString) {
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("old_string not found in file: %s", resolvedPath),
		}, nil
	}

	newContent := strings.ReplaceAll(content, oldString, newString)

	tmpFile := resolvedPath + ".tmp"
	if err := os.WriteFile(tmpFile, []byte(newContent), 0644); err != nil {
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to write edited file: %v", err),
		}, nil
	}

	if err := os.Rename(tmpFile, resolvedPath); err != nil {
		os.Remove(tmpFile)
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("Failed to save edited file: %v", err),
		}, nil
	}

	occurrences := strings.Count(content, oldString)

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"path":         resolvedPath,
			"occurrences":  occurrences,
			"old_length":   len(oldString),
			"new_length":   len(newString),
		},
	}, nil
}
