package agentloop

import (
	"strings"
)

type SubAgentResult struct {
	Status  string
	Summary string
	Files   []string
	Next    string
	Raw     string
}

var subAgentStatuses = map[string]bool{
	"success": true,
	"failure": true,
	"partial": true,
}

func ParseSubAgentResult(text string) SubAgentResult {
	res := SubAgentResult{Raw: text}
	block := extractResultBlock(text)
	if block == nil {
		res.Status = "partial"
		res.Summary = lastParagraph(text)
		return res
	}
	res.Status = "partial"
	for key, value := range block {
		switch key {
		case "status":
			if subAgentStatuses[value] {
				res.Status = value
			}
		case "summary":
			res.Summary = value
		case "files":
			res.Files = parseFileList(value)
		case "next":
			res.Next = value
		}
	}
	if res.Status == "partial" && res.Summary == "" {
		res.Summary = lastParagraph(text)
	}
	return res
}

func extractResultBlock(text string) map[string]string {
	lines := strings.Split(text, "\n")
	var block map[string]string
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inBlock {
			if strings.EqualFold(trimmed, "RESULT:") || strings.HasPrefix(strings.ToUpper(trimmed), "RESULT:") {
				inBlock = true
				block = map[string]string{}
				rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "RESULT:"), "result:"))
				if rest != "" {
					splitKey(rest, block)
				}
			}
			continue
		}
		if trimmed == "" {
			if len(block) > 0 {
				return block
			}
			continue
		}
		if !strings.Contains(trimmed, ":") {
			continue
		}
		splitKey(trimmed, block)
	}
	if inBlock && len(block) > 0 {
		return block
	}
	if inBlock {
		return block
	}
	return nil
}

func splitKey(line string, block map[string]string) {
	idx := strings.Index(line, ":")
	key := strings.ToLower(strings.TrimSpace(line[:idx]))
	value := strings.TrimSpace(line[idx+1:])
	if key == "" {
		return
	}
	block[key] = value
}

func parseFileList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "none") {
		return nil
	}
	parts := strings.Split(value, ",")
	files := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			files = append(files, p)
		}
	}
	return files
}

func lastParagraph(text string) string {
	paragraphs := strings.Split(text, "\n\n")
	for i := len(paragraphs) - 1; i >= 0; i-- {
		p := strings.TrimSpace(paragraphs[i])
		if p != "" {
			return p
		}
	}
	return strings.TrimSpace(text)
}