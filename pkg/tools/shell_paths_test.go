package tools

import (
	"testing"
)

func TestExtractShellPaths(t *testing.T) {
	t.Run("extracts path from cat command", func(t *testing.T) {
		paths := ExtractShellPaths("cat /tmp/file.txt")
		if len(paths) != 1 || paths[0] != "/tmp/file.txt" {
			t.Errorf("expected [/tmp/file.txt], got %v", paths)
		}
	})

	t.Run("extracts path from ls command", func(t *testing.T) {
		paths := ExtractShellPaths("ls -la /tmp/dir")
		if len(paths) != 1 || paths[0] != "/tmp/dir" {
			t.Errorf("expected [/tmp/dir], got %v", paths)
		}
	})

	t.Run("extracts paths from cp command", func(t *testing.T) {
		paths := ExtractShellPaths("cp /tmp/src.txt /tmp/dst.txt")
		if len(paths) != 2 {
			t.Errorf("expected 2 paths, got %d: %v", len(paths), paths)
		}
	})

	t.Run("extracts path from grep command", func(t *testing.T) {
		paths := ExtractShellPaths("grep 'func' /home/src/main.go")
		if len(paths) != 1 || paths[0] != "/home/src/main.go" {
			t.Errorf("expected [/home/src/main.go], got %v", paths)
		}
	})

	t.Run("extracts path from rm command", func(t *testing.T) {
		paths := ExtractShellPaths("rm -rf /tmp/build")
		if len(paths) != 1 || paths[0] != "/tmp/build" {
			t.Errorf("expected [/tmp/build], got %v", paths)
		}
	})

	t.Run("extracts path from mkdir command", func(t *testing.T) {
		paths := ExtractShellPaths("mkdir -p /tmp/newdir/subdir")
		if len(paths) != 1 || paths[0] != "/tmp/newdir/subdir" {
			t.Errorf("expected [/tmp/newdir/subdir], got %v", paths)
		}
	})

	t.Run("extracts path from touch command", func(t *testing.T) {
		paths := ExtractShellPaths("touch /tmp/file.txt")
		if len(paths) != 1 || paths[0] != "/tmp/file.txt" {
			t.Errorf("expected [/tmp/file.txt], got %v", paths)
		}
	})

	t.Run("extracts path from head command", func(t *testing.T) {
		paths := ExtractShellPaths("head -n 5 /tmp/log.txt")
		if len(paths) != 1 || paths[0] != "/tmp/log.txt" {
			t.Errorf("expected [/tmp/log.txt], got %v", paths)
		}
	})

	t.Run("extracts path from tail command", func(t *testing.T) {
		paths := ExtractShellPaths("tail -f /tmp/output.log")
		if len(paths) != 1 || paths[0] != "/tmp/output.log" {
			t.Errorf("expected [/tmp/output.log], got %v", paths)
		}
	})

	t.Run("extracts path from diff command", func(t *testing.T) {
		paths := ExtractShellPaths("diff /tmp/a.txt /tmp/b.txt")
		if len(paths) != 2 {
			t.Errorf("expected 2 paths, got %d: %v", len(paths), paths)
		}
	})

	t.Run("extracts path from chmod command", func(t *testing.T) {
		paths := ExtractShellPaths("chmod 644 /tmp/script.sh")
		if len(paths) != 1 || paths[0] != "/tmp/script.sh" {
			t.Errorf("expected [/tmp/script.sh], got %v", paths)
		}
	})

	t.Run("extracts path from git command", func(t *testing.T) {
		paths := ExtractShellPaths("git add /home/project/file.go")
		if len(paths) != 1 || paths[0] != "/home/project/file.go" {
			t.Errorf("expected [/home/project/file.go], got %v", paths)
		}
	})

	t.Run("extracts path from mv command", func(t *testing.T) {
		paths := ExtractShellPaths("mv /tmp/old.txt /tmp/new.txt")
		if len(paths) != 2 {
			t.Errorf("expected 2 paths, got %d: %v", len(paths), paths)
		}
	})

	t.Run("extracts path from find command", func(t *testing.T) {
		paths := ExtractShellPaths("find /tmp -name '*.go'")
		if len(paths) != 1 || paths[0] != "/tmp" {
			t.Errorf("expected [/tmp], got %v", paths)
		}
	})

	t.Run("relative path from cat", func(t *testing.T) {
		paths := ExtractShellPaths("cat file.txt")
		if len(paths) != 1 || paths[0] != "file.txt" {
			t.Errorf("expected [file.txt], got %v", paths)
		}
	})

	t.Run("no paths for ping command", func(t *testing.T) {
		paths := ExtractShellPaths("ping -c 2 192.168.1.192")
		if len(paths) != 0 {
			t.Errorf("expected no paths for ping, got %v", paths)
		}
	})

	t.Run("no paths for ssh command", func(t *testing.T) {
		paths := ExtractShellPaths("ssh -o ConnectTimeout=5 grishberg@192.168.1.192 hostname")
		if len(paths) != 0 {
			t.Errorf("expected no paths for ssh, got %v", paths)
		}
	})

	t.Run("no paths for curl command", func(t *testing.T) {
		paths := ExtractShellPaths("curl http://localhost:8080")
		if len(paths) != 0 {
			t.Errorf("expected no paths for curl, got %v", paths)
		}
	})

	t.Run("no paths for echo command", func(t *testing.T) {
		paths := ExtractShellPaths("echo hello world")
		if len(paths) != 0 {
			t.Errorf("expected no paths for echo, got %v", paths)
		}
	})

	t.Run("no paths for go build", func(t *testing.T) {
		paths := ExtractShellPaths("go build -o agent .")
		if len(paths) != 0 {
			t.Errorf("expected no paths for go build, got %v", paths)
		}
	})

	t.Run("no paths for whoami", func(t *testing.T) {
		paths := ExtractShellPaths("whoami")
		if len(paths) != 0 {
			t.Errorf("expected no paths for whoami, got %v", paths)
		}
	})

	t.Run("no paths for hostname", func(t *testing.T) {
		paths := ExtractShellPaths("hostname")
		if len(paths) != 0 {
			t.Errorf("expected no paths for hostname, got %v", paths)
		}
	})

	t.Run("empty command returns empty", func(t *testing.T) {
		paths := ExtractShellPaths("")
		if len(paths) != 0 {
			t.Errorf("expected no paths for empty command, got %v", paths)
		}
	})
}

func TestShellCommandPathsAllowed(t *testing.T) {
	setup := func(t *testing.T) string {
		dir, _ := setupAccessTest(t)
		SetWorkingDir(dir)
		t.Cleanup(func() {
			SetWorkingDir("")
			cleanupAccessTest(t, dir)
		})
		return dir
	}

	t.Run("cat file inside allowed dir is allowed", func(t *testing.T) {
		dir := setup(t)
		if !ShellCommandPathsAllowed("cat " + dir + "/file.txt") {
			t.Error("expected true for file in allowed dir")
		}
	})

	t.Run("cat file outside allowed dir is denied", func(t *testing.T) {
		_ = setup(t)
		if ShellCommandPathsAllowed("cat /etc/passwd") {
			t.Error("expected false for file outside allowed dir")
		}
	})

	t.Run("ls without paths is allowed", func(t *testing.T) {
		_ = setup(t)
		if !ShellCommandPathsAllowed("ls -la") {
			t.Error("expected true for file command without explicit paths")
		}
	})

	t.Run("git status is allowed", func(t *testing.T) {
		_ = setup(t)
		if !ShellCommandPathsAllowed("git status") {
			t.Error("expected true for git command in allowed dir")
		}
	})

	t.Run("non-file command is denied", func(t *testing.T) {
		_ = setup(t)
		if ShellCommandPathsAllowed("pip install requests") {
			t.Error("expected false for non-file command")
		}
	})

	t.Run("compound command with cd inside is allowed", func(t *testing.T) {
		dir := setup(t)
		if !ShellCommandPathsAllowed("cd " + dir + "/subdir && ls -la") {
			t.Error("expected true for cd inside allowed dir")
		}
	})

	t.Run("cd outside allowed dir is denied", func(t *testing.T) {
		_ = setup(t)
		if ShellCommandPathsAllowed("cd /etc && cat passwd") {
			t.Error("expected false for cd outside allowed dir")
		}
	})

	t.Run("rm -rf .. is denied", func(t *testing.T) {
		_ = setup(t)
		if ShellCommandPathsAllowed("rm -rf ..") {
			t.Error("expected false for rm -rf .. (escapes allowed dir)")
		}
	})

	t.Run("one outside path denies whole command", func(t *testing.T) {
		dir := setup(t)
		if ShellCommandPathsAllowed("cat " + dir + "/a.txt && cat /etc/passwd") {
			t.Error("expected false when any path is outside allowed dir")
		}
	})

	t.Run("empty command is allowed", func(t *testing.T) {
		_ = setup(t)
		if !ShellCommandPathsAllowed("") {
			t.Error("expected true for empty command")
		}
	})
}

func TestShellPathsAllAllowed(t *testing.T) {
	t.Run("returns true when all paths are allowed", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		allowed := ShellPathsAllAllowed([]string{dir + "/file.txt"})
		if !allowed {
			t.Error("expected true for path in allowed dir")
		}
	})

	t.Run("returns false when any path is outside allowed", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)

		allowed := ShellPathsAllAllowed([]string{dir + "/file.txt", "/etc/passwd"})
		if allowed {
			t.Error("expected false when one path is outside allowed dir")
		}
	})

	t.Run("returns false for path outside allowed", func(t *testing.T) {
		_, _ = setupAccessTest(t)
		defer SetAccessController(nil)

		allowed := ShellPathsAllAllowed([]string{"/etc/shadow"})
		if allowed {
			t.Error("expected false for /etc/shadow")
		}
	})

	t.Run("returns true for empty paths", func(t *testing.T) {
		allowed := ShellPathsAllAllowed([]string{})
		if !allowed {
			t.Error("expected true for empty paths")
		}
	})

	t.Run("returns true when no controller", func(t *testing.T) {
		SetAccessController(nil)
		allowed := ShellPathsAllAllowed([]string{"/any/path"})
		if !allowed {
			t.Error("expected true with nil controller")
		}
	})
}
