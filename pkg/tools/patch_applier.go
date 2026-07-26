package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func applyFilePatch(path string, pf PatchFile) map[string]interface{} {
	switch {
	case pf.IsNew:
		return applyNewFile(path, pf)
	case pf.IsDelete:
		return applyDeleteFile(path)
	default:
		return applyUpdateFile(path, pf)
	}
}

func applyNewFile(path string, pf PatchFile) map[string]interface{} {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return errorResult(path, fmt.Errorf("create dir: %w", err))
	}

	content := buildContentFromHunks(pf.Hunks)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return errorResult(path, fmt.Errorf("write: %w", err))
	}

	return map[string]interface{}{
		"file":   path,
		"status": "created",
		"size":   len(content),
	}
}

func applyDeleteFile(path string) map[string]interface{} {
	if err := os.Remove(path); err != nil {
		return errorResult(path, fmt.Errorf("delete: %w", err))
	}
	return map[string]interface{}{
		"file":   path,
		"status": "deleted",
	}
}

func applyUpdateFile(path string, pf PatchFile) map[string]interface{} {
	data, err := os.ReadFile(path)
	if err != nil {
		return errorResult(path, fmt.Errorf("read: %w", err))
	}

	lines := splitLines(string(data))
	offset := 0

	for _, hunk := range pf.Hunks {
		targetStart := hunk.OldStart - 1 + offset
		if targetStart < 0 {
			targetStart = 0
		}
		if targetStart > len(lines) {
			targetStart = len(lines)
		}

		contextLines, _, removedCount := classifyHunkLines(hunk)
		contextMatch := findContext(lines, targetStart, contextLines, removedCount)
		if contextMatch < 0 {
			contextMatch = targetStart
		}

		lines, offset = applyHunk(lines, contextMatch, hunk, offset)
	}

	result := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(result), 0644); err != nil {
		return errorResult(path, fmt.Errorf("write: %w", err))
	}

	return map[string]interface{}{
		"file":   path,
		"status": "updated",
		"size":   len(result),
	}
}

func buildContentFromHunks(hunks []Hunk) string {
	var b strings.Builder
	for _, hunk := range hunks {
		for _, line := range hunk.Lines {
			if strings.HasPrefix(line, "+") {
				b.WriteString(line[1:])
				b.WriteByte('\n')
			} else if !strings.HasPrefix(line, "-") &&
				!strings.HasPrefix(line, "\\") {
				b.WriteString(line)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

func splitLines(content string) []string {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func classifyHunkLines(hunk Hunk) (contextLines, addedLines []string, removedCount int) {
	for _, l := range hunk.Lines {
		switch {
		case strings.HasPrefix(l, " ") || strings.HasPrefix(l, "-"):
			contextLines = append(contextLines, l[1:])
		case strings.HasPrefix(l, "+"):
			addedLines = append(addedLines, l[1:])
		}
		if strings.HasPrefix(l, "-") {
			removedCount++
		}
	}
	return
}

func applyHunk(lines []string, start int, hunk Hunk, offset int) ([]string, int) {
	pos := start
	for _, l := range hunk.Lines {
		switch {
		case strings.HasPrefix(l, "-"):
			if pos < len(lines) {
				lines = append(lines[:pos], lines[pos+1:]...)
				offset--
			}
		case strings.HasPrefix(l, "+"):
			insertLine := l[1:]
			insertPos := pos
			if insertPos > len(lines) {
				insertPos = len(lines)
			}
			lines = append(lines[:insertPos],
				append([]string{insertLine}, lines[insertPos:]...)...)
			pos++
			offset++
		case strings.HasPrefix(l, " "):
			pos++
		}
	}
	return lines, offset
}

func findContext(lines []string, start int, contextLines []string, removedCount int) int {
	if len(contextLines) == 0 {
		return start
	}

	contextBefore := removedCount
	if contextBefore > len(contextLines) {
		contextBefore = len(contextLines)
	}

	searchStart := start - contextBefore
	if searchStart < 0 {
		searchStart = 0
	}
	searchEnd := start + 3
	if searchEnd > len(lines) {
		searchEnd = len(lines)
	}

	for i := searchStart; i <= searchEnd &&
		i+len(contextLines) <= len(lines); i++ {
		if matchContextLines(lines[i:], contextLines) {
			return i
		}
	}
	return -1
}

func matchContextLines(candidate, contextLines []string) bool {
	for j, cl := range contextLines {
		if candidate[j] != cl {
			return false
		}
	}
	return true
}
