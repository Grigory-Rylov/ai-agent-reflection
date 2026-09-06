package agent

import (
	"encoding/json"
	"testing"

	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func TestConvertHistoryToAPIMessages_ToolCallArgumentsAlwaysString(t *testing.T) {
	cases := []struct {
		name           string
		storedArgs     string
		wantInnerArgs  string
	}{
		{
			name:          "native quoted string arguments stay quoted",
			storedArgs:    `"{\"tail\":40,\"task_id\":\"bg-1\"}"`,
			wantInnerArgs: `{"tail":40,"task_id":"bg-1"}`,
		},
		{
			name:          "xml-converted object arguments become a string",
			storedArgs:    `{"limit":"60","path":"/tmp/a"}`,
			wantInnerArgs: `{"limit":"60","path":"/tmp/a"}`,
		},
		{
			name:          "empty arguments become an empty object string",
			storedArgs:    ``,
			wantInnerArgs: `{}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := sess.NewSession(sess.DefaultConfig())
			s.UpdateSystemPrompt("system")
			s.AddUserMessage("user")
			s.AddAssistantMessageWithToolCalls("", []sess.MsgToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: sess.MsgToolCallFunc{
					Name:      "file_read",
					Arguments: tc.storedArgs,
				},
			}})
			s.AddToolMessage("call-1", "file_read", "ok")

			api := (&agentImpl{}).convertHistoryToAPIMessages(s.GetHistory())

			var args json.RawMessage
			for _, m := range api {
				if len(m.ToolCalls) > 0 {
					args = m.ToolCalls[0].Function.Arguments
				}
			}
			if len(args) == 0 {
				t.Fatalf("expected tool call arguments in API message, got none")
			}
			if args[0] != '"' {
				t.Fatalf("tool_calls arguments must be a JSON string, got %s", string(args))
			}
			var inner string
			if err := json.Unmarshal(args, &inner); err != nil {
				t.Fatalf("arguments is not a JSON string: %v", err)
			}
			if inner != tc.wantInnerArgs {
				t.Errorf("arguments inner = %q, want %q", inner, tc.wantInnerArgs)
			}
		})
	}
}