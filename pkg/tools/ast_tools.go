package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"
)

const (
	astSnippetMaxLines = 6
	astSnippetMaxChars = 400
)

type astPos struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type astMatch struct {
	Text  string `json:"text"`
	Range struct {
		Start astPos `json:"start"`
		End   astPos `json:"end"`
	} `json:"range"`
	File        string `json:"file"`
	Lines       string `json:"lines"`
	Replacement string `json:"replacement,omitempty"`
}

func resolveAstGrep() (string, error) {
	if p, err := exec.LookPath("ast-grep"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("sg"); err == nil {
		out, err := exec.Command(p, "--version").Output()
		if err == nil && strings.Contains(string(out), "ast-grep") {
			return p, nil
		}
	}
	return "", fmt.Errorf("ast-grep binary not found: install with `npm install -g @ast-grep/cli` or `cargo install ast-grep --locked`")
}

func runAstGrep(ctx context.Context, bin string, args []string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if WorkingDir != "" {
		cmd.Dir = WorkingDir
	}
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func astGrepArgs(pattern, path, lang string, extra ...string) []string {
	if path == "" {
		path = "."
	}
	args := []string{"run", "-p", pattern}
	if lang != "" {
		args = append(args, "-l", lang)
	}
	args = append(args, extra...)
	return append(args, path)
}

func parseAstMatches(out string) ([]astMatch, error) {
	var matches []astMatch
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &matches); err != nil {
		return nil, fmt.Errorf("failed to parse ast-grep output: %v", err)
	}
	return matches, nil
}

func astMatchFiles(matches []astMatch) []string {
	seen := make(map[string]bool)
	files := make([]string, 0, len(matches))
	for _, m := range matches {
		if !seen[m.File] {
			seen[m.File] = true
			files = append(files, m.File)
		}
	}
	return files
}

func astTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "y", "on":
		return true
	}
	return false
}

func truncateSnippet(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	trimmed := len(lines) > astSnippetMaxLines
	if trimmed {
		lines = lines[:astSnippetMaxLines]
	}
	out := strings.Join(lines, "\n")
	if utf8.RuneCountInString(out) > astSnippetMaxChars {
		out = string([]rune(out)[:astSnippetMaxChars])
		trimmed = true
	}
	if trimmed {
		out += "\n..."
	}
	return out
}

func prefixBlock(prefix, text string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func formatAstMatches(matches []astMatch, withRewrite bool) string {
	var b strings.Builder
	for _, m := range matches {
		fmt.Fprintf(&b, "%s:%d:%d\n", m.File, m.Range.Start.Line+1, m.Range.Start.Column+1)
		if withRewrite {
			b.WriteString(prefixBlock("- ", truncateSnippet(m.Text)))
			b.WriteString("\n")
			b.WriteString(prefixBlock("+ ", truncateSnippet(m.Replacement)))
		} else {
			b.WriteString(prefixBlock("  ", truncateSnippet(m.Text)))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func astRunError(stderr string, err error) ToolResult {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = err.Error()
	}
	return ToolResult{Success: false, Error: fmt.Sprintf("ast-grep failed: %s", msg)}
}

type AstGrepTool struct{}

func (t *AstGrepTool) Name() string {
	return "ast_grep"
}

func (t *AstGrepTool) Description() string {
	return "Structural code search with ast-grep patterns (AST-aware, not text). Use $VAR to capture one node and $$$ARGS for any number of nodes. Returns file:line:col and a snippet for each match."
}

func (t *AstGrepTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": CreateStringParameter("pattern", "AST pattern to match, e.g. 'func $F()' or 'console.log($$$)'", true),
			"path":    CreateStringParameter("path", "File or directory to search (default: current directory)", false),
			"lang":    CreateStringParameter("lang", "Source language (e.g. go, tsx, python). Auto-detected from file extension when omitted", false),
		},
		"required": []string{"pattern"},
	}
}

func (t *AstGrepTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	pattern := inputs["pattern"]
	if pattern == "" {
		return ToolResult{Success: false, Error: "pattern parameter is required"}, nil
	}
	bin, err := resolveAstGrep()
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}, nil
	}
	stdout, stderr, runErr := runAstGrep(ctx, bin, astGrepArgs(pattern, inputs["path"], inputs["lang"], "--json=compact"))
	if runErr != nil {
		return astRunError(stderr, runErr), nil
	}
	matches, parseErr := parseAstMatches(stdout)
	if parseErr != nil {
		return ToolResult{Success: false, Error: parseErr.Error()}, nil
	}
	return ToolResult{Success: true, Data: map[string]interface{}{
		"count":  len(matches),
		"files":  astMatchFiles(matches),
		"output": formatAstMatches(matches, false),
	}}, nil
}

type AstEditTool struct{}

func (t *AstEditTool) Name() string {
	return "ast_edit"
}

func (t *AstEditTool) Description() string {
	return "Structural code rewrite with ast-grep: replace every AST node matching the pattern with the rewrite template (captures like $VAR/$$$ARGS are substituted; empty rewrite deletes the match). Set dry_run=true to preview changes without touching files."
}

func (t *AstEditTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": CreateStringParameter("pattern", "AST pattern to match, e.g. 'func $F()' or 'console.log($$$)'", true),
			"rewrite": CreateStringParameter("rewrite", "Replacement template; captures ($VAR) from the pattern are substituted", true),
			"path":    CreateStringParameter("path", "File or directory to rewrite (default: current directory)", false),
			"lang":    CreateStringParameter("lang", "Source language (e.g. go, tsx, python). Auto-detected from file extension when omitted", false),
			"dry_run": CreateBooleanParameter("dry_run", "Preview the changes without modifying any files (default: false)", false),
		},
		"required": []string{"pattern", "rewrite"},
	}
}

func (t *AstEditTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	pattern := inputs["pattern"]
	if pattern == "" {
		return ToolResult{Success: false, Error: "pattern parameter is required"}, nil
	}
	rewrite, ok := inputs["rewrite"]
	if !ok {
		return ToolResult{Success: false, Error: "rewrite parameter is required (pass an empty string to delete matches)"}, nil
	}
	bin, err := resolveAstGrep()
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}, nil
	}
	dryRun := astTruthy(inputs["dry_run"])
	stdout, stderr, runErr := runAstGrep(ctx, bin, astGrepArgs(pattern, inputs["path"], inputs["lang"], "-r", rewrite, "--json=compact"))
	if runErr != nil {
		return astRunError(stderr, runErr), nil
	}
	matches, parseErr := parseAstMatches(stdout)
	if parseErr != nil {
		return ToolResult{Success: false, Error: parseErr.Error()}, nil
	}
	if len(matches) == 0 {
		return ToolResult{Success: true, Data: map[string]interface{}{
			"count": 0, "files": []string{}, "dry_run": dryRun,
			"output": "No matches.",
		}}, nil
	}
	files := astMatchFiles(matches)
	if dryRun {
		return ToolResult{Success: true, Data: map[string]interface{}{
			"count": len(matches), "files": files, "dry_run": true,
			"output": fmt.Sprintf("Dry run: %d match(es) in %d file(s) would change\n%s", len(matches), len(files), formatAstMatches(matches, true)),
		}}, nil
	}
	stdout, stderr, runErr = runAstGrep(ctx, bin, astGrepArgs(pattern, inputs["path"], inputs["lang"], "-r", rewrite, "-U"))
	if runErr != nil {
		return astRunError(stderr, runErr), nil
	}
	return ToolResult{Success: true, Data: map[string]interface{}{
		"count": len(matches), "files": files, "dry_run": false,
		"output": strings.TrimSpace(fmt.Sprintf("Rewrote %d match(es) in %d file(s):\n%s", len(matches), len(files), strings.Join(files, "\n"))),
	}}, nil
}
