// Package instructions реализует чтение AGENTS.md/CLAUDE.md как в opencode:
// поиск от рабочей директории вверх до корня git-worktree + глобальный файл
// из конфиг-директории агента. Содержимое подставляется в системный промпт
// отдельным system-сообщением в формате "Instructions from: <путь>".
package instructions

import (
	"os"
	"path/filepath"
	"strings"
)

// projectFileNames — имена файлов-инструкций в порядке приоритета.
// Первое имя с хотя бы одним совпадением выигрывает (как в opencode).
var projectFileNames = []string{"AGENTS.md", "CLAUDE.md"}

// configDir — глобальная конфиг-директория агента (переопределяется в тестах).
var configDir = ""

// Build возвращает текст инструкций для рабочей директории dir:
// глобальный файл из конфиг-директории + все проектные AGENTS.md/CLAUDE.md
// от dir вверх до git-корня (ближайшие первыми).
// Пустой результат — если ничего не найдено.
func Build(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}

	files := globalFiles()
	files = append(files, projectFiles(abs)...)

	var sb strings.Builder
	seen := make(map[string]bool)
	for _, f := range files {
		if seen[f] {
			continue
		}
		seen[f] = true
		content, err := os.ReadFile(f)
		if err != nil || strings.TrimSpace(string(content)) == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("Instructions from: " + f + "\n")
		sb.WriteString(string(content))
	}
	return sb.String()
}

// globalFiles возвращает первый существующий файл-инструкцию
// из глобальной конфиг-директории агента.
func globalFiles() []string {
	for _, name := range projectFileNames {
		p := filepath.Join(globalConfigDir(), name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return []string{p}
		}
	}
	return nil
}

// projectFiles находит файлы-инструкции от start вверх до git-корня.
// Ближайший к start файл идёт первым. Не выходит за пределы git-репозитория
// и домашней директории.
func projectFiles(start string) []string {
	home, _ := os.UserHomeDir()
	root := gitRoot(start)
	if root == "" {
		root = start
	}

	for _, name := range projectFileNames {
		var found []string
		dir := filepath.Clean(start)
		for {
			p := filepath.Join(dir, name)
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				found = append(found, p)
			}
			if dir == root || dir == home || filepath.Dir(dir) == dir {
				break
			}
			dir = filepath.Dir(dir)
		}
		if len(found) > 0 {
			return found
		}
	}
	return nil
}

// gitRoot возвращает директорию с .git (корень git-worktree),
// поднимаясь вверх от start. Пустая строка — если git нет.
func gitRoot(start string) string {
	dir := filepath.Clean(start)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// globalConfigDir возвращает глобальную конфиг-директорию агента
// (по умолчанию ~/.config/ai-agent).
func globalConfigDir() string {
	if configDir != "" {
		return configDir
	}
	if dir := os.Getenv("AI_AGENT_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "ai-agent")
}
