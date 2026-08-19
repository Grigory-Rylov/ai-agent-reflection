package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)


const (
	DefaultToolOutputMaxLines = 2000
	DefaultToolOutputMaxBytes = 50 * 1024
	truncationRetention       = 7 * 24 * time.Hour
)


type TruncateOptions struct {
	MaxLines    int
	MaxBytes    int
	Dir         string
	HasTaskTool bool
}


type TruncateResult struct {
	Content    string
	Truncated  bool
	OutputPath string
}


func TruncateToolResult(content string, opts TruncateOptions) (TruncateResult, error) {
	if opts.MaxLines <= 0 {
		opts.MaxLines = DefaultToolOutputMaxLines
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultToolOutputMaxBytes
	}

	lines := strings.Split(content, "\n")
	if len(lines) <= opts.MaxLines && len(content) <= opts.MaxBytes {
		return TruncateResult{Content: content}, nil
	}

	dir := opts.Dir
	if dir == "" {
		dir = filepath.Join(WorkingDir, "tool-output")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return TruncateResult{}, fmt.Errorf("create tool-output dir: %w", err)
	}

	outputPath := filepath.Join(dir, truncationFilename())
	if err := os.WriteFile(outputPath, []byte(content), 0o644); err != nil {
		return TruncateResult{}, fmt.Errorf("save tool output: %w", err)
	}

	cleanupTruncationDir(dir)

	preview, removed, unit := headPreview(lines, opts.MaxLines, opts.MaxBytes, len(content))
	return TruncateResult{
		Content:    fmt.Sprintf("%s\n\n...%d %s truncated...\n\n%s", preview, removed, unit, truncationHint(outputPath, opts.HasTaskTool)),
		Truncated:  true,
		OutputPath: outputPath,
	}, nil
}


func headPreview(lines []string, maxLines, maxBytes, totalBytes int) (preview string, removed int, unit string) {
	out := make([]string, 0, maxLines)
	bytes := 0
	hitBytes := false

	for i := 0; i < len(lines) && i < maxLines; i++ {
		size := len(lines[i])
		if i > 0 {
			size++
		}
		if bytes+size > maxBytes {
			hitBytes = true
			break
		}
		out = append(out, lines[i])
		bytes += size
	}

	if hitBytes {
		return strings.Join(out, "\n"), totalBytes - bytes, "bytes"
	}
	return strings.Join(out, "\n"), len(lines) - len(out), "lines"
}


func truncationHint(outputPath string, hasTaskTool bool) string {
	if hasTaskTool {
		return fmt.Sprintf(
			"The tool call succeeded but the output was truncated. Full output saved to: %s\n"+
				"Use the Task tool to delegate processing of this file (use Grep, Read with offset/limit). "+
				"Do NOT read the full file yourself - delegate to save context. Available agents: list via /help",
			outputPath)
	}
	return fmt.Sprintf(
		"The tool call succeeded but the output was truncated. Full output saved to: %s\n"+
			"Use Grep to search the full content or Read with offset/limit to view specific sections.",
		outputPath)
}


func truncationFilename() string {
	return fmt.Sprintf("tool_%d", time.Now().UnixNano())
}


func cleanupTruncationDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-truncationRetention)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "tool_") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			removeStaleToolOutput(filepath.Join(dir, entry.Name()))
		}
	}
}


func removeStaleToolOutput(path string) {
	_ = os.Remove(path)
}
