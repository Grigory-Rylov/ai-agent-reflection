package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/access"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/permission"
)

func TestZZConfirmRootCause(t *testing.T) {
	title := "fix(permission): read-only ops auto-pass, mutations ask"
	body := "## Summary\n\nRefines the shell-permission module so read-only operations\nnever trigger a prompt, while mutations outside the whitelist keep asking.\n\nKey changes:\n\n- Absolute program invocations now route through the regular\n  read-only / mutation pipeline instead of blanket deny.\n- Heredoc bodies are kept atomic by the command splitter, so a\n  commit message can no longer surface as a fake sub-command."
	cmd := "gh pr create --base master --head fix/review-2026-08 --title \"" + title + "\" --body \"$(cat <<'PRDESC'\n" + body + "\nPRDESC)\""

	ctrl := access.NewController([]string{"/home/grishberg/projects/go/ai-agent-reflection"})
	prevWD := WorkingDir
	SetWorkingDir("/")
	SetAccessController(ctrl)
	defer func() { SetAccessController(nil); SetWorkingDir(prevWD) }()

	sub := permission.SplitCommands(cmd)[0]
	toks := permission.Tokenize(sub)
	fmt.Printf("top-level toks=%d\n", len(toks))
	for i, tk := range toks {
		flag := ""
		if strings.ContainsAny(tk, "\n\t ") {
			flag += "[WS]"
		}
		if looksLikePath(tk) {
			flag += "[LOOKS-PATH]"
		}
		if isExplicitPathToken(tk) {
			flag += "[EXPLICIT]"
		}
		if flag != "" {
			fmt.Printf("  top[%d]%s len=%d head=%q\n", i, flag, len(tk), trimHead(tk, 40))
		}
	}
	for _, m := range nestedSubRe.FindAllStringSubmatch(sub, -1) {
		inner := m[1]
		ip := collectFilePaths(inner, nil)
		fmt.Printf("INNER=%q\n  innerPaths=%v\n", trimHead(inner, 50), ip)
		for _, p := range ip {
			fmt.Printf("    path=%q wsIn=%v\n", trimHead(p, 80), strings.ContainsAny(p, "\n\t "))
		}
	}
	fmt.Printf("overall safe=%v\n", ShellCommandFilesystemSafe(cmd))
}

func trimHead(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "")
	if len(s) <= n {
		return s
	}
	return s[:n] + ".."
}
