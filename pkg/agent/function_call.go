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

	// Собираем response, reasoning и tool_calls из streaming
	responseText, reasoningText, finishReason, streamToolCalls := a.collectStreamResponseWithToolCalls(chunkChan)

	// Отправляем reasoning в thinkingPeerID если есть
	if reasoningText != "" && a.thinkingCallback != nil {
		if err := a.thinkingCallback(session.GetPeerID(), reasoningText); err != nil {
			fmt.Printf("[WARN] Failed to send thinking message: %v\n", err)
		}
	}

	// Если LLM вернул tool_calls — используем те что собрали из стриминга
	if finishReason == "tool_calls" || len(streamToolCalls) > 0 {
		toolCalls := streamToolCalls
		if len(toolCalls) == 0 {
			// Fallback: если не собрали tool_calls из стриминга — пробуем non-streaming
			fmt.Printf("[WARN] LLM returned tool_calls but none collected from stream, trying non-streaming\n")
			var err error
			toolCalls, err = a.getToolCallsFromResponse(ctx, messages, toolsSchema)
			if err != nil {
				fmt.Printf("[WARN] Non-streaming tool_calls failed: %v\n", err)
			}
		}
		if len(toolCalls) == 0 {
			// Нет tool_calls — возвращаем пустой ответ или reasoning если есть
			if reasoningText != "" {
				session.AddAssistantMessage(reasoningText)
				return FunctionCallResult{Success: true, Response: reasoningText}, nil
			}
			return a.returnTextResponse(session, "I'm here and ready to help! What would you like to know?"), nil
		}

		result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())
		if len(result.ToolCalls) > 0 {
			finalResponse, err := a.processToolResults(ctx, messages, toolCalls, result.ToolCalls, session)
			if err != nil {
				return FunctionCallResult{}, fmt.Errorf("process tool results: %w", err)
			}
			return FunctionCallResult{Success: true, Response: finalResponse}, nil
		}
	}

	// JSON fallback: проверяем responseText на наличие JSON tool calls
	if result, used, err := a.jsonFallback(ctx, responseText, messages, session); used {
		if err != nil {
			return FunctionCallResult{}, fmt.Errorf("json fallback: %w", err)
		}
		return result, nil
	}

	// XML fallback: проверяем responseText на наличие XML tool calls
	if result, used, err := a.xmlFallback(ctx, responseText, messages, session); used {
		if err != nil {
			return FunctionCallResult{}, fmt.Errorf("xml fallback: %w", err)
		}
		return result, nil
	}

	if responseText == "" || a.isNonToolResponse(finishReason) {
		// Fallback — если LLM не ответил текстом
		if responseText == "" {
			responseText = "I'm here and ready to help! What would you like to know?"
		}
		session.AddAssistantMessage(responseText)
		return FunctionCallResult{
			Success:  true,
			Response: responseText,
		}, nil
	}

	// Обычный текстовый ответ
	session.AddAssistantMessage(responseText)
	return FunctionCallResult{
		Success:  true,
		Response: responseText,
	}, nil
}

// xmlFallback проверяет responseText на наличие XML tool calls,
// выполняет их и обрабатывает результаты
func (a *agentImpl) xmlFallback(ctx context.Context, responseText string, messages []Message, session *session.Session) (FunctionCallResult, bool, error) {
	parsed := ParseXMLToolCalls(responseText)
	if len(parsed.ToolCalls) == 0 {
		return FunctionCallResult{}, false, nil
	}

	toolCalls := convertXMLToolCalls(parsed.ToolCalls)

	fmt.Printf("[TOOL] XML fallback: detected %d tool calls in response text\n", len(toolCalls))

	result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())
	if len(result.ToolCalls) > 0 {
		hasSuccess := false
		for _, tr := range result.ToolCalls {
			if !tr.IsError {
				hasSuccess = true
				break
			}
		}
		if !hasSuccess {
			fmt.Printf("[TOOL] XML fallback: all %d tool calls failed, skipping\n", len(toolCalls))
			return FunctionCallResult{}, false, nil
		}

		finalResponse, err := a.processXMLToolResults(ctx, messages, parsed.Content, toolCalls, result.ToolCalls, session)
		if err != nil {
			return FunctionCallResult{}, true, fmt.Errorf("process xml tool results: %w", err)
		}
		return FunctionCallResult{Success: true, Response: finalResponse}, true, nil
	}

	return FunctionCallResult{}, false, nil
}

// jsonFallback проверяет responseText на наличие JSON tool calls,
// выполняет их и обрабатывает результаты
func (a *agentImpl) jsonFallback(ctx context.Context, responseText string, messages []Message, session *session.Session) (FunctionCallResult, bool, error) {
	parsed := ParseJSONToolCalls(responseText)
	if len(parsed.ToolCalls) == 0 {
		return FunctionCallResult{}, false, nil
	}

	toolCalls := convertXMLToolCalls(parsed.ToolCalls)

	fmt.Printf("[TOOL] JSON fallback: detected %d tool calls in response text\n", len(toolCalls))

	result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())
	if len(result.ToolCalls) > 0 {
		hasSuccess := false
		for _, tr := range result.ToolCalls {
			if !tr.IsError {
				hasSuccess = true
				break
			}
		}
		if !hasSuccess {
			fmt.Printf("[TOOL] JSON fallback: all %d tool calls failed, skipping\n", len(toolCalls))
			return FunctionCallResult{}, false, nil
		}

		finalResponse, err := a.processXMLToolResults(ctx, messages, parsed.Content, toolCalls, result.ToolCalls, session)
		if err != nil {
			return FunctionCallResult{}, true, fmt.Errorf("process json tool results: %w", err)
		}
		return FunctionCallResult{Success: true, Response: finalResponse}, true, nil
	}

	return FunctionCallResult{}, false, nil
}

// processXMLToolResults отправляет результат выполнения XML инструментов обратно в AI
func (a *agentImpl) processXMLToolResults(ctx context.Context, originalMessages []Message, cleanedContent string, toolCalls []ToolCall, toolResults []ToolCallResult, session *session.Session) (string, error) {
	messages := make([]Message, len(originalMessages))
	copy(messages, originalMessages)

	// Assistant message: очищенный текст + tool_calls
	reqToolCalls := make([]ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		reqToolCalls[i] = buildToolCallForRequest(tc)
	}
	messages = append(messages, Message{
		Role:      "assistant",
		Content:   cleanedContent,
		ToolCalls: reqToolCalls,
	})

	// Результаты инструментов
	for _, tr := range toolResults {
		messages = append(messages, Message{
			Role:    "tool",
			Content: tr.Content,
		})
	}

	streamConfig := StreamingConfig{
		Model:       a.config.Model,
		MaxTokens:   a.config.MaxTokens,
		Temperature: a.config.Temperature,
		Stream:      true,
	}

	chunkChan, err := a.streamingRequest(ctx, streamConfig, messages)
	if err != nil {
		return "", fmt.Errorf("streaming request for xml tool results: %w", err)
	}

	responseText, _ := a.collectStreamResponse(chunkChan)
	if responseText == "" {
		responseText = "Tools executed successfully."
	}

	session.AddAssistantMessage(responseText)
	return responseText, nil
}

// convertXMLToolCalls конвертирует XMLToolCall в ToolCall для executeAllTools
func convertXMLToolCalls(xmlCalls []XMLToolCall) []ToolCall {
	toolCalls := make([]ToolCall, len(xmlCalls))
	for i, xc := range xmlCalls {
		argsJSON, _ := json.Marshal(xc.Args)
		argsWrapped, _ := json.Marshal(string(argsJSON))
		toolCalls[i] = ToolCall{
			ID:    fmt.Sprintf("xml_call_%d", i),
			Type:  "function",
			Index: i,
			Function: ToolCallFunction{
				Name:      xc.Name,
				Arguments: argsWrapped,
			},
		}
	}
	return toolCalls
}

// buildToolsStreamConfig создаёт конфигурацию для streaming с инструментами
func (a *agentImpl) buildToolsStreamConfig(toolsSchema []map[string]interface{}) StreamingConfig {
	// Если схемы инструментов переданы через SetTools — используем их
	schema := toolsSchema
	if schema == nil && len(a.toolSchemas) > 0 {
		schema = a.toolSchemas
	}
	return StreamingConfig{
		Model:       a.config.Model,
		MaxTokens:   a.config.MaxTokens,
		Temperature: a.config.Temperature,
		Tools:       schema,
		Stream:      true,
	}
}

// collectStreamResponse собирает ответ из streaming потока (только response, без reasoning)
func (a *agentImpl) collectStreamResponse(chunkChan <-chan StreamChunkEvent) (string, string) {
	var fullResponse strings.Builder
	var fullReasoning strings.Builder

	for event := range chunkChan {
		if event.IsDone {
			break
		}
		if event.Content != "" {
			fullResponse.WriteString(event.Content)
		}
		if event.ReasoningContent != "" {
			fullReasoning.WriteString(event.ReasoningContent)
		}
	}

	response := fullResponse.String()
	reasoning := fullReasoning.String()
	return response, reasoning
}

// collectStreamResponseWithToolCalls собирает ответ из streaming потока с reasoning и tool_calls
func (a *agentImpl) collectStreamResponseWithToolCalls(chunkChan <-chan StreamChunkEvent) (string, string, string, []ToolCall) {
	var fullResponse strings.Builder
	var fullReasoning strings.Builder
	var finishReason string
	var allToolCalls []ToolCall

	for event := range chunkChan {
		if event.IsDone {
			finishReason = event.FinishReason
			break
		}
		if event.Content != "" {
			fullResponse.WriteString(event.Content)
		}
		if event.ReasoningContent != "" {
			fullReasoning.WriteString(event.ReasoningContent)
		}
		if len(event.ToolCalls) > 0 {
			allToolCalls = MergeToolCalls(allToolCalls, event.ToolCalls)
		}
	}

	response := fullResponse.String()
	reasoning := fullReasoning.String()
	return response, reasoning, finishReason, allToolCalls
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
func (a *agentImpl) executeAllTools(ctx context.Context, toolCalls []ToolCall, peerID int64) FunctionCallResult {
	result := FunctionCallResult{
		Success:   true,
		ToolCalls: make([]ToolCallResult, 0),
	}

	for _, tc := range toolCalls {
		toolResult, execErr := a.executeTool(tc, peerID)
		if execErr != nil {
			result.ToolCalls = append(result.ToolCalls, ToolCallResult{
				ToolCallID: tc.ID,
				ToolName:   ToolCallName(tc),
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
	// Не указываем модель — сервер использует модель по умолчанию
	reqBody := map[string]interface{}{
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
func (a *agentImpl) processToolResults(ctx context.Context, originalMessages []Message, toolCalls []ToolCall, toolResults []ToolCallResult, session *session.Session) (string, error) {
	messages := a.buildMessagesWithToolResults(originalMessages, toolCalls, toolResults)

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

	// Если модель не вернула текст — осмысленный fallback
	if responseText == "" {
		responseText = "Tools executed successfully."
	}

	session.AddAssistantMessage(responseText)
	return responseText, nil
}

// buildToolCall converts parsed ToolCall to request format (arguments as object, not string)
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

// buildMessagesWithToolResults формирует массив сообщений с результатами инструментов
// OpenAI требует: user → assistant(tool_calls) → tool(result) → assistant(ответ)
func (a *agentImpl) buildMessagesWithToolResults(originalMessages []Message, toolCalls []ToolCall, toolResults []ToolCallResult) []Message {
	messages := make([]Message, len(originalMessages))
	copy(messages, originalMessages)

	// Assistant message с tool_calls — arguments должны быть объектом, а не строкой
	reqToolCalls := make([]ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		reqToolCalls[i] = buildToolCallForRequest(tc)
	}
	messages = append(messages, Message{
		Role:      "assistant",
		Content:   "",
		ToolCalls: reqToolCalls,
	})

	// Результаты инструментов
	for _, tr := range toolResults {
		messages = append(messages, Message{
			Role:    "tool",
			Content: tr.Content,
		})
	}

	return messages
}

// briefToolCall формирует краткое описание вызова инструмента (без содержимого файлов)
func briefToolCall(toolName string, args map[string]string) string {
	switch toolName {
	case "file_read", "file_write", "file_list", "edit":
		if path, ok := args["path"]; ok {
			return fmt.Sprintf("%s(%q)", toolName, truncateStr(path, 80))
		}
	case "shell_execute":
		if cmd, ok := args["command"]; ok {
			return fmt.Sprintf("shell(%q)", truncateStr(cmd, 60))
		}
	case "web_fetch":
		if url, ok := args["url"]; ok {
			return fmt.Sprintf("web_fetch(%q)", truncateStr(url, 80))
		}
	case "web_search":
		if q, ok := args["query"]; ok {
			return fmt.Sprintf("web_search(%q)", truncateStr(q, 60))
		}
	case "search_code":
		if p, ok := args["pattern"]; ok {
			return fmt.Sprintf("search_code(%q)", truncateStr(p, 60))
		}
	case "glob":
		if p, ok := args["pattern"]; ok {
			return fmt.Sprintf("glob(%q)", truncateStr(p, 60))
		}
	case "calc":
		if e, ok := args["expression"]; ok {
			return fmt.Sprintf("calc(%q)", truncateStr(e, 60))
		}
	case "time_get":
		return "time_get()"
	}
	return toolName
}

func truncateStr(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

// executeTool выполняет инструмент с логированием и отправкой thinking
func (a *agentImpl) executeTool(toolCall ToolCall, peerID int64) (ToolCallResult, error) {
	toolName := ToolCallName(toolCall)

	tool, ok := a.toolsRegistry.Get(toolName)
	if !ok {
		errMsg := fmt.Sprintf("Tool not found: %s", toolName)
		fmt.Printf("[TOOL] Error: %s\n", errMsg)
		a.sendThinking(peerID, "[TOOL] Error: "+errMsg)
		return a.createErrorResult(toolCall.ID, toolName, errMsg), fmt.Errorf(errMsg)
	}

	args, err := parseToolArguments(toolCall)
	if err != nil {
		errMsg := fmt.Sprintf("Invalid arguments for %s: %v", toolName, err)
		fmt.Printf("[TOOL] Error: %s\n", errMsg)
		a.sendThinking(peerID, "[TOOL] Error: "+errMsg)
		return a.createErrorResult(toolCall.ID, toolName, errMsg), err
	}

	brief := briefToolCall(toolName, args)
	fmt.Printf("[TOOL] Call: %s\n", brief)
	a.sendThinking(peerID, "[TOOL] Call: "+brief)

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		errMsg := fmt.Sprintf("Execution error for %s: %v", toolName, err)
		fmt.Printf("[TOOL] Error: %s\n", errMsg)
		a.sendThinking(peerID, "[TOOL] Error: "+errMsg)
		return a.createErrorResult(toolCall.ID, toolName, errMsg), err
	}

	content := tools.MarshalToolResult(result)
	resultMsg := fmt.Sprintf("[TOOL] Result: %s success=%v", toolName, result.Success)
	fmt.Println(resultMsg)
	a.sendThinking(peerID, resultMsg)

	return ToolCallResult{
		ToolCallID: toolCall.ID,
		ToolName:   toolName,
		Content:    content,
		IsError:    !result.Success,
	}, nil
}

// sendThinking отправляет thinking сообщение через callback
func (a *agentImpl) sendThinking(peerID int64, content string) {
	if a.thinkingCallback != nil {
		a.thinkingCallback(peerID, content)
	}
}

func (a *agentImpl) createErrorResult(toolCallID, toolName, errorMsg string) ToolCallResult {
	return ToolCallResult{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Content:    errorMsg,
		IsError:    true,
	}
}
