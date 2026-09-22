package agentloop

import "testing"

func TestLooksLikePlaceholderTask(t *testing.T) {
	cases := []struct {
		name        string
		task        string
		placeholder bool
	}{
		{"empty", "", true},
		{"spaces only", "   ", true},
		{"placeholder word", "placeholder", true},
		{"placeholder uppercase", "PLACEHOLDER", true},
		{"single letter x", "x", true},
		{"todo", "todo", true},
		{"tbd", "TBD", true},
		{"ellipsis", "...", true},
		{"dash", "-", true},
		{"review work", "review work", false},
		{"short but real", "go build ./...", false},
		{"russian task", "пересобери бинарники", false},
		{"single long word", "рефакторинг", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikePlaceholderTask(tc.task); got != tc.placeholder {
				t.Errorf("looksLikePlaceholderTask(%q) = %v, want %v", tc.task, got, tc.placeholder)
			}
		})
	}
}

func TestSubAgentToolRejectsPlaceholderPrompt(t *testing.T) {
	tool := &SubAgentTool{MaxDepth: 3}
	result, err := tool.Execute(t.Context(), map[string]string{
		"subagent_type": "reviewer",
		"prompt":        "placeholder",
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Success {
		t.Fatalf("expected failure for placeholder prompt, got success: %+v", result)
	}
	if result.Error == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestSubAgentToolTargetsReviewAgent(t *testing.T) {
	tool := &SubAgentTool{}
	cases := []struct {
		args map[string]string
		want bool
	}{
		{map[string]string{"subagent_type": "reviewer"}, true},
		{map[string]string{"subagent_type": "worker"}, false},
		{map[string]string{"name": "reviewer"}, true},
		{map[string]string{}, false},
	}
	for i, tc := range cases {
		if got := tool.TargetsReviewAgent(tc.args); got != tc.want {
			t.Errorf("case %d: TargetsReviewAgent(%v) = %v, want %v", i, tc.args, got, tc.want)
		}
	}
}
