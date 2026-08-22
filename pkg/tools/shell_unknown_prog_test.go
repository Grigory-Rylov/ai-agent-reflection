package tools

import (
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/access"
)

var ghBodyLines = []string{
	"## Summary",
	"Refines the shell-permission module so the agent stops nagging for clearly",
	"harmless read-only inspection, while still gating every operation that could",
	"mutate state outside the whitelisted directories.",
	"",
	"## Changes",
	"- **Absolute programs** now flow through the regular read-only / mutation",
	"  analysis instead of a blanket \"any absolute path => ask\" rule. Pure readers",
	"  (cat, ls, grep, ...) are always auto-approved; interpreter/script",
	"  launches stay gated; go with destructive verbs stay gated.",
	"- **Heredoc awareness** in the command splitter: a commit -F - <<EOF ... EOF",
	"  payload is treated as one atomic unit, so the permission prompt shows the real",
	"  intent (allow git commit) instead of leaking arbitrary lines of the commit",
	"  message body.",
	"- Permission prompts surface only the non-safe fragment(s), not the whole chain.",
	"",
	"## Invariant preserved",
	"Any action capable of rewriting files outside allowed dirs (redirects,",
	"tee/rm/cp, find -delete|-exec, interpreters w/ scripts, env/path",
	"mutations) still requires explicit approval.",
	"",
	"## Verification",
	"Full go test ./... green across all packages. New regression coverage added",
	"for the exact command shapes that previously mis-triggered.",
}

func TestShellCommandFilesystemSafeGhPrCreate(t *testing.T) {
	title := "fix(permission): read-only ops auto-pass, mutations ask; heredoc-safe splitting"
	body := strings.Join(ghBodyLines, "\n")
	cmd := "gh pr create --base master --head fix/review-2026-08 --title \"" + title + "\" --body \"$(cat <<'PRDESC'\n" + body + "\nPRDESC\n)\" 2>&1"

	dir := t.TempDir()
	prevWD := WorkingDir
	SetWorkingDir(dir)
	SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		SetAccessController(nil)
		SetWorkingDir(prevWD)
	})

	if !ShellCommandFilesystemSafe(cmd) {
		t.Fatalf("gh pr create with heredoc body must be safe (no local fs mutation)\nPROBLEMATICS=%v", ProblematicShellSubCommands(cmd))
	}
}

func TestShellCommandFilesystemSafeUnknownProgOutsidePath(t *testing.T) {
	dir := t.TempDir()
	prevWD := WorkingDir
	SetWorkingDir(dir)
	SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		SetAccessController(nil)
		SetWorkingDir(prevWD)
	})

	if ShellCommandFilesystemSafe("mytool /etc/shadow") {
		t.Error("unknown program with absolute path outside whitelist must ask")
	}
}

func TestShellCommandFilesystemSafeRedirectOutsideWhitelist(t *testing.T) {
	dir := t.TempDir()
	prevWD := WorkingDir
	SetWorkingDir(dir)
	SetAccessController(access.NewController([]string{dir}))
	t.Cleanup(func() {
		SetAccessController(nil)
		SetWorkingDir(prevWD)
	})

	if ShellCommandFilesystemSafe("mytool data.csv > /etc/output.csv") {
		t.Error("redirection to outside whitelist must ask")
	}
}
