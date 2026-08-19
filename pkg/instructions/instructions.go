


package instructions

import (
	"os"
	"path/filepath"
	"strings"
)


var projectFileNames = []string{"AGENTS.md", "CLAUDE.md"}


var configDir = ""


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


func globalFiles() []string {
	for _, name := range projectFileNames {
		p := filepath.Join(globalConfigDir(), name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return []string{p}
		}
	}
	return nil
}


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
