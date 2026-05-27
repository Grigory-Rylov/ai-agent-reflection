package agent

import (
	"encoding/json"
	"fmt"
)

func convertXMLToolCalls(xmlCalls []XMLToolCall) []ToolCall {
	toolCalls := make([]ToolCall, len(xmlCalls))
	for i, xc := range xmlCalls {
		argsJSON, _ := json.Marshal(xc.Args)
		toolCalls[i] = ToolCall{
			ID:    fmt.Sprintf("xml_call_%d", i),
			Type:  "function",
			Index: i,
			Function: ToolCallFunction{
				Name:      xc.Name,
				Arguments: json.RawMessage(argsJSON),
			},
		}
	}
	return toolCalls
}
