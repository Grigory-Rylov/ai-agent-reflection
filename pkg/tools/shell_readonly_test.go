package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/access"
)

func TestShellCommandFilesystemSafeReadOnly(t *testing.T) {
	setup := func(t *testing.T) string {
		dir, _ := setupAccessTest(t)
		prevWD := WorkingDir
		t.Cleanup(func() {
			SetWorkingDir(prevWD)
			cleanupAccessTest(t, dir)
		})
		SetWorkingDir(dir)
		return dir
	}

	t.Run("inspect go toolchain command is safe", func(t *testing.T) {
		setup(t)
		cmd := "ls /usr/local/go/bin/ 2>/dev/null && which go 2>/dev/null && ls ~/go 2>/dev/null && head -3 && cat build.sh"
		if !ShellCommandFilesystemSafe(cmd) {
			t.Error("expected true: read-only inspect of system paths should not ask")
		}
	})

	t.Run("filesystem search for binary is safe", func(t *testing.T) {
		setup(t)
		cmd := "find / -maxdepth 4 -name 'go' -type f -path '*bin*' 2>/dev/null && head && ls /mnt/data/usr/local/go/bin 2>/dev/null"
		if !ShellCommandFilesystemSafe(cmd) {
			t.Error("expected true: read-only find/ls of system paths should not ask")
		}
	})

	t.Run("export PATH plus go test chain is safe", func(t *testing.T) {
		setup(t)
		cmd := "ls -la /usr/local/go/bin/ && export PATH=\"$PATH:/mnt/data/usr/local/go/bin\" && go test ./pkg/vk/ -run 'TestLogCommand' 2>&1 && tail -6"
		if !ShellCommandFilesystemSafe(cmd) {
			t.Error("expected true: read-only ls plus env export and go test should not ask")
		}
	})

	t.Run("inspect both install dirs and PATH is safe", func(t *testing.T) {
		setup(t)
		cmd := "ls /usr/local/go/bin 2>/dev/null && ls /mnt/data/usr/local/go/bin 2>/dev/null && which -a go 2>/dev/null && echo PATH=$PATH && tr ':' '\\n' && grep -i go"
		if !ShellCommandFilesystemSafe(cmd) {
			t.Error("expected true: read-only inspect of system paths and PATH should not ask")
		}
	})

	t.Run("inspect mnt data go bin and home go dir is safe", func(t *testing.T) {
		setup(t)
		cmd := "ls -la /mnt/data/usr/local/go/bin/ 2>&1 && head -3 && echo \"HOME=$HOME\" && ls ~/go 2>/dev/null"
		if !ShellCommandFilesystemSafe(cmd) {
			t.Error("expected true: read-only inspect of system paths should not ask")
		}
	})

	t.Run("reading via absolute program outside allowed dir is safe", func(t *testing.T) {
		setup(t)
		cmd := "ls -la /mnt/data/usr/local/go/bin/ 2>&1 && /mnt/data/usr/local/go/bin/go version 2>&1 && head -3 && echo \"HOME=$HOME\" && ls ~/go 2>/dev/null"
		if !ShellCommandFilesystemSafe(cmd) {
			t.Error("expected true: read-only verb via absolute program performs no mutation, should not ask")
		}
	})

	t.Run("find usr for go binary chain is safe", func(t *testing.T) {
		setup(t)
		cmd := "ls -la /mnt/data/usr/local/go/bin 2>&1 && ls ~/go/bin 2>/dev/null && which go golang 2>&1 && find /usr -maxdepth 4 -name 'go' -type f 2>/dev/null && head && echo PATH=$PATH"
		if !ShellCommandFilesystemSafe(cmd) {
			t.Error("expected true: read-only inspection chain performs no mutation, should not ask")
		}
	})

	t.Run("find usr chain with mutation outside allowed dir is not safe", func(t *testing.T) {
		setup(t)
		cmd := "ls -la /mnt/data/usr/local/go/bin 2>&1 && which go golang 2>&1 && find /usr -maxdepth 4 -name 'go' -type f 2>/dev/null && rm -rf /usr/local/go/bin"
		if ShellCommandFilesystemSafe(cmd) {
			t.Error("expected false: rm outside allowed dir must ask")
		}
	})

	t.Run("plain cat of system file is safe", func(t *testing.T) {
		setup(t)
		if !ShellCommandFilesystemSafe("cat /etc/passwd") {
			t.Error("expected true: plain read outside allowed dir should not ask")
		}
	})

	t.Run("ls home go dir is safe", func(t *testing.T) {
		setup(t)
		if !ShellCommandFilesystemSafe("ls ~/go 2>/dev/null") {
			t.Error("expected true: read-only ls of ~ path should not ask")
		}
	})

	t.Run("tail without file is safe", func(t *testing.T) {
		setup(t)
		if !ShellCommandFilesystemSafe("tail -6") {
			t.Error("expected true: tail with no file argument should not ask")
		}
	})

	t.Run("tr reading proc environ via stdin redirect is safe", func(t *testing.T) {
		setup(t)
		if !ShellCommandFilesystemSafe(`tr '\0' '\n' < /proc/34947/environ`) {
			t.Error("expected true: tr reading /proc/<pid>/environ via stdin redirect is read-only, should not ask")
		}
	})
}

func TestShellCommandFilesystemSafeUnknownProgWithHeredocPayload(t *testing.T) {
	newCtrl := func(t *testing.T, dirs ...string) {
		prevWD := WorkingDir
		SetWorkingDir(t.TempDir())
		SetAccessController(access.NewController(dirs))
		t.Cleanup(func() {
			SetAccessController(nil)
			SetWorkingDir(prevWD)
		})
	}

	t.Run("gh pr create with heredoc body is safe despite unknown program", func(t *testing.T) {
		newCtrl(t, "/home/grishberg/projects/go/ai-agent-reflection")
		title := "fix(permission): read-only ops auto-pass, mutations ask"
		body := "## Summary\n\nRefines the shell-permission module so read-only operations\nnever trigger a prompt, while mutations outside the whitelist keep asking.\n\nKey changes:\n\n- Absolute program invocations now route through the regular\n  read-only / mutation pipeline instead of blanket deny.\n- Heredoc bodies are kept atomic by the command splitter, so a\n  commit message can no longer surface as a fake sub-command."
		cmd := "gh pr create --base master --head fix/review-2026-08 --title \"" + title + "\" --body \"$(cat <<'PRDESC'\n" + body + "\nPRDESC)\""
		if ShellCommandFilesystemSafe(cmd) {
			return
		}
		t.Error("expected true: unknown program whose only outside-list material is heredoc prose must not ask")
	})

	t.Run("unknown program with real outside write path still asks", func(t *testing.T) {
		newCtrl(t, "/home/grishberg/projects/go/ai-agent-reflection")
		if ShellCommandFilesystemSafe("mytool --input /etc/shadow") {
			t.Error("expected false: unknown program acting on an outside-list path must ask")
		}
	})
}

func TestShellCommandFilesystemSafeAbsoluteProgram(t *testing.T) {
	newCtrl := func(t *testing.T, dirs ...string) {
		prevWD := WorkingDir
		SetWorkingDir(t.TempDir())
		SetAccessController(access.NewController(dirs))
		t.Cleanup(func() {
			SetAccessController(nil)
			SetWorkingDir(prevWD)
		})
	}

	t.Run("absolute go vet with tmp in whitelist is safe", func(t *testing.T) {
		newCtrl(t, "/tmp")
		cmd := "/tmp/godl/go/bin/go vet ./pkg/permission/ 2>&1"
		if ShellCommandFilesystemSafe(cmd) {
			return
		}
		t.Error("expected true: read-only go vet launched from whitelisted /tmp should not ask")
	})

	t.Run("absolute go build and test chain from tmp is safe", func(t *testing.T) {
		newCtrl(t, "/tmp")
		cmd := "/tmp/godl/go/bin/go build ./... 2>&1 && /tmp/godl/go/bin/go test ./... 2>&1"
		if ShellCommandFilesystemSafe(cmd) {
			return
		}
		t.Error("expected true: go invoked from whitelisted /tmp should not ask")
	})

	t.Run("absolute python script outside whitelist asks", func(t *testing.T) {
		dir := t.TempDir()
		newCtrl(t, dir)
		cmd := "/mnt/data/usr/bin/python3 /mnt/data/evil/setup.py"
		if ShellCommandFilesystemSafe(cmd) {
			t.Error("expected false: executing an interpreter script outside whitelist must ask")
		}
	})

	t.Run("absolute rm outside whitelist asks", func(t *testing.T) {
		newCtrl(t, "/tmp")
		if ShellCommandFilesystemSafe("/tmp/rm -rf /usr/local/go/bin") {
			t.Error("expected false: mutating binary touching outside dir must ask")
		}
	})
}

func TestIsReadOnlySubcommand(t *testing.T) {
	tests := []struct {
		name string
		sub  string
		want bool
	}{
		{"cat absolute path", "cat /etc/passwd", true},
		{"ls system dir", "ls -la /usr/local/go/bin/", true},
		{"which binary", "which go 2>/dev/null", true},
		{"head with count", "head -3", true},
		{"tail with count", "tail -6", true},
		{"find without mutation flags", "find / -maxdepth 4 -name 'go' -type f -path '*bin*' 2>/dev/null", true},
		{"find with executable flag", "find /tmp /root /home -maxdepth 4 -name 'go' -type f -executable 2>/dev/null", true},
		{"find with delete flag", "find /tmp -name '*.log' -delete", false},
		{"find with exec flag", `find /tmp -name x -exec rm {} \;`, false},
		{"grep file", `grep 'func' /home/src/main.go`, true},
		{"diff files", "diff /tmp/a.txt /tmp/b.txt", true},
		{"cp is not read-only", "cp /etc/passwd /tmp/x", false},
		{"rm is not read-only", "rm -rf /usr/local/go/bin", false},
		{"sed is not read-only", `sed 's/a/b/' /tmp/f`, false},
		{"export is not a file op", `export PATH="$PATH:/x"`, true},
		{"go test is not a file op", "go test ./pkg/vk/", true},
		{"head with explicit file outside allowed dir", "head -3 /etc/passwd", true},
		{"tail following file outside allowed dir", "tail -f /var/log/syslog", true},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReadOnlySubcommand(tt.sub); got != tt.want {
				t.Errorf("IsReadOnlySubcommand(%q) = %v, want %v", tt.sub, got, tt.want)
			}
		})
	}
}

func TestShellCommandFilesystemSafeEnvMutations(t *testing.T) {
	setup := func(t *testing.T) string {
		dir, _ := setupAccessTest(t)
		prevWD := WorkingDir
		t.Cleanup(func() {
			SetWorkingDir(prevWD)
			cleanupAccessTest(t, dir)
		})
		SetWorkingDir(dir)
		return dir
	}

	t.Run("export PATH with absolute dir is safe", func(t *testing.T) {
		// export PATH=... is a pure in-process env mutation: no file is read
		// or written, so it must not trigger a permission prompt even when
		// the value contains absolute paths outside the allowed dirs.
		setup(t)
		if !ShellCommandFilesystemSafe("export PATH=/home/grishberg/.local/node/bin:$PATH") {
			t.Error("expected true: export PATH with absolute dir outside allowed dirs does not touch the filesystem")
		}
	})

	t.Run("export PATH chained with go version is safe", func(t *testing.T) {
		setup(t)
		cmd := `export PATH="/home/grishberg/.local/node/bin:$PATH" && go version`
		if !ShellCommandFilesystemSafe(cmd) {
			t.Error("expected true: export PATH plus read-only go version should not ask")
		}
	})

	t.Run("env assignment pointing outside allowed dir is not safe", func(t *testing.T) {
		setup(t)
		if ShellCommandFilesystemSafe("GOCACHE=/outside/go-cache go build ./...") {
			t.Error("expected false: env var with path outside allowed dirs must ask")
		}
	})

	t.Run("export assignment pointing outside allowed dir is not safe", func(t *testing.T) {
		setup(t)
		if ShellCommandFilesystemSafe(`export GOCACHE=/outside/go-cache && go test ./...`) {
			t.Error("expected false: export with path outside allowed dirs must ask")
		}
	})

	t.Run("env assignment relative to cwd is safe", func(t *testing.T) {
		dir := setup(t)
		cache := filepath.Join(dir, "go-cache")
		if !ShellCommandFilesystemSafe("GOCACHE=" + cache + " go build ./...") {
			t.Error("expected true: env var pointing inside allowed dir should not ask")
		}
	})

	t.Run("read-only cat piped into bare head is safe", func(t *testing.T) {
		setup(t)
		if !ShellCommandFilesystemSafe("cat /etc/passwd | head") {
			t.Error("expected true: cat piped into head is read-only and safe")
		}
	})
}

func TestShellCommandFilesystemSafeReadOnlyStillAsksOnWrite(t *testing.T) {
	setup := func(t *testing.T) string {
		dir, _ := setupAccessTest(t)
		prevWD := WorkingDir
		t.Cleanup(func() {
			SetWorkingDir(prevWD)
			cleanupAccessTest(t, dir)
		})
		SetWorkingDir(dir)
		return dir
	}

	t.Run("read-only cat with write redirect is not safe", func(t *testing.T) {
		setup(t)
		if ShellCommandFilesystemSafe("cat /etc/passwd > /tmp/leak.txt") {
			t.Error("expected false: redirection target outside allowed dir must still ask")
		}
	})

	t.Run("read-only ls chained with rm is not safe", func(t *testing.T) {
		setup(t)
		if ShellCommandFilesystemSafe("ls /usr/local/go/bin && rm -rf /usr/local/go/bin") {
			t.Error("expected false: mutating subcommand outside allowed dir must still ask")
		}
	})

	t.Run("find with delete is not safe", func(t *testing.T) {
		setup(t)
		if ShellCommandFilesystemSafe("find / -name '*.log' -delete 2>/dev/null") {
			t.Error("expected false: find -delete mutates the filesystem")
		}
	})

	t.Run("write redirect inside allowed dir is safe", func(t *testing.T) {
		dir := setup(t)
		if !ShellCommandFilesystemSafe("cat /etc/passwd > " + dir + "/out.txt") {
			t.Error("expected true: redirection target inside allowed dir should not ask")
		}
	})

	t.Run("write to outside allowed dir via tee is not safe", func(t *testing.T) {
		setup(t)
		if ShellCommandFilesystemSafe("echo hi | tee /etc/motd") {
			t.Error("expected false: tee target outside allowed dir must still ask")
		}
	})

	t.Run("read-only ls with write redirect inside allowed dir is safe", func(t *testing.T) {
		dir := setup(t)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if !ShellCommandFilesystemSafe("ls /usr/local/go/bin/ > " + dir + "/listing.txt") {
			t.Error("expected true: read-only ls redirected inside allowed dir should not ask")
		}
	})
}
