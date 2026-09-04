package permission

import (
	"os"
	"testing"
)

func TestCheckCriticalDestructive(t *testing.T) {
	home := os.Getenv("HOME")
	cases := []struct {
		name    string
		command string
	}{
		{"rm root", "rm -rf /"},
		{"rm home", "rm -rf " + home},
		{"rm tilde", "rm -rf ~"},
		{"rm tilde subpath", "rm -rf ~/projects"},
		{"rm -fr root", "rm -fr /"},
		{"mkfs ext4", "mkfs.ext4 /dev/sda1"},
		{"mkfs bare", "mkfs /dev/sda1"},
		{"dd device", "dd if=/dev/zero of=/dev/sda bs=1M"},
		{"fork bomb", ":(){ :|:& };:"},
		{"shred device", "shred -vf /dev/sda1"},
		{"wipefs device", "wipefs -a /dev/sda1"},
		{"chmod 777 root", "chmod -R 777 /"},
		{"chmod 777 home", "chmod -R 777 " + home},
		{"force push", "git push --force origin main"},
		{"force push -f", "git push -f origin main"},
		{"force push lease", "git push --force-with-lease origin main"},
		{"systemctl stop", "systemctl stop agent.service"},
		{"systemctl restart", "systemctl --user restart agent"},
		{"service stop", "service ssh stop"},
		{"compound critical", "ls && rm -rf /"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched, reasons := CheckCritical(tc.command)
			if !matched {
				t.Fatalf("expected critical match for %q, got none", tc.command)
			}
			if len(reasons) == 0 {
				t.Errorf("expected non-empty reasons for %q", tc.command)
			}
		})
	}
}

func TestCheckCriticalBenign(t *testing.T) {
	home := os.Getenv("HOME")
	cases := []struct {
		name    string
		command string
	}{
		{"rm relative", "rm -rf ./build"},
		{"rm tmp", "rm -rf /tmp/test"},
		{"rm single file", "rm file.txt"},
		{"rm -r project", "rm -r ./out"},
		{"git push normal", "git push origin main"},
		{"dd to file", "dd if=/dev/zero of=/tmp/disk.img bs=1M count=1"},
		{"chmod 755", "chmod -R 755 ./app"},
		{"chmod 777 relative", "chmod -R 777 ./tmpdir"},
		{"ls", "ls -la"},
		{"cd only", "cd /tmp && ls"},
		{"shred file", "shred -vf /tmp/secret.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matched, _ := CheckCritical(tc.command)
			if matched {
				t.Errorf("expected no critical match for %q", tc.command)
			}
		})
	}
	_ = home
}