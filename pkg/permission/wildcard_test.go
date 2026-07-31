package permission

import "testing"

func TestMatch(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		pattern string
		want    bool
	}{
		{"exact match", "cat", "cat", true},
		{"wildcard matches anything", "cat", "*", true},
		{"wildcard matches multi token", "cat file.txt", "*", true},
		{"empty string matches wildcard", "", "*", true},
		{"exact mismatch", "cat", "dog", false},
		{"prefix wildcard", "bash", "ba*", true},
		{"prefix wildcard mismatch", "cat", "ba*", false},
		{"suffix wildcard", "file.txt", "*.txt", true},
		{"suffix wildcard mismatch", "file.go", "*.txt", false},
		{"trailing space wildcard matches bare command", "cat", "cat *", true},
		{"trailing space wildcard matches with arg", "cat file.txt", "cat *", true},
		{"trailing space wildcard matches multi args", "cat a b c", "cat *", true},
		{"trailing space wildcard no match", "dog", "cat *", false},
		{"permission glob", "mcp_server_tool", "mcp_*", true},
		{"permission glob mismatch", "bash", "mcp_*", false},
		{"question mark", "cat", "ca?", true},
		{"question mark mismatch length", "catt", "ca?", false},
		{"dollar env not expanded", "git status", "git $*", false},
		{"regex specials are escaped", "a.b", "a.b", true},
		{"regex specials no match", "axb", "a.b", false},
		{"backslash normalized", `a\b`, "a/b", true},
		{"multi wildcard in middle", "src/components/Button.tsx", "src/*", true},
		{"glob crosses slashes", "src/components/Button.tsx", "src/*", true},
		{"path pattern with dirs", "src/components/Button.tsx", "src/components/*", true},
		{"relative glob no match", "other/foo", "src/*", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Match(tt.input, tt.pattern); got != tt.want {
				t.Errorf("Match(%q, %q) = %v, want %v", tt.input, tt.pattern, got, tt.want)
			}
		})
	}
}
