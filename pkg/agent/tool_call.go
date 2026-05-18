package agent

import (
	"encoding/json"
	"fmt"
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

// MergeToolCalls объединяет инкрементальные tool_calls из streaming дельт
// OpenAI streaming format:
//   chunk1: {"index":0,"id":"call_1","function":{"name":"file_write","arguments":""}}
//   chunk2: {"index":0,"function":{"arguments":"{\"path\":"}}
//   chunk3: {"index":0,"function":{"arguments":"\"test.txt\"}"}}
//
// arguments в каждом chunk — JSON-строка, а не сырой JSON-объект.
// Конкатенировать их нужно на уровне распарсенных строк, а не байт.
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

// mergeArguments конкатенирует два JSON-токена arguments на уровне строковых значений.
// Каждый токен — JSON-строка (например, "{\"path\":\"test.txt\"}"),
// нужно извлечь строку, сконкатенировать, и вернуть как JSON-строку.
func mergeArguments(existing, delta json.RawMessage) json.RawMessage {
	var existingStr string
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &existingStr); err != nil {
			// Если не удалось распарсить — используем сырые байты (без кавычек)
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

// ToolCallName возвращает имя инструмента из tool_call
func ToolCallName(tc ToolCall) string {
	return tc.Function.Name
}

// ToolCallArgumentsStr возвращает аргументы как распарсенную строку
func ToolCallArgumentsStr(tc ToolCall) string {
	if len(tc.Function.Arguments) == 0 {
		return ""
	}
	// Arguments — это JSON-строка, нужно извлечь значение
	var s string
	if err := json.Unmarshal(tc.Function.Arguments, &s); err != nil {
		// fallback: просто убираем кавычки
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

func getToolCallContent(rawMessage map[string]interface{}) string {
	if content, ok := rawMessage["content"].(string); ok {
		return content
	}
	return ""
}

func getFinishReason(rawMessage map[string]interface{}) string {
	if finishReason, ok := rawMessage["finish_reason"].(string); ok {
		return finishReason
	}
	return ""
}

func formatToolMessage(toolCallID, toolName, result string) map[string]interface{} {
	return map[string]interface{}{
		"role":         "tool",
		"tool_call_id": toolCallID,
		"name":         toolName,
		"content":      result,
	}
}

func formatToolErrorMessage(toolCallID, toolName, errorMsg string) map[string]interface{} {
	return map[string]interface{}{
		"role":         "tool",
		"tool_call_id": toolCallID,
		"name":         toolName,
		"content":      fmt.Sprintf("Error: %s", errorMsg),
	}
}

func isToolCallResponse(rawMessage map[string]interface{}) bool {
	_, hasToolCalls := rawMessage["tool_calls"]
	return hasToolCalls
}

// parseToolArguments распарсивает JSON-аргументы tool_call в map[string]string
func parseToolArguments(tc ToolCall) (map[string]string, error) {
	argsStr := ToolCallArgumentsStr(tc)
	if argsStr == "" {
		return make(map[string]string), nil
	}

	var args map[string]string
	if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
		return nil, fmt.Errorf("parse tool arguments: %w", err)
	}
	return args, nil
}
