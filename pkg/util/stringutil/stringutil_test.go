package stringutil

import "testing"

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		suffix string
		want   string
	}{
		{
			name:   "no truncation needed",
			s:      "hello",
			maxLen: 10,
			suffix: "...",
			want:   "hello",
		},
		{
			name:   "exact length — no truncation",
			s:      "hello",
			maxLen: 5,
			suffix: "...",
			want:   "hello",
		},
		{
			name:   "truncate with suffix",
			s:      "hello world",
			maxLen: 5,
			suffix: "...",
			want:   "hello...",
		},
		{
			name:   "empty string",
			s:      "",
			maxLen: 3,
			suffix: "...",
			want:   "",
		},
		{
			name:   "zero maxLen — always truncate if non-empty",
			s:      "hi",
			maxLen: 0,
			suffix: "...",
			want:   "...",
		},
		{
			name:   "rune safe — Cyrillic",
			s:      "привет мир",
			maxLen: 6,
			suffix: "...",
			want:   "привет...",
		},
		{
			name:   "custom suffix",
			s:      "hello world",
			maxLen: 5,
			suffix: "[…]",
			want:   "hello[…]",
		},
		{
			name:   "empty suffix",
			s:      "hello world",
			maxLen: 5,
			suffix: "",
			want:   "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.s, tt.maxLen, tt.suffix)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d, %q) = %q, want %q", tt.s, tt.maxLen, tt.suffix, got, tt.want)
			}
		})
	}
}
