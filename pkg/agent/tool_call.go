package agent

import (
	"encoding/json"
	"fmt"
)

// ============================================================
// Tool Call — обработка вызовов инструментов
// ============================================================

// ToolCall представляет вызов инструмента от AI
type ToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Name     string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolCallResult представляет результат выполнения инструмента
type ToolCallResult struct {
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error"`
}

// parseToolCalls парсит tool_calls из ответа AI
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

// getToolCallContent извлекает content из rawMessage (если есть)
func getToolCallContent(rawMessage map[string]interface{}) string {
	if content, ok := rawMessage["content"].(string); ok {
		return content
	}
	return ""
}

// getFinishReason извлекает finish_reason из rawMessage
func getFinishReason(rawMessage map[string]interface{}) string {
	if finishReason, ok := rawMessage["finish_reason"].(string); ok {
		return finishReason
	}
	return ""
}

// formatToolMessage формирует сообщение с результатом выполнения инструмента
func formatToolMessage(toolCallID, toolName, result string) map[string]interface{} {
	return map[string]interface{}{
		"role":       "tool",
		"tool_call_id": toolCallID,
		"name":       toolName,
		"content":    result,
	}
}

// formatToolErrorMessage формирует сообщение об ошибке выполнения инструмента
func formatToolErrorMessage(toolCallID, toolName, errorMsg string) map[string]interface{} {
	return map[string]interface{}{
		"role":       "tool",
		"tool_call_id": toolCallID,
		"name":       toolName,
		"content":    fmt.Sprintf("Error: %s", errorMsg),
	}
}

// isToolCallResponse проверяет, содержит ли ответ tool_calls
func isToolCallResponse(rawMessage map[string]interface{}) bool {
	_, hasToolCalls := rawMessage["tool_calls"]
	return hasToolCalls
}
