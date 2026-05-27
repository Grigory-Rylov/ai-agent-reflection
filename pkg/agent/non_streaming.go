package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (a *agentImpl) getToolCallsFromResponse(ctx context.Context, messages []Message, toolsSchema []map[string]interface{}) ([]ToolCall, error) {
	reqBody := a.buildNonStreamingRequestJSON(messages, toolsSchema)
	req, err := a.createNonStreamingRequest(reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	rawResponse, err := a.decodeResponse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return a.extractToolCallsFromResponse(rawResponse)
}

func (a *agentImpl) buildNonStreamingRequestJSON(messages []Message, toolsSchema []map[string]interface{}) []byte {
	reqBody := a.buildBaseRequestJSON(a.config.Model, messages, false)

	if len(toolsSchema) > 0 {
		reqBody["tools"] = toolsSchema
	}

	jsonData, _ := json.Marshal(reqBody)

	if a.config.Debug {
		a.saveDebugPrompt(jsonData)
	}

	return jsonData
}

func (a *agentImpl) createNonStreamingRequest(jsonData []byte) (*http.Request, error) {
	reqURL := fmt.Sprintf("%s/v1/chat/completions", a.config.LlamaServerURL)
	req, err := http.NewRequestWithContext(context.Background(), "POST", reqURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (a *agentImpl) decodeResponse(body io.Reader) (map[string]interface{}, error) {
	var rawResponse map[string]interface{}
	if err := json.NewDecoder(body).Decode(&rawResponse); err != nil {
		return nil, err
	}
	return rawResponse, nil
}

func (a *agentImpl) extractToolCallsFromResponse(rawResponse map[string]interface{}) ([]ToolCall, error) {
	choices, ok := rawResponse["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return nil, nil
	}

	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil, nil
	}

	message, ok := choice["message"].(map[string]interface{})
	if !ok {
		return nil, nil
	}

	return parseToolCalls(message)
}

func buildToolCallForRequest(tc ToolCall) ToolCall {
	argsStr := ToolCallArgumentsStr(tc)
	if argsStr == "" {
		return tc
	}
	var argsObj interface{}
	if err := json.Unmarshal([]byte(argsStr), &argsObj); err != nil {
		return tc
	}
	rawArgs, _ := json.Marshal(argsObj)
	tc.Function.Arguments = rawArgs
	return tc
}

func (a *agentImpl) buildMessagesWithToolResults(originalMessages []Message, toolCalls []ToolCall, toolResults []ToolCallResult) []Message {
	messages := make([]Message, len(originalMessages))
	copy(messages, originalMessages)

	reqToolCalls := make([]ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		reqToolCalls[i] = buildToolCallForRequest(tc)
	}
	messages = append(messages, Message{
		Role:      "assistant",
		Content:   "",
		ToolCalls: reqToolCalls,
	})

	for _, tr := range toolResults {
		messages = append(messages, Message{
			Role:       "tool",
			ToolCallID: tr.ToolCallID,
			Name:       tr.ToolName,
			Content:    tr.Content,
		})
	}

	return messages
}
