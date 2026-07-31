package tools

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opencode/llama-client/pkg/access"
)

// Global access controller, shared across all tool instances.
// Set at startup from main.go, checked by resolvePath.
var globalAccessController *access.Controller

// SetAccessController устанавливает глобальный контроллер доступа.
func SetAccessController(ctrl *access.Controller) {
	globalAccessController = ctrl
}

// GetAccessController возвращает текущий контроллер доступа.
func GetAccessController() *access.Controller {
	return globalAccessController
}

// CheckPathAllowed проверяет, разрешён ли доступ к указанному пути.
// Возвращает nil если доступ разрешён, или ошибку с описанием причины отказа.
func CheckPathAllowed(resolvedPath string) error {
	if globalAccessController == nil {
		return nil
	}
	result := globalAccessController.CheckAccess(resolvedPath)
	if !result.Allowed {
		allowedDirs := globalAccessController.AllowedDirs()
		return fmt.Errorf(
			"access denied: you do not have permission to access %q. "+
				"Allowed directories: %v. "+
				"You can only work with files inside these directories. "+
				"If you need access to a different directory, ask the user to grant it.",
			resolvedPath, formatDirs(allowedDirs),
		)
	}
	return nil
}

// formatDirs форматирует список директорий для сообщения об ошибке.
func formatDirs(dirs []string) string {
	if len(dirs) == 0 {
		return "none"
	}
	quoted := make([]string, len(dirs))
	for i, d := range dirs {
		quoted[i] = fmt.Sprintf("%q", d)
	}
	return strings.Join(quoted, ", ")
}

// FileToolKind определяет тип файлового инструмента
type FileToolKind int

const (
	ToolRead  FileToolKind = iota // только чтение (file_read, file_list)
	ToolWrite                     // запись (file_write, edit)
)

// fileToolPaths возвращает список имён параметров, содержащих пути,
// для указанного инструмента.
func FileToolPaths(toolName string, args map[string]string) []string {
	switch toolName {
	case "file_read", "read_file":
		if p, ok := args["path"]; ok {
			return []string{p}
		}
	case "file_write", "write_file":
		if p, ok := args["path"]; ok {
			return []string{p}
		}
	case "edit", "edit_file":
		if p, ok := args["path"]; ok {
			return []string{p}
		}
	case "file_list", "list_dir", "dir_list":
		if p, ok := args["path"]; ok && p != "" {
			return []string{p}
		}
		return []string{"."}
	case "glob", "find_files":
		var paths []string
		if p, ok := args["path"]; ok && p != "" {
			paths = append(paths, p)
		}
		return paths
	case "search_code", "grep", "grep_search":
		if p, ok := args["path"]; ok && p != "" {
			return []string{p}
		}
		return []string{"."}
	case "shell_execute", "shell":
		return nil // командная строка — проверяем отдельно
	}
	return nil
}

// fileCommands содержит команды, оперирующие файлами/директориями.
// Для этих команд ExtractShellPaths извлекает пути из аргументов.
var fileCommands = map[string]bool{
	"cat": true, "less": true, "more": true, "head": true, "tail": true,
	"ls": true, "find": true,
	"cp": true, "mv": true, "rm": true,
	"mkdir": true, "touch": true, "chmod": true, "chown": true,
	"grep": true, "sed": true, "awk": true,
	"diff": true, "patch": true,
	"git": true,
}

// isAbsolutePath возвращает true, если строка выглядит как абсолютный Unix-путь.
func isAbsolutePath(s string) bool {
	return len(s) > 0 && s[0] == '/'
}

// looksLikePath проверяет, похож ли токен на путь к файлу.
// Исключает флаги, IP-адреса, URL, хосты, цитируемые строки и опции.
func looksLikePath(token string) bool {
	if strings.HasPrefix(token, "-") {
		return false
	}
	if len(token) > 0 && (token[0] == '\'' || token[0] == '"' || token[0] == '`') {
		return false
	}
	if isAbsolutePath(token) {
		return true
	}
	if strings.HasPrefix(token, "http://") || strings.HasPrefix(token, "https://") {
		return false
	}
	if strings.HasPrefix(token, "~") {
		return true
	}
	if strings.Contains(token, ".") && !strings.Contains(token, "@") {
		parts := strings.Split(token, ".")
		allNumeric := true
		for _, p := range parts {
			isNum := true
			for _, c := range p {
				if c < '0' || c > '9' {
					isNum = false
					break
				}
			}
			if isNum {
				continue
			}
			allNumeric = false
		}
		if allNumeric {
			return false
		}
		return true
	}
	if strings.Contains(token, "@") {
		return false
	}
	return false
}

// ExtractShellPaths извлекает пути к файлам из shell-команды.
// Возвращает пустой слайс, если команда не оперирует файлами.
func ExtractShellPaths(command string) []string {
	if command == "" {
		return nil
	}

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return nil
	}

	cmd := parts[0]
	if slashIdx := strings.LastIndex(cmd, "/"); slashIdx >= 0 {
		cmd = cmd[slashIdx+1:]
	}
	if !fileCommands[cmd] {
		return nil
	}

	var paths []string
	for _, token := range parts[1:] {
		if looksLikePath(token) {
			paths = append(paths, token)
		}
	}
	return paths
}

// PathsAllAllowed проверяет, что все указанные пути находятся
// в разрешённых директориях access controller'а.
// Возвращает true если контроллера нет, путей нет, или все пути разрешены.
func PathsAllAllowed(paths []string) bool {
	ctrl := GetAccessController()
	if ctrl == nil {
		return true
	}
	for _, p := range paths {
		resolved, err := resolvePath(p)
		if err != nil {
			return false
		}
		if err := CheckPathAllowed(resolved); err != nil {
			return false
		}
	}
	return true
}

// ShellPathsAllAllowed проверяет, что все указанные пути находятся
// в разрешённых директориях access controller'а.
// Возвращает true если контроллера нет, путей нет, или все пути разрешены.
func ShellPathsAllAllowed(paths []string) bool {
	return PathsAllAllowed(paths)
}

// CheckToolArgs проверяет все пути в аргументах инструмента на доступ.
func CheckToolArgs(toolName string, args map[string]string) error {
	paths := FileToolPaths(toolName, args)
	for _, p := range paths {
		resolved, err := resolvePath(p)
		if err != nil {
			return fmt.Errorf("invalid path: %w", err)
		}
		if err := CheckPathAllowed(resolved); err != nil {
			return err
		}
		// Для glob с паттерном — проверяем что результат останется внутри разрешённых
		if toolName == "glob" || toolName == "find_files" {
			if pattern, ok := args["pattern"]; ok && pattern != "" {
				matchPath := filepath.Join(resolved, pattern)
				matchPath = filepath.Clean(matchPath)
				if err := CheckPathAllowed(matchPath); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
