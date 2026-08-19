package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ToolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Index    int              `json:"index"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallResult struct {
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error"`
}


func MergeToolCalls(existing []ToolCall, delta []ToolCall) []ToolCall {
	for _, tc := range delta {
		found := false
		for i, exist := range existing {
			if exist.Index == tc.Index {
				found = true
				mergeToolCallDelta(&existing[i], tc)
				break
			}
		}
		if !found {
			existing = append(existing, tc)
		}
	}
	return existing
}

func mergeToolCallDelta(existing *ToolCall, delta ToolCall) {
	if delta.Function.Name != "" {
		existing.Function.Name = delta.Function.Name
	}
	if delta.ID != "" {
		existing.ID = delta.ID
	}
	if delta.Type != "" {
		existing.Type = delta.Type
	}
	if len(delta.Function.Arguments) > 0 {
		existing.Function.Arguments = mergeArguments(existing.Function.Arguments, delta.Function.Arguments)
	}
}


func mergeArguments(existing, delta json.RawMessage) json.RawMessage {
	var existingStr string
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &existingStr); err != nil {
			
			raw := string(existing)
			if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
				existingStr = raw[1 : len(raw)-1]
			} else {
				existingStr = raw
			}
		}
	}

	var deltaStr string
	if len(delta) > 0 {
		if err := json.Unmarshal(delta, &deltaStr); err != nil {
			raw := string(delta)
			if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
				deltaStr = raw[1 : len(raw)-1]
			} else {
				deltaStr = raw
			}
		}
	}

	combined := existingStr + deltaStr
	result, _ := json.Marshal(combined)
	return result
}


func ToolCallName(tc ToolCall) string {
	return tc.Function.Name
}


func ToolCallArgumentsStr(tc ToolCall) string {
	if len(tc.Function.Arguments) == 0 {
		return ""
	}
	
	var s string
	if err := json.Unmarshal(tc.Function.Arguments, &s); err != nil {
		
		raw := string(tc.Function.Arguments)
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
			return raw[1 : len(raw)-1]
		}
		return raw
	}
	return s
}

func parseToolCalls(rawMessage map[string]interface{}) ([]ToolCall, error) {
	toolCallsField, ok := rawMessage["tool_calls"]
	if !ok {
		return nil, nil
	}

	var toolCalls []ToolCall
	toolCallsBytes, err := json.Marshal(toolCallsField)
	if err != nil {
		return nil, fmt.Errorf("marshal tool_calls: %w", err)
	}

	if err := json.Unmarshal(toolCallsBytes, &toolCalls); err != nil {
		return nil, fmt.Errorf("unmarshal tool_calls: %w", err)
	}

	return toolCalls, nil
}


func parseToolArguments(tc ToolCall) (map[string]string, error) {
	argsStr := ToolCallArgumentsStr(tc)
	if argsStr == "" {
		return make(map[string]string), nil
	}

	var rawArgs map[string]interface{}
	if err := json.Unmarshal([]byte(argsStr), &rawArgs); err != nil {
		return nil, fmt.Errorf("parse tool arguments: %w", err)
	}

	args := make(map[string]string, len(rawArgs))
	for k, v := range rawArgs {
		normalized := k
		
		
		if _, hasSnake := rawArgs[toSnakeCase(k)]; !hasSnake && k != toSnakeCase(k) {
			normalized = toSnakeCase(k)
		}
		switch val := v.(type) {
		case string:
			args[normalized] = val
		case float64:
			args[normalized] = fmt.Sprintf("%v", val)
		case bool:
			args[normalized] = fmt.Sprintf("%v", val)
		default:
			if v != nil {
				data, err := json.Marshal(v)
				if err == nil {
					args[normalized] = string(data)
				} else {
					args[normalized] = fmt.Sprintf("%v", v)
				}
			}
		}
	}
	return args, nil
}


func toSnakeCase(s string) string {
	if s == "" {
		return s
	}
	var result strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				result.WriteByte('_')
			}
			result.WriteRune(r + 32)
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}
