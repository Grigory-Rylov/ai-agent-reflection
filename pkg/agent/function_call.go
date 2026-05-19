package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/pkg/tools"
	sess "github.com/opencode/llama-client/session"
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
func (a *agentImpl) processWithTools(ctx context.Context, messages []Message, session *sess.Session, maxToolCalls int) (FunctionCallResult, error) {
	toolsSchema := a.toolsRegistry.ToOpenAISchema()
	streamConfig := a.buildToolsStreamConfig(toolsSchema)

	chunkChan, err := a.streamingRequest(ctx, streamConfig, messages)
	if err != nil {
		return FunctionCallResult{}, fmt.Errorf("streaming request: %w", err)
	}

	// Собираем response, reasoning и tool_calls из streaming
	responseText, reasoningText, finishReason, streamToolCalls, err := a.collectStreamResponseWithToolCalls(chunkChan)
	if err != nil {
		return FunctionCallResult{}, err
	}

	// DEBUG: логируем все части ответа
	logger.DebugToFile("streaming response: content=%d, reasoning=%d, tool_calls=%d, finish=%q",
		len(responseText), len(reasoningText), len(streamToolCalls), finishReason)
	if len(responseText) > 0 {
		logger.DebugToFile("content: %s", responseText)
	}
	if len(reasoningText) > 0 {
		logger.DebugToFile("reasoning: %s", reasoningText)
	}

	// Сохраняем выполненные NATIVE tool calls для фильтрации дублей
	executedToolCalls := make(map[string]bool)

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

		// Логируем формат инструментов
		fmt.Printf("[TOOL] NATIVE format: detected %d tool calls\n", len(toolCalls))

		// Сохраняем сигнатуры выполненных инструментов
		for _, tc := range toolCalls {
			sig := toolCallSignature(tc)
			executedToolCalls[sig] = true
		}

		result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())
		if len(result.ToolCalls) > 0 {
			finalResponse, err := a.processToolResults(ctx, messages, toolCalls, result.ToolCalls, session)
			if err != nil {
				return FunctionCallResult{}, fmt.Errorf("process tool results: %w", err)
			}
			// processToolResults уже обработал всё включая XML - возвращаем результат
			return FunctionCallResult{Success: true, Response: finalResponse}, nil
		}
	}

	// Проверяем reasoning на наличие XML tool calls
	// Извлекаем и выполняем инструменты, отправляем очищенный текст в thinking
	cleanedReasoning := reasoningText
	if reasoningText != "" {
		parsedReasoning := ParseXMLToolCalls(reasoningText)
		if len(parsedReasoning.ToolCalls) > 0 {
			// Есть XML в reasoning - выполняем инструменты
			fmt.Printf("[TOOL] XML in reasoning: detected %d tool calls\n", len(parsedReasoning.ToolCalls))

			// Фильтруем дубли уже выполненных NATIVE инструментов
			var uniqueCalls []XMLToolCall
			for _, tc := range parsedReasoning.ToolCalls {
				sig := xmlToolCallSignature(tc)
				if !executedToolCalls[sig] {
					uniqueCalls = append(uniqueCalls, tc)
				} else {
					fmt.Printf("[TOOL] XML duplicate skipped: %s\n", tc.Name)
				}
			}

			if len(uniqueCalls) > 0 {
				toolCalls := convertXMLToolCalls(uniqueCalls)
				result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())
				if len(result.ToolCalls) > 0 {
					finalResponse, err := a.processXMLToolResults(ctx, messages, parsedReasoning.Content, toolCalls, result.ToolCalls, session)
					if err != nil {
						return FunctionCallResult{}, fmt.Errorf("process xml tool results: %w", err)
					}
					return FunctionCallResult{Success: true, Response: finalResponse}, nil
				}
			}

			// Очищенный reasoning (без XML) для отправки в thinking
			cleanedReasoning = parsedReasoning.Content
		}
	}

	// Отправляем очищенный reasoning в thinkingPeerID
	if cleanedReasoning != "" && a.thinkingCallback != nil {
		if err := a.thinkingCallback(session.GetPeerID(), cleanedReasoning); err != nil {
			fmt.Printf("[WARN] Failed to send thinking message: %v\n", err)
		}
	}

	// XML fallback: проверяем responseText И reasoningText на наличие XML tool calls, фильтруем дубли
	// Модель может отправить XML в любом из этих полей
	textToCheck := responseText
	if len(reasoningText) > len(textToCheck) {
		textToCheck = reasoningText
		logger.DebugToFile("Using reasoningText for XML check (%d chars)", len(reasoningText))
	}

	if xmlResult, used, err := a.xmlFallbackFiltered(ctx, textToCheck, messages, session, executedToolCalls); used {
		if err != nil {
			return FunctionCallResult{}, fmt.Errorf("xml fallback: %w", err)
		}
		return xmlResult, nil
	}

	// JSON fallback: проверяем responseText на наличие JSON tool calls
	if result, used, err := a.jsonFallback(ctx, responseText, messages, session); used {
		if err != nil {
			return FunctionCallResult{}, fmt.Errorf("json fallback: %w", err)
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

// toolCallSignature создаёт уникальную сигнатуру для инструмента (имя + аргументы)
func toolCallSignature(tc ToolCall) string {
	name := tc.Function.Name
	args := ToolCallArgumentsStr(tc)
	return name + ":" + args
}

// xmlToolCallSignature создаёт сигнатуру для XML tool call
func xmlToolCallSignature(tc XMLToolCall) string {
	argsJSON, _ := json.Marshal(tc.Args)
	return tc.Name + ":" + string(argsJSON)
}

// xmlFallbackFiltered проверяет responseText на наличие XML tool calls,
// фильтрует дубли уже выполненных инструментов, выполняет оставшиеся
func (a *agentImpl) xmlFallbackFiltered(ctx context.Context, responseText string, messages []Message, session *sess.Session, executed map[string]bool) (FunctionCallResult, bool, error) {
	parsed := ParseXMLToolCalls(responseText)
	if len(parsed.ToolCalls) == 0 {
		return FunctionCallResult{}, false, nil
	}

	// Фильтруем дубли
	var uniqueCalls []XMLToolCall
	for _, tc := range parsed.ToolCalls {
		sig := xmlToolCallSignature(tc)
		if executed[sig] {
			fmt.Printf("[TOOL] XML duplicate skipped: %s\n", tc.Name)
			continue
		}
		uniqueCalls = append(uniqueCalls, tc)
	}

	if len(uniqueCalls) == 0 {
		return FunctionCallResult{}, false, nil
	}

	fmt.Printf("[TOOL] XML fallback: detected %d tool calls (%d duplicates skipped)\n", len(uniqueCalls), len(parsed.ToolCalls)-len(uniqueCalls))

	toolCalls := convertXMLToolCalls(uniqueCalls)
	result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())

	if len(result.ToolCalls) > 0 {
		// Всегда отправляем результаты модели - даже если были ошибки
		// Это позволит модели понять что пошло не так и исправить запрос
		finalResponse, err := a.processXMLToolResults(ctx, messages, parsed.Content, toolCalls, result.ToolCalls, session)
		if err != nil {
			return FunctionCallResult{}, true, fmt.Errorf("process xml tool results: %w", err)
		}
		return FunctionCallResult{Success: true, Response: finalResponse}, true, nil
	}

	return FunctionCallResult{}, false, nil
}

// xmlFallback проверяет responseText на наличие XML tool calls,
// выполняет их и обрабатывает результаты
func (a *agentImpl) xmlFallback(ctx context.Context, responseText string, messages []Message, session *sess.Session) (FunctionCallResult, bool, error) {
	parsed := ParseXMLToolCalls(responseText)
	if len(parsed.ToolCalls) == 0 {
		return FunctionCallResult{}, false, nil
	}

	toolCalls := convertXMLToolCalls(parsed.ToolCalls)

	fmt.Printf("[TOOL] XML fallback: detected %d tool calls in response text\n", len(toolCalls))

	result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())
	if len(result.ToolCalls) > 0 {
		// Всегда отправляем результаты модели - даже если были ошибки
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
func (a *agentImpl) jsonFallback(ctx context.Context, responseText string, messages []Message, session *sess.Session) (FunctionCallResult, bool, error) {
	parsed := ParseJSONToolCalls(responseText)
	if len(parsed.ToolCalls) == 0 {
		return FunctionCallResult{}, false, nil
	}

	toolCalls := convertXMLToolCalls(parsed.ToolCalls)

	fmt.Printf("[TOOL] JSON fallback: detected %d tool calls in response text\n", len(toolCalls))

	result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())
	if len(result.ToolCalls) > 0 {
		// Всегда отправляем результаты модели - даже если были ошибки
		finalResponse, err := a.processXMLToolResults(ctx, messages, parsed.Content, toolCalls, result.ToolCalls, session)
		if err != nil {
			return FunctionCallResult{}, true, fmt.Errorf("process json tool results: %w", err)
		}
		return FunctionCallResult{Success: true, Response: finalResponse}, true, nil
	}

	return FunctionCallResult{}, false, nil
}

// processXMLToolResults отправляет результат выполнения XML инструментов обратно в AI
func (a *agentImpl) processXMLToolResults(ctx context.Context, originalMessages []Message, cleanedContent string, toolCalls []ToolCall, toolResults []ToolCallResult, session *sess.Session) (string, error) {
	// Сохраняем сообщение ассистента с tool_calls в историю сессии
	sessionToolCalls := make([]sess.MsgToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		sessionToolCalls[i] = sess.MsgToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: sess.MsgToolCallFunc{
				Name:      tc.Function.Name,
				Arguments: string(tc.Function.Arguments),
			},
		}
	}
	session.AddAssistantMessageWithToolCalls(cleanedContent, sessionToolCalls)

	// Сохраняем результаты инструментов в историю сессии
	for _, tr := range toolResults {
		session.AddToolMessage(tr.ToolCallID, tr.ToolName, tr.Content)
	}

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
			Role:       "tool",
			ToolCallID: tr.ToolCallID,
			Name:       tr.ToolName,
			Content:    tr.Content,
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

	responseText, _, err := a.collectStreamResponse(chunkChan)
	if err != nil {
		return "", err
	}
	if responseText == "" {
		responseText = "Tools executed successfully."
	}

	// Защита: проверяем responseText на оставшийся XML-код
	// Модель может сгенерировать новые XML tool calls в ответе
	finalCheck := ParseXMLToolCalls(responseText)
	if len(finalCheck.ToolCalls) > 0 {
		logger.DebugToFile("processXMLToolResults: XML detected in response, using cleaned content")
		responseText = finalCheck.Content
	}

	// Если после очистки пусто - отправляем результат инструментов как ответ
	// Это лучше чем пустое сообщение
	if responseText == "" {
		logger.DebugToFile("processXMLToolResults: responseText is empty, using tool results summary")
		// Формируем краткий отчёт о выполненных инструментах
		var summary strings.Builder
		for _, tr := range toolResults {
			if tr.IsError {
				summary.WriteString(fmt.Sprintf("❌ %s: failed\n", tr.ToolName))
			} else {
				summary.WriteString(fmt.Sprintf("✅ %s: success\n", tr.ToolName))
			}
		}
		responseText = strings.TrimSpace(summary.String())
		if responseText == "" {
			responseText = "Инструменты выполнены."
		}
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
func (a *agentImpl) collectStreamResponse(chunkChan <-chan StreamChunkEvent) (string, string, error) {
	var fullResponse strings.Builder
	var fullReasoning strings.Builder

	for event := range chunkChan {
		if event.IsError {
			return "", "", fmt.Errorf("API error: %s (code: %s)", event.Content, event.ErrorCode)
		}
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
	return response, reasoning, nil
}

// collectStreamResponseWithToolCalls собирает ответ из streaming потока с reasoning и tool_calls
func (a *agentImpl) collectStreamResponseWithToolCalls(chunkChan <-chan StreamChunkEvent) (string, string, string, []ToolCall, error) {
	var fullResponse strings.Builder
	var fullReasoning strings.Builder
	var finishReason string
	var allToolCalls []ToolCall

	for event := range chunkChan {
		if event.IsError {
			return "", "", "", nil, fmt.Errorf("API error: %s (code: %s)", event.Content, event.ErrorCode)
		}
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
	return response, reasoning, finishReason, allToolCalls, nil
}

// isNonToolResponse проверяет, что ответ не содержит tool_calls
func (a *agentImpl) isNonToolResponse(finishReason string) bool {
	if finishReason == "" {
		return false
	}
	return !strings.Contains(finishReason, "tool")
}

// returnTextResponse возвращает текстовый ответ
func (a *agentImpl) returnTextResponse(session *sess.Session, responseText string) FunctionCallResult {
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
func (a *agentImpl) processToolResults(ctx context.Context, originalMessages []Message, toolCalls []ToolCall, toolResults []ToolCallResult, session *sess.Session) (string, error) {
	// Сохраняем сообщение ассистента с tool_calls в историю сессии
	sessionToolCalls := make([]sess.MsgToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		sessionToolCalls[i] = sess.MsgToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: sess.MsgToolCallFunc{
				Name:      tc.Function.Name,
				Arguments: string(tc.Function.Arguments),
			},
		}
	}
	session.AddAssistantMessageWithToolCalls("", sessionToolCalls)

	// Сохраняем результаты инструментов в историю сессии
	for _, tr := range toolResults {
		session.AddToolMessage(tr.ToolCallID, tr.ToolName, tr.Content)
	}

	// Формируем сообщения для API из обновлённой истории
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

	// Собираем ответ с проверкой на tool_calls
	responseText, reasoningText, finishReason, streamToolCalls, err := a.collectStreamResponseWithToolCalls(chunkChan)
	if err != nil {
		return "", err
	}

	logger.DebugToFile("processToolResults: content=%d, reasoning=%d, tool_calls=%d, finish=%q",
		len(responseText), len(reasoningText), len(streamToolCalls), finishReason)
	if len(responseText) > 0 {
		logger.DebugToFile("processToolResults content: %s", responseText)
	}
	if len(reasoningText) > 0 {
		logger.DebugToFile("processToolResults reasoning: %s", reasoningText)
	}

	// Если модель вернула новые tool_calls (NATIVE) — выполняем их
	if len(streamToolCalls) > 0 {
		fmt.Printf("[TOOL] NATIVE format: detected %d tool calls in tool results response\n", len(streamToolCalls))
		result := a.executeAllTools(ctx, streamToolCalls, session.GetPeerID())
		if len(result.ToolCalls) > 0 {
			// Рекурсивно обрабатываем результаты
			return a.processToolResults(ctx, messages, streamToolCalls, result.ToolCalls, session)
		}
	}

	// Проверяем на XML tool calls в content И в reasoning
	// Модель может отправить XML в любом из этих полей
	logger.DebugToFile("processToolResults XML check: content=%d chars, reasoning=%d chars", len(responseText), len(reasoningText))

	textToCheck := responseText
	if len(reasoningText) > len(textToCheck) {
		textToCheck = reasoningText
		logger.DebugToFile("processToolResults: using reasoningText for XML check")
	} else {
		logger.DebugToFile("processToolResults: using responseText for XML check")
	}

	// Логируем первые 500 символов текста для парсинга
	preview := textToCheck
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}
	logger.DebugToFile("processToolResults: textToCheck preview: %q", preview)

	parsed := ParseXMLToolCalls(textToCheck)
	logger.DebugToFile("processToolResults: parsed %d XML tool calls from textToCheck", len(parsed.ToolCalls))

	if len(parsed.ToolCalls) == 0 && len(responseText) > 0 && responseText != reasoningText {
		// Пробуем ещё раз с responseText
		parsed = ParseXMLToolCalls(responseText)
		logger.DebugToFile("processToolResults: fallback parse from responseText: %d tool calls", len(parsed.ToolCalls))
	}

	if len(parsed.ToolCalls) > 0 {
		fmt.Printf("[TOOL] XML fallback: detected %d tool calls in tool results response\n", len(parsed.ToolCalls))
		toolCalls := convertXMLToolCalls(parsed.ToolCalls)
		result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())
		if len(result.ToolCalls) > 0 {
			// Всегда отправляем результаты модели - даже если были ошибки
			return a.processXMLToolResults(ctx, messages, parsed.Content, toolCalls, result.ToolCalls, session)
		}
	}

	// Отправляем очищенный reasoning в thinking (без XML тегов)
	cleanedReasoning := reasoningText
	if reasoningText != "" {
		parsedReasoning := ParseXMLToolCalls(reasoningText)
		if len(parsedReasoning.ToolCalls) > 0 {
			cleanedReasoning = parsedReasoning.Content
		}
	}
	if cleanedReasoning != "" && a.thinkingCallback != nil {
		a.thinkingCallback(session.GetPeerID(), cleanedReasoning)
	}

	// Если модель не вернула текст — возвращаем пустую строку
	// Не отправляем fallback сообщения в чат
	if responseText == "" {
		return "", nil
	}

	// Проверяем, не пустой ли финальный текст
	if responseText == "" {
		logger.DebugToFile("processToolResults: responseText is empty, using fallback")
		responseText = "I have processed your request."
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
			Role:       "tool",
			ToolCallID: tr.ToolCallID,
			Name:       tr.ToolName,
			Content:    tr.Content,
		})
	}

	return messages
}

// briefToolCall формирует краткое описание вызова инструмента (без содержимого файлов)
func briefToolCall(toolName string, args map[string]string) string {
	switch toolName {
	case "file_read", "read_file":
		if path, ok := args["path"]; ok {
			return fmt.Sprintf("read_file(%q)", truncateStr(path, 80))
		}
	case "file_write", "write_file":
		if path, ok := args["path"]; ok {
			return fmt.Sprintf("write_file(%q)", truncateStr(path, 80))
		}
	case "file_list", "list_dir", "dir_list":
		if path, ok := args["path"]; ok {
			return fmt.Sprintf("list_dir(%q)", truncateStr(path, 80))
		}
	case "edit", "edit_file":
		if path, ok := args["path"]; ok {
			return fmt.Sprintf("edit(%q)", truncateStr(path, 80))
		}
	case "shell_execute", "shell":
		if cmd, ok := args["command"]; ok {
			return fmt.Sprintf("shell(%q)", truncateStr(cmd, 60))
		}
	case "web_fetch", "fetch":
		if url, ok := args["url"]; ok {
			return fmt.Sprintf("web_fetch(%q)", truncateStr(url, 80))
		}
	case "web_search", "search":
		if q, ok := args["query"]; ok {
			return fmt.Sprintf("web_search(%q)", truncateStr(q, 60))
		}
	case "search_code", "grep", "grep_search":
		if p, ok := args["pattern"]; ok {
			return fmt.Sprintf("search_code(%q)", truncateStr(p, 60))
		}
	case "glob", "find_files":
		if p, ok := args["pattern"]; ok {
			return fmt.Sprintf("glob(%q)", truncateStr(p, 60))
		}
	case "calc", "calculate":
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
		// Инструмент не найден - возвращаем список доступных
		availableTools := a.getAvailableToolsList()
		errMsg := fmt.Sprintf("Tool '%s' not found. Available tools: %s", toolName, availableTools)
		fmt.Printf("[TOOL] Error: %s\n", errMsg)
		a.sendThinking(peerID, "[TOOL] Error: "+errMsg)
		return a.createErrorResult(toolCall.ID, toolName, errMsg), fmt.Errorf("%s", errMsg)
	}

	args, err := parseToolArguments(toolCall)
	if err != nil {
		// Неверные аргументы - возвращаем правильный формат
		schema := tool.Schema()
		errMsg := fmt.Sprintf("Invalid arguments for '%s': %v. Expected schema: %v", toolName, err, schema)
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
	if result.Success {
		resultMsg := fmt.Sprintf("[TOOL] Result: %s success", toolName)
		fmt.Println(resultMsg)
		a.sendThinking(peerID, resultMsg)
	} else {
		// Отправляем в thinking информацию об ошибке
		resultMsg := fmt.Sprintf("[TOOL] Result: %s failed - %s", toolName, truncateStr(content, 200))
		fmt.Println(resultMsg)
		a.sendThinking(peerID, resultMsg)
	}

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

// getAvailableToolsList возвращает список доступных инструментов в виде строки
func (a *agentImpl) getAvailableToolsList() string {
	tools := a.toolsRegistry.GetAll()
	if len(tools) == 0 {
		return "no tools registered"
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name())
	}
	return strings.Join(names, ", ")
}

func (a *agentImpl) createErrorResult(toolCallID, toolName, errorMsg string) ToolCallResult {
	return ToolCallResult{
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Content:    errorMsg,
		IsError:    true,
	}
}
