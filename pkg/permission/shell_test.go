package permission

import (
	"reflect"
	"sort"
	"testing"
)

func TestScanCommandPatterns(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		patterns []string
		always   []string
	}{
		{
			name:     "simple command",
			command:  "echo hello",
			patterns: []string{"echo hello"},
			always:   []string{"echo *"},
		},
		{
			name:     "multiple commands with and",
			command:  "echo foo && echo bar",
			patterns: []string{"echo foo", "echo bar"},
			always:   []string{"echo *"},
		},
		{
			name:     "multiple commands with semicolon",
			command:  "ls -la; cat file.txt",
			patterns: []string{"ls -la", "cat file.txt"},
			always:   []string{"ls *", "cat *"},
		},
		{
			name:     "multiple commands with or and pipe",
			command:  "true || false | cat",
			patterns: []string{"true", "false", "cat"},
			always:   []string{"true *", "false *", "cat *"},
		},
		{
			name:     "cd command is not a pattern",
			command:  "cd .",
			patterns: nil,
			always:   nil,
		},
		{
			name:     "cd with other command",
			command:  "cd ../ && pwd",
			patterns: []string{"pwd"},
			always:   []string{"pwd *"},
		},
		{
			name:     "git uses two token prefix",
			command:  "git log --oneline -5",
			patterns: []string{"git log --oneline -5"},
			always:   []string{"git log *"},
		},
		{
			name:     "cat always pattern is single token",
			command:  "ls -la",
			patterns: []string{"ls -la"},
			always:   []string{"ls *"},
		},
		{
			name:     "redirect kept in pattern",
			command:  "echo test > output.txt",
			patterns: []string{"echo test > output.txt"},
			always:   []string{"echo *"},
		},
		{
			name:     "rm with flags",
			command:  "rm -rf /tmp/foo",
			patterns: []string{"rm -rf /tmp/foo"},
			always:   []string{"rm *"},
		},
		{
			name:     "nested command substitution",
			command:  `echo $(cat "file")`,
			patterns: []string{`echo $(cat "file")`, `cat "file"`},
			always:   []string{"echo *", "cat *"},
		},
		{
			name:     "npm run three tokens",
			command:  "npm run dev -- --port 3000",
			patterns: []string{"npm run dev -- --port 3000"},
			always:   []string{"npm run dev *"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scan := ScanCommand(tt.command)
			if !reflect.DeepEqual(scan.Patterns, tt.patterns) {
				t.Errorf("ScanCommand(%q).Patterns = %v, want %v", tt.command, scan.Patterns, tt.patterns)
			}
			if !reflect.DeepEqual(scan.Always, tt.always) {
				t.Errorf("ScanCommand(%q).Always = %v, want %v", tt.command, scan.Always, tt.always)
			}
		})
	}
}

func TestScanCommandQuotes(t *testing.T) {
	scan := ScanCommand(`echo "hello world" && cat 'it''s'`)
	if !reflect.DeepEqual(scan.Patterns, []string{`echo "hello world"`, `cat 'it''s'`}) {
		t.Errorf("patterns = %v", scan.Patterns)
	}
	if !reflect.DeepEqual(scan.Always, []string{"echo *", "cat *"}) {
		t.Errorf("always = %v", scan.Always)
	}
}

func TestScanCommandParenthesized(t *testing.T) {
	scan := ScanCommand("(echo one && echo two); echo three")
	want := []string{"(echo one && echo two)", "echo three"}
	if !reflect.DeepEqual(scan.Patterns, want) {
		t.Errorf("patterns = %v, want %v", scan.Patterns, want)
	}
}

func TestScanCommandNoDuplicates(t *testing.T) {
	scan := ScanCommand("echo hi && echo hi")
	if !reflect.DeepEqual(scan.Patterns, []string{"echo hi"}) {
		t.Errorf("patterns = %v, want [echo hi]", scan.Patterns)
	}
	if !reflect.DeepEqual(scan.Always, []string{"echo *"}) {
		t.Errorf("always = %v, want [echo *]", scan.Always)
	}
}

func TestScanCommandEmpty(t *testing.T) {
	scan := ScanCommand("")
	if len(scan.Patterns) != 0 || len(scan.Always) != 0 {
		t.Errorf("expected no patterns for empty command, got %v / %v", scan.Patterns, scan.Always)
	}
}

func TestScanCommandSortedStable(t *testing.T) {
	scan := ScanCommand("b; a; c")
	want := []string{"b", "a", "c"}
	if !reflect.DeepEqual(scan.Patterns, want) {
		t.Errorf("patterns = %v, want %v", scan.Patterns, want)
	}
	got := append([]string(nil), scan.Always...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"a *", "b *", "c *"}) {
		t.Errorf("always = %v", scan.Always)
	}
}
