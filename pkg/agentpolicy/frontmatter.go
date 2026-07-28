package agentpolicy

import (
	"fmt"
	"os"
	"strings"
)

type Frontmatter struct {
	Description string `yaml:"description"`
	Mode        string `yaml:"mode"`
	Hidden      bool   `yaml:"hidden"`
	Leaf        bool   `yaml:"leaf"`
	Review      bool   `yaml:"review"`
	Model       string `yaml:"model"`
}

func ParseFrontmatterFile(path string) (Frontmatter, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Frontmatter{}, "", fmt.Errorf("read %s: %w", path, err)
	}
	return ParseFrontmatter(string(data))
}

func ParseFrontmatter(content string) (Frontmatter, string, error) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return Frontmatter{}, content, nil
	}

	end := strings.Index(content[3:], "\n---")
	if end < 0 {
		return Frontmatter{}, content, nil
	}
	end += 3

	fmRaw := content[3:end]
	body := strings.TrimSpace(content[end+4:])

	fm := Frontmatter{}

	for _, line := range strings.Split(fmRaw, "\n") {
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])

		switch key {
		case "description":
			fm.Description = strings.Trim(val, "\"'")
		case "mode":
			fm.Mode = val
		case "hidden":
			fm.Hidden = val == "true"
		case "leaf":
			fm.Leaf = val == "true"
		case "review":
			fm.Review = val == "true"
		case "model":
			fm.Model = val
		}
	}

	return fm, body, nil
}
