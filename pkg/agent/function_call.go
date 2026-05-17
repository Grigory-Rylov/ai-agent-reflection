package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/opencode/llama-client/pkg/tools"
	"github.com/opencode/llama-client/session"
)

// ============================================================
// Function Calling — оркестрация вызовов инструментов
// ============================================================

// FunctionCallResult представляет результат function calling
type FunctionCallResult struct {
	Success  bool
	Response string
	ToolCalls []ToolCallResult
}

// processWithTools обрабатывает ответ AI с поддержкой инструментов
func (a *agentImpl) processWithTools(ctx context.Context, messages []Message, session *session.Session, maxToolCalls int) (FunctionCallResult, error) {
	toolsSchema := a.toolsRegistry.ToOpenAISchema()
	streamConfig := a.buildToolsStreamConfig(toolsSchema)

	chunkChan, err := a.streamingRequest(ctx, streamConfig, messages)
	if err != nil {
		return FunctionCallResult{}, fmt.Errorf("streaming request: %w", err)
	}

	responseText, finishReason := a.collectStreamResponse(chunkChan)
	if responseText == "" || a.isNonToolResponse(finishReason) {
		return a.returnTextResponse(session, responseText), nil
	}

	rawToolCalls, err := a.getToolCallsFromResponse(ctx, messages, toolsSchema)
	if err != nil || len(rawToolCalls) == 0 {
		return a.returnTextResponse(session, responseText), nil
	}

	result := a.executeAllTools(ctx, rawToolCalls)
	if len(result.ToolCalls) > 0 {
		finalResponse, err := a.processToolResults(ctx, messages, result.ToolCalls, session)
		if err != nil {
			return FunctionCallResult{}, fmt.Errorf("process tool results: %w", err)
		}
		return FunctionCallResult{Success: true, Response: finalResponse}, nil
	}

	return a.returnTextResponse(session, responseText), nil
}

// buildToolsStreamConfig создаёт конфигурацию для streaming с инструментами
func (a *agentImpl) buildToolsStreamConfig(toolsSchema []map[string]interface{}) StreamingConfig {
	return StreamingConfig{
		Model:       a.config.Model,
		MaxTokens:   a.config.MaxTokens,
		Temperature: a.config.Temperature,
		Tools:       toolsSchema,
		Stream:      true,
	}
}

// collectStreamResponse собирает ответ из streaming потока
func (a *agentImpl) collectStreamResponse(chunkChan <-chan StreamChunkEvent) (string, string) {
	var fullResponse strings.Builder
	var finishReason string

	for event := range chunkChan {
		if event.IsDone {
			finishReason = event.FinishReason
			break
		}
		if event.Content != "" {
			fullResponse.WriteString(event.Content)
		}
	}

	return fullResponse.String(), finishReason
}

// isNonToolResponse проверяет, что ответ не содержит tool_calls
func (a *agentImpl) isNonToolResponse(finishReason string) bool {
	if finishReason == "" {
		return false
	}
	return !strings.Contains(finishReason, "tool")
}

// returnTextResponse возвращает текстовый ответ
func (a *agentImpl) returnTextResponse(session *session.Session, responseText string) FunctionCallResult {
	session.AddAssistantMessage(responseText)
	return FunctionCallResult{
		Success:  true,
		Response: responseText,
	}
}

// executeAllTools выполняет все инструменты из ответа AI
func (a *agentImpl) executeAllTools(ctx context.Context, toolCalls []ToolCall) FunctionCallResult {
	result := FunctionCallResult{
		Success:   true,
		ToolCalls: make([]ToolCallResult, 0),
	}

	for _, tc := range toolCalls {
		toolResult, execErr := a.executeTool(tc)
		if execErr != nil {
			result.ToolCalls = append(result.ToolCalls, ToolCallResult{
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Content:    fmt.Sprintf("Error: %v", execErr),
				IsError:    true,
			})
			continue
		}
		result.ToolCalls = append(result.ToolCalls, toolResult)
	}

	return result
}

// getToolCallsFromResponse отправляет не-streaming запрос для получения tool_calls
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

// buildNonStreamingRequestJSON формирует JSON для non-streaming запроса
func (a *agentImpl) buildNonStreamingRequestJSON(messages []Message, toolsSchema []map[string]interface{}) []byte {
	reqBody := map[string]interface{}{
		"model":       a.config.Model,
		"messages":    messages,
		"temperature": a.config.Temperature,
		"max_tokens":  a.config.MaxTokens,
		"stream":      false,
	}

	if len(toolsSchema) > 0 {
		reqBody["tools"] = toolsSchema
	}

	jsonData, _ := json.Marshal(reqBody)
	return jsonData
}

// createNonStreamingRequest создаёт HTTP запрос без streaming
func (a *agentImpl) createNonStreamingRequest(jsonData []byte) (*http.Request, error) {
	reqURL := fmt.Sprintf("%s/v1/chat/completions", a.config.LlamaServerURL)
	req, err := http.NewRequestWithContext(context.Background(), "POST", reqURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// decodeResponse декодирует JSON ответ
func (a *agentImpl) decodeResponse(body io.Reader) (map[string]interface{}, error) {
	var rawResponse map[string]interface{}
	if err := json.NewDecoder(body).Decode(&rawResponse); err != nil {
		return nil, err
	}
	return rawResponse, nil
}

// extractToolCallsFromResponse извлекает tool_calls из ответа
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

// processToolResults отправляет результат выполнения инструментов обратно в AI
func (a *agentImpl) processToolResults(ctx context.Context, originalMessages []Message, toolResults []ToolCallResult, session *session.Session) (string, error) {
	messages := a.buildMessagesWithToolResults(originalMessages, toolResults)

	streamConfig := StreamingConfig{
		Model:       a.config.Model,
		MaxTokens:   a.config.MaxTokens,
		Temperature: a.config.Temperature,
		Stream:      true,
	}

	chunkChan, err := a.streamingRequest(ctx, streamConfig, messages)
	if err != nil {
		return "", fmt.Errorf("streaming request for tool results: %w", err)
	}

	responseText, _ := a.collectStreamResponse(chunkChan)
	session.AddAssistantMessage(responseText)

	return responseText, nil
}

// buildMessagesWithToolResults формирует массив сообщений с результатами инструментов
func (a *agentImpl) buildMessagesWithToolResults(originalMessages []Message, toolResults []ToolCallResult) []Message {
	messages := make([]Message, len(originalMessages))
	copy(messages, originalMessages)

	for _, tr := range toolResults {
		messages = append(messages, Message{
			Role:    "tool",
			Content: tr.Content,
		})
	}

	return messages
}

// executeTool выполняет инструмент
func (a *agentImpl) executeTool(toolCall ToolCall) (ToolCallResult, error) {
	tool, ok := a.toolsRegistry.Get(toolCall.Name)
	if !ok {
		return a.createErrorResult(toolCall.ID, toolCall.Name, fmt.Sprintf("Tool not found: %s", toolCall.Name)), fmt.Errorf("tool not found: %s", toolCall.Name)
	}

	args, err := a.parseToolArguments(toolCall)
	if err != nil {
		return a.createErrorResult(toolCall.ID, toolCall.Name, fmt.Sprintf("Invalid arguments: %v", err)), err
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		return a.createErrorResult(toolCall.ID, toolCall.Name, fmt.Sprintf("Execution error: %v", err)), err
	}

	content := tools.MarshalToolResult(result)
	return ToolCallResult{
		ToolCallID: toolCall.ID,
		ToolName:   toolCall.Name,
		Content:    content,
		IsError:    !result.Success,
	}, nil
}

// createErrorResult создаёт результат с ошибкой
func (a *agentImpl) createErrorResult(toolCallID, toolName, errorMsg string) ToolCallResult {
	return ToolCallResult{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Content:    errorMsg,
		IsError:    true,
	}
}

// parseToolArguments парсит аргументы инструмента
func (a *agentImpl) parseToolArguments(toolCall ToolCall) (map[string]string, error) {
	if len(toolCall.Arguments) == 0 {
		return make(map[string]string), nil
	}

	var args map[string]string
	if err := json.Unmarshal(toolCall.Arguments, &args); err != nil {
		return nil, err
	}
	return args, nil
}
