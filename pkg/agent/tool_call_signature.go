package agent

import (
	"encoding/json"
)

func toolCallSignature(tc ToolCall) string {
	name := tc.Function.Name
	args := ToolCallArgumentsStr(tc)

	var normalized interface{}
	if json.Unmarshal([]byte(args), &normalized) == nil {
		if canonical, err := json.Marshal(normalized); err == nil {
			args = string(canonical)
		}
	}

	return name + ":" + args
}

func xmlToolCallSignature(tc XMLToolCall) string {
	argsJSON, _ := json.Marshal(tc.Args)
	return tc.Name + ":" + string(argsJSON)
}
