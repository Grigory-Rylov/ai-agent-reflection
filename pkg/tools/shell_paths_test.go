package tools

import (
	"os"
	"path/filepath"
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

func TestShellCommandHasFilePaths(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{"adb devices has no paths", "adb -s emulator-5554 devices -l", false},
		{"echo has no paths", "echo hello", false},
		{"ping has no paths", "ping -c 2 192.168.1.192", false},
		{"pip install has no paths", "pip install requests", false},
		{"cd has no paths", "cd /tmp", true},
		{"cat absolute path", "cat /etc/passwd", true},
		{"ls without paths is pathless", "ls -la", false},
		{"git status is pathless", "git status", false},
		{"rm outside path", "rm -rf /tmp/build", true},
		{"rm -rf .. has path", "rm -rf ..", true},
		{"redirection with spaces", "echo hi > /etc/file", true},
		{"redirection appended", "echo hi >> /tmp/log.txt", true},
		{"stderr redirection", "cmd 2> /tmp/err.log", true},
		{"redirection no space", "echo hi>/etc/file", true},
		{"fd redirect 2>&1 is not a file", "cmd 2>&1", false},
		{"curl output flag path", "curl http://x -o /tmp/out.bin", true},
		{"nested subcommand path", "echo $(cat /etc/passwd)", true},
		{"package name com.avito.android is not a path", "adb -s emulator-5554 shell am force-stop com.avito.android", false},
		{"package name with pidof", "adb -s emulator-5554 shell pidof com.avito.android", false},
		{"version 1.2.3 is not a path", "echo v1.2.3", false},
		{"env prefix binary with tilde", "LD_LIBRARY_PATH=~/Android/Sdk/emulator/lib64 nohup ~/Android/Sdk/emulator/emulator -avd MyAVD", true},
		{"env prefix plus redirection", "FOO=bar cmd > /tmp/out.log 2>&1", true},
		{"adb shell device path is not host", "adb -s emulator-5554 shell uiautomator dump /data/local/tmp/ui.xml 2>&1", false},
		{"adb shell chain device path excluded", "adb -s emulator-5554 shell uiautomator dump /data/local/tmp/ui.xml 2>&1 && cat /data/local/tmp/ui.xml && head -30", false},
		{"adb push host path still checked", "adb push /etc/passwd /sdcard/", true},
		{"ssh remote command is not host", "ssh user@host 'rm -rf /var/tmp/x'", false},
		{"scp remote target is not host", "scp user@host:/data/ui.xml ./localfile.txt", true},
		{"adb shell redirection to host log", "adb shell screencap /sdcard/x.png > /tmp/screen.png", true},
		{"empty command", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShellCommandHasFilePaths(tt.command); got != tt.want {
				t.Errorf("ShellCommandHasFilePaths(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestShellCommandPathsAllowedEnvAndRedirect(t *testing.T) {
	t.Run("env prefix command inside allowed dir is allowed", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		sub := filepath.Join(dir, "sub")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := "LD_LIBRARY_PATH=" + sub + "/lib64 nohup " + sub + "/app -flag value > " + dir + "/out.log 2>&1"
		if !ShellCommandPathsAllowed(cmd) {
			t.Error("expected true when all paths (binary, env, redirect) are inside allowed dir")
		}
	})

	t.Run("env prefix binary outside allowed dir is denied", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		cmd := "LD_LIBRARY_PATH=~/Android/Sdk/emulator/lib64 nohup ~/Android/Sdk/emulator/emulator -avd MyAVD"
		if ShellCommandPathsAllowed(cmd) {
			t.Error("expected false when binary path is outside allowed dir")
		}
	})

	t.Run("redirection inside allowed dir is allowed", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		if !ShellCommandPathsAllowed("echo hi > " + dir + "/out.log") {
			t.Error("expected true for redirect inside allowed dir")
		}
	})

	t.Run("redirection outside allowed dir is denied", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		if ShellCommandPathsAllowed("echo hi > /etc/file") {
			t.Error("expected false for redirect outside allowed dir")
		}
	})

	t.Run("dotted package name is not a file op", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		if ShellCommandPathsAllowed("adb -s emulator-5554 shell am force-stop com.avito.android") {
			t.Error("expected false for non-file command without explicit paths")
		}
	})
}

func TestShellCommandPathsAllowedDeviceContext(t *testing.T) {
	t.Run("adb shell device paths are not host operations", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		cmd := "adb -s emulator-5554 shell uiautomator dump /data/local/tmp/ui.xml 2>&1 && cat /data/local/tmp/ui.xml && head -30"
		if ShellCommandHasFilePaths(cmd) {
			t.Error("expected no host file operations: device path and chained cat of device file are device-side")
		}
	})

	t.Run("adb push host source outside allowed dir is denied", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		if ShellCommandPathsAllowed("adb push /etc/passwd /sdcard/") {
			t.Error("expected false: host source of push is outside allowed dir")
		}
	})

	t.Run("adb push host source inside allowed dir is allowed", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		cmd := "adb push " + dir + "/file.apk /sdcard/"
		if !ShellCommandPathsAllowed(cmd) {
			t.Error("expected true: host source of push is inside allowed dir")
		}
	})

	t.Run("adb shell redirection to host outside is denied", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		if ShellCommandPathsAllowed("adb shell screencap /sdcard/x.png > /etc/out.png") {
			t.Error("expected false: redirection target is host path outside allowed dir")
		}
	})
}

func TestShellCommandFilesystemSafe(t *testing.T) {
	t.Run("adb shell + pull + head chain is safe", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		prevWD := WorkingDir
		t.Cleanup(func() { SetWorkingDir(prevWD) })
		SetWorkingDir(dir)
		cmd := "adb -s emulator-5554 shell uiautomator dump /data/local/tmp/ui.xml && sleep 1 && adb -s emulator-5554 pull /data/local/tmp/ui.xml ./ui_test.xml 2>&1 && head -30 ui_test.xml"
		if !ShellCommandFilesystemSafe(cmd) {
			t.Error("expected true: device paths and cwd host files are safe")
		}
	})

	t.Run("host write outside allowed dir is not safe", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		if ShellCommandFilesystemSafe("echo hi > /etc/file") {
			t.Error("expected false: host write outside allowed dir")
		}
	})

	t.Run("read outside allowed dir is safe (read-only cannot mutate)", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		if !ShellCommandFilesystemSafe("cat /etc/passwd") {
			t.Error("expected true: read-only cat cannot mutate the filesystem")
		}
	})

	t.Run("adb device paths are safe", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		if !ShellCommandFilesystemSafe("adb shell rm -rf /data/local/tmp/x") {
			t.Error("expected true: device paths do not touch host")
		}
	})

	t.Run("redirect to /dev/null is safe", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		if !ShellCommandFilesystemSafe("pkill -f qemu-system-aarch64 2>/dev/null") {
			t.Error("expected true: /dev/null is a discard path, not a host file op")
		}
	})

	t.Run("emulator command with /dev/null and allowed binary is safe", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		emu := filepath.Join(dir, "emulator")
		if err := os.MkdirAll(emu, 0o755); err != nil {
			t.Fatal(err)
		}
		prevWD := WorkingDir
		t.Cleanup(func() { SetWorkingDir(prevWD) })
		SetWorkingDir(dir)
		cmd := "pkill -f qemu-system-aarch64 2>/dev/null && sleep 3 && LD_LIBRARY_PATH=" + emu + "/lib64 nohup " + emu + "/emulator -avd MyAVD > " + filepath.Join(dir, "emulator.log") + " 2>&1 &"
		if !ShellCommandFilesystemSafe(cmd) {
			t.Error("expected true: /dev/null discard and binary inside allowed dir")
		}
	})

	t.Run("emulator binary outside allowed dir is not safe", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		cmd := "nohup ~/Android/Sdk/emulator/emulator -avd MyAVD > " + filepath.Join(dir, "emulator.log") + " 2>&1 &"
		if ShellCommandFilesystemSafe(cmd) {
			t.Error("expected false: emulator binary path is outside allowed dir")
		}
	})

	t.Run("nohup with interpreter and script inside allowed dir is safe", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		script := filepath.Join(dir, "run.sh")
		if err := os.WriteFile(script, []byte("#!/bin/bash\nsleep 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := "nohup /bin/bash " + script + " > " + filepath.Join(dir, "run.log") + " 2>&1 &"
		if !ShellCommandFilesystemSafe(cmd) {
			t.Error("expected true: /bin/bash is the interpreter, script and log in allowed dir")
		}
	})

	t.Run("env wrapper with interpreter is safe", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		script := filepath.Join(dir, "run.sh")
		cmd := "env FOO=1 /bin/bash " + script
		if !ShellCommandFilesystemSafe(cmd) {
			t.Error("expected true: interpreter after env is not a file op")
		}
	})

	t.Run("time wrapper with user binary outside allowed dir is not safe", func(t *testing.T) {
		dir, _ := setupAccessTest(t)
		defer cleanupAccessTest(t, dir)
		cmd := "time /home/orangepi/Android/Sdk/emulator/emulator -avd MyAVD > " + filepath.Join(dir, "emulator.log") + " 2>&1"
		if ShellCommandFilesystemSafe(cmd) {
			t.Error("expected false: wrapped binary path is outside allowed dir")
		}
	})

	t.Run("empty command is safe", func(t *testing.T) {
		SetAccessController(nil)
		if !ShellCommandFilesystemSafe("") {
			t.Error("expected true for empty command")
		}
	})
}
