package agent

import "testing"

func TestParseSSEEvent_ReasoningFieldVariants(t *testing.T) {
	tests := []struct {
		name        string
		data        string
		wantReason  string
		wantContent string
	}{
		{
			name:       "vllm 0.27 reasoning field",
			data:       `{"choices":[{"delta":{"reasoning":"need to think"},"finish_reason":null}]}`,
			wantReason: "need to think",
		},
		{
			name:        "legacy reasoning_content field",
			data:        `{"choices":[{"delta":{"reasoning_content":"think"},"finish_reason":null}]}`,
			wantReason:  "think",
		},
		{
			name:       "both fields present prefers reasoning_content",
			data:       `{"choices":[{"delta":{"reasoning":"alt","reasoning_content":"main"}}]}`,
			wantReason: "main",
		},
		{
			name:        "plain content only",
			data:        `{"choices":[{"delta":{"content":"hello"}}]}`,
			wantContent: "hello",
		},
	}

	parser := &agentImpl{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := parser.parseSSEEvent(tt.data)
			if event == nil || len(event.Choices) == 0 {
				t.Fatalf("expected one choice, got %+v", event)
			}
			delta := event.Choices[0].Delta
			if delta.ReasoningContent != tt.wantReason {
				t.Errorf("ReasoningContent = %q, want %q", delta.ReasoningContent, tt.wantReason)
			}
			if delta.Content != tt.wantContent {
				t.Errorf("Content = %q, want %q", delta.Content, tt.wantContent)
			}
		})
	}
}
