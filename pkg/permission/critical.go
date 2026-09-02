package permission

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type CriticalPattern struct {
	Name   string
	Reason string
}

var CriticalPatterns = []CriticalPattern{
	{Name: "rm-recursive-root", Reason: "rm -rf of root or home directory"},
	{Name: "mkfs", Reason: "filesystem format"},
	{Name: "dd-device", Reason: "raw write to a block device"},
	{Name: "fork-bomb", Reason: "fork bomb"},
	{Name: "shred-device", Reason: "shred of a block device"},
	{Name: "wipefs-device", Reason: "wipefs on a block device"},
	{Name: "chmod-777-root", Reason: "recursive chmod 777 of root or home"},
	{Name: "force-push", Reason: "force push to remote"},
	{Name: "systemctl", Reason: "system service control"},
}

var deviceTargetRe = regexp.MustCompile(`^(/dev/|/proc/|/sys/)`)
var forkBombRe = regexp.MustCompile(`:\s*\(\s*\)\s*\{[^}]*\|[^}]*&`)

func CheckCritical(command string) (bool, []string) {
	seen := map[string]bool{}
	if forkBombRe.MatchString(command) {
		seen["fork-bomb"] = true
	}
	for _, source := range SplitCommands(command) {
		tokens := Tokenize(source)
		if len(tokens) == 0 {
			continue
		}
		cmd := filepath.Base(tokens[0])
		if cwdCommands[cmd] {
			continue
		}
		name := matchCritical(cmd, tokens[1:])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		reasons := []string{}
		for _, p := range CriticalPatterns {
			if p.Name == name {
				reasons = append(reasons, p.Reason)
				break
			}
		}
		if len(reasons) == 0 {
			reasons = append(reasons, name)
		}
	}
	return len(seen) > 0, flattenReasons(seen)
}

func flattenReasons(seen map[string]bool) []string {
	reasons := []string{}
	for _, p := range CriticalPatterns {
		if seen[p.Name] {
			reasons = append(reasons, p.Reason)
		}
	}
	return reasons
}

func matchCritical(cmd string, args []string) string {
	switch cmd {
	case "rm":
		if hasFlag(args, "-rf", "-fr", "-r", "-R") && hasDestructiveTarget(args) {
			return "rm-recursive-root"
		}
	case "mkfs", "mkfs.ext2", "mkfs.ext3", "mkfs.ext4", "mkfs.xfs", "mkfs.btrfs", "mkfs.vfat", "mkfs.ntfs":
		return "mkfs"
	case "dd":
		if hasDeviceOf(args) {
			return "dd-device"
		}
	case ":":
		if isForkBomb(args) {
			return "fork-bomb"
		}
	case "shred":
		for _, a := range args {
			if deviceTargetRe.MatchString(a) {
				return "shred-device"
			}
		}
	case "wipefs":
		for _, a := range args {
			if deviceTargetRe.MatchString(a) {
				return "wipefs-device"
			}
		}
	case "chmod":
		if hasRecursiveFlag(args) && hasMode777(args) && hasDestructiveTarget(args) {
			return "chmod-777-root"
		}
	case "git":
		if isForcePush(args) {
			return "force-push"
		}
	case "systemctl", "service":
		return "systemctl"
	}
	return ""
}

func hasFlag(args []string, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f {
				return true
			}
		}
	}
	return false
}

func hasRecursiveFlag(args []string) bool {
	for _, a := range args {
		if a == "-R" || a == "-r" || strings.HasPrefix(a, "-R") || strings.HasPrefix(a, "-r") {
			return true
		}
	}
	return false
}

func hasMode777(args []string) bool {
	for _, a := range args {
		if a == "777" || a == "-R777" {
			return true
		}
	}
	return false
}

func hasDestructiveTarget(args []string) bool {
	home := expandHome()
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if isDestructivePath(a, home) {
			return true
		}
	}
	return false
}

func isDestructivePath(path, home string) bool {
	if path == "/" || path == "~" || strings.HasPrefix(path, "~/") {
		return true
	}
	if home != "" && (path == home || strings.HasPrefix(path, home+"/")) {
		return true
	}
	return false
}

func expandHome() string {
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return home
}

func hasDeviceOf(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "of=") {
			target := a[3:]
			if deviceTargetRe.MatchString(target) {
				return true
			}
		}
	}
	return false
}

func isForkBomb(args []string) bool {
	return len(args) >= 3 && strings.Contains(strings.Join(args, " "), "|") && strings.Contains(strings.Join(args, " "), "&")
}

func isForcePush(args []string) bool {
	seenPush := false
	force := false
	for _, a := range args {
		if a == "push" {
			seenPush = true
			continue
		}
		if seenPush && (a == "--force" || a == "-f" || a == "--force-with-lease") {
			force = true
		}
	}
	return seenPush && force
}