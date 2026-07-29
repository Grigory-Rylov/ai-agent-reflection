package agentpolicy

import (
	"fmt"
	"os"
	"strings"
)

func LoadMDPrompt(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", path, err)
	}
	content := string(data)

	if strings.HasPrefix(content, "---\n") {
		end := strings.Index(content[4:], "\n---")
		if end >= 0 {
			content = content[4+end+5:]
		}
	}

	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("empty prompt in %s", path)
	}
	return content, nil
}
