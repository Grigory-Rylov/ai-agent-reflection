package permission

import (
	"strings"
	"testing"
)

func TestSplitCommandsHeredocStaysOneCommand(t *testing.T) {
	cmd := "git commit -F - <<'EOF'\nfeat(x): title\nbody line one\n- bullet two\nEOF"

	got := SplitCommands(cmd)

	want := 1
	if len(got) != want {
		t.Fatalf("expected %d command, got %d: %#v", want, len(got), got)
	}

	if !strings.Contains(got[0], "-F -") {
		t.Errorf("first command lost the actual program: %q", got[0])
	}
	if !strings.Contains(got[0], "EOF") {
		t.Errorf("first command missing heredoc terminator: %q", got[0])
	}

	for _, fragment := range []string{"title", "bullet two"} {
		for _, c := range got {
			if c == fragment {
				t.Errorf("heredoc body leaked as separate command: %q", c)
			}
		}
	}
}

func TestSplitCommandsHeredocThenChained(t *testing.T) {
	cmd := "echo hi <<'E'\nline1\nE\n&& ls -la"

	got := SplitCommands(cmd)

	foundEcho := false
	for _, c := range got {
		if strings.HasPrefix(c, "echo") {
			foundEcho = true
			if !strings.Contains(c, "line1") {
				t.Errorf("echo command missing heredoc body: %q", c)
			}
		}
	}
	if !foundEcho {
		t.Fatalf("did not find the echo command in %#v", got)
	}

	hasLS := false
	for _, c := range got {
		if strings.TrimLeft(c, "& ") == "ls -la" || c == "ls -la" {
			hasLS = true
		}
	}
	if !hasLS {
		t.Errorf("expected trailing 'ls -la' as its own segment, got %#v", got)
	}
}

func TestSplitCommandsUnquotedHeredocDelimiter(t *testing.T) {
	cmd := "cat > /tmp/out <<EOF\nhello world\nEOF"

	got := SplitCommands(cmd)
	if len(got) != 1 {
		t.Fatalf("expected 1 command, got %d: %#v", len(got), got)
	}
	if !strings.Contains(got[0], "hello world") {
		t.Errorf("missing heredoc body: %q", got[0])
	}
}
