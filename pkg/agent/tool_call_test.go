package agent

import (
	"encoding/json"
	"testing"
)

func toolCallWithArgs(name, args string) ToolCall {
	quoted, _ := json.Marshal(args)
	return ToolCall{
		ID:       "id1",
		Type:     "function",
		Function: ToolCallFunction{Name: name, Arguments: quoted},
	}
}

func TestUnparseableToolCallName(t *testing.T) {
	tests := []struct {
		name      string
		toolCalls []ToolCall
		want      string
	}{
		{
			name:      "truncated json arguments",
			toolCalls: []ToolCall{toolCallWithArgs("task", `{"subagent_type":"lead","prompt":"build a `)},
			want:      "task",
		},
		{
			name:      "valid json arguments",
			toolCalls: []ToolCall{toolCallWithArgs("task", `{"subagent_type":"lead","prompt":"done"}`)},
			want:      "",
		},
		{
			name:      "empty arguments is not unparseable",
			toolCalls: []ToolCall{toolCallWithArgs("time_get", "")},
			want:      "",
		},
		{
			name:      "json that is not an object",
			toolCalls: []ToolCall{toolCallWithArgs("task", `"just a string"`)},
			want:      "task",
		},
		{
			name:      "no tool calls",
			toolCalls: nil,
			want:      "",
		},
		{
			name: "returns first unparseable among several",
			toolCalls: []ToolCall{
				toolCallWithArgs("time_get", `{"tz":"UTC"}`),
				toolCallWithArgs("task", `{"subagent_type":"qa","prompt":"ver`),
			},
			want: "task",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unparseableToolCallName(tt.toolCalls)
			if got != tt.want {
				t.Errorf("unparseableToolCallName = %q, want %q", got, tt.want)
			}
			if hasUnparseableToolCall(tt.toolCalls) != (tt.want != "") {
				t.Errorf("hasUnparseableToolCall disagrees with unparseableToolCallName")
			}
		})
	}
}
