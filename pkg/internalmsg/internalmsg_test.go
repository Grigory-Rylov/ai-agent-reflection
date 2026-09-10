package internalmsg

import (
	"strings"
	"testing"
)

func TestIsInternal(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "label at start", content: Label + "\n- decided X", want: true},
		{name: "label with leading whitespace", content: "   " + Label + "\n- decided X", want: true},
		{name: "label only", content: Label, want: true},
		{name: "plain answer", content: "The result is 4.", want: false},
		{name: "empty", content: "", want: false},
		{name: "label in the middle is not internal", content: "answer " + Label, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsInternal(tc.content); got != tc.want {
				t.Errorf("IsInternal(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

func TestStrip(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "no summary label is untouched", input: "Plain answer with * bullet\n\n- and a dash line", want: "Plain answer with * bullet\n\n- and a dash line"},
		{name: "trailing summary block removed", input: "The result is 4.\n\n" + Label + "\n- first decision\n- second decision", want: "The result is 4."},
		{name: "leading summary block removed", input: Label + "\n- first decision\n\nThe result is 4.", want: "The result is 4."},
		{name: "summary block stops at real answer line", input: Label + "\n- first decision\n\nThe result is 4.\n- bullet of the real answer", want: "The result is 4.\n- bullet of the real answer"},
		{name: "label only removed leaves nothing", input: Label, want: ""},
		{name: "inline label keeps preceding text", input: "answer " + Label + "\n- decision", want: "answer"},
		{name: "asterisk and dot bullets removed", input: Label + "\n* one\n• two\nafter", want: "after"},
		{name: "empty lines inside body removed only when body resumes", input: Label + "\n- one\n\n\n- two\n\nreal", want: "real"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Strip(tc.input)
			if got != tc.want {
				t.Errorf("Strip(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if strings.Contains(got, Label) {
				t.Errorf("result still contains summary label: %q", got)
			}
		})
	}
}
