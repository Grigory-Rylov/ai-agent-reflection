package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ParseJSONToolCalls(input string) XMLParseResult {
	var result XMLParseResult
	var content strings.Builder

	inCodeBlock := false
	i := 0
	for i < len(input) {
		if !inCodeBlock && hasTripleBacktickAt(input, i) {
			inCodeBlock = true
			content.WriteString("```")
			i += 3
			continue
		}
		if inCodeBlock && hasTripleBacktickAt(input, i) {
			inCodeBlock = false
			content.WriteString("```")
			i += 3
			continue
		}

		if inCodeBlock || input[i] != '{' {
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

func hasTripleBacktickAt(input string, i int) bool {
	if i+3 > len(input) {
		return false
	}
	if input[i] != '`' || input[i+1] != '`' || input[i+2] != '`' {
		return false
	}
	if i > 0 && input[i-1] != '\n' {
		prev := i
		for prev > 0 && input[prev-1] == ' ' {
			prev--
		}
		if prev > 0 && input[prev-1] != '\n' {
			return false
		}
	}
	return true
}
