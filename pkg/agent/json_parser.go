package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ParseJSONToolCalls(input string) XMLParseResult {
	var result XMLParseResult
	var content strings.Builder

	i := 0
	for i < len(input) {
		if input[i] != '{' {
			content.WriteByte(input[i])
			i++
			continue
		}

		dec := json.NewDecoder(strings.NewReader(input[i:]))
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			content.WriteByte(input[i])
			i++
			continue
		}

		var candidate struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(raw, &candidate); err != nil || candidate.Name == "" {
			content.WriteByte(input[i])
			i++
			continue
		}

		args := make(map[string]string)
		for k, v := range candidate.Arguments {
			args[k] = fmt.Sprintf("%v", v)
		}

		result.ToolCalls = append(result.ToolCalls, XMLToolCall{
			Name: candidate.Name,
			Args: args,
		})

		i += int(dec.InputOffset())
	}

	result.Content = content.String()
	return result
}
