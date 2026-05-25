package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/pkg/tools"
	sess "github.com/opencode/llama-client/session"
)

// ============================================================
// Function Calling — оркестрация вызовов инструментов
// ============================================================

// MaxToolResultSize — максимальный размер результата инструмента в символах
const MaxToolResultSize = 50000

// FunctionCallResult представляет результат function calling
type FunctionCallResult struct {
	Success  bool
	Response string
	ToolCalls []ToolCallResult
}

// processWithTools обрабатывает ответ AI с поддержкой инструментов
func (a *agentImpl) processWithTools(ctx context.Context, messages []Message, session *sess.Session, maxToolCalls int) (FunctionCallResult, error) {
	toolsSchema := a.toolsRegistry.ToOpenAISchema()
	fmt.Printf("[TOOLS] processWithTools: %d tools in registry, schema has %d entries\n", len(a.toolsRegistry.GetAll()), len(toolsSchema))
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
		logger.DebugToFile("\n--------------------------------")
		logger.DebugToFile("content: %s", responseText)
		logger.DebugToFile("--------------------------------")
	}
	if len(reasoningText) > 0 {
		logger.DebugToFile("reasoning: %s", reasoningText)
	}

	// Сохраняем выполненные NATIVE tool calls для фильтрации дублей
	executedToolCalls := make(map[string]bool)

	// Если LLM вернул tool_calls — используем те что собрали из стриминга
	if finishReason == "tool_calls" || len(streamToolCalls) > 0 {
		toolCalls := streamToolCalls
		logger.DebugToFile("[FLOW] Entering tool_calls branch: finishReason=%q, len(streamToolCalls)=%d", finishReason, len(streamToolCalls))
		if len(toolCalls) == 0 {
			// Fallback: если не собрали tool_calls из стриминга — пробуем non-streaming
			fmt.Printf("[WARN] LLM returned tool_calls but none collected from stream, trying non-streaming\n")
			logger.DebugToFile("[FLOW] Trying non-streaming fallback")
			var err error
			toolCalls, err = a.getToolCallsFromResponse(ctx, messages, toolsSchema)
			if err != nil {
				fmt.Printf("[WARN] Non-streaming tool_calls failed: %v\n", err)
			}
		}
		if len(toolCalls) == 0 {
			// Нет tool_calls — возвращаем пустой ответ или reasoning если есть
			logger.DebugToFile("[FLOW] No tool_calls after all attempts, returning empty")
			if reasoningText != "" {
				session.AddAssistantMessage(reasoningText)
				return FunctionCallResult{Success: true, Response: reasoningText}, nil
			}
			// Модель не ответила — возвращаем пустой ответ
			return FunctionCallResult{Success: true, Response: ""}, nil
		}

		// Логируем формат инструментов
		logger.DebugToFile("[TOOL] NATIVE format: detected %d tool calls", len(toolCalls))

		// Сохраняем сигнатуры выполненных инструментов
		for _, tc := range toolCalls {
			sig := toolCallSignature(tc)
			executedToolCalls[sig] = true
		}

		logger.DebugToFile("[FLOW] Calling executeAllTools with %d tool calls", len(toolCalls))
		result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())
		logger.DebugToFile("[FLOW] executeAllTools returned %d results", len(result.ToolCalls))
		if len(result.ToolCalls) > 0 {
			finalResponse, err := a.processToolResults(ctx, messages, "", toolCalls, result.ToolCalls, session, executedToolCalls)
			if err != nil {
				return FunctionCallResult{}, fmt.Errorf("process tool results: %w", err)
			}
			// processToolResults уже обработал всё включая XML - возвращаем результат
			return FunctionCallResult{Success: true, Response: finalResponse}, nil
		}
		logger.DebugToFile("[FLOW] No tool results, continuing...")
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
					for _, tc := range toolCalls {
						executedToolCalls[toolCallSignature(tc)] = true
					}
					finalResponse, err := a.processToolResults(ctx, messages, parsedReasoning.Content, toolCalls, result.ToolCalls, session, executedToolCalls)
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
		logger.DebugToFile("[THINKING] Sending %d chars of reasoning to thinking chat", len(cleanedReasoning))
		if err := a.thinkingCallback(session.GetPeerID(), cleanedReasoning); err != nil {
			fmt.Printf("[WARN] Failed to send thinking message: %v\n", err)
			logger.DebugToFile("[THINKING] Failed to send: %v", err)
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
		// Fallback — если LLM не ответил текстом, возвращаем пустой ответ
		if responseText == "" {
			return FunctionCallResult{Success: true, Response: ""}, nil
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
	for _, tc := range toolCalls {
		executed[toolCallSignature(tc)] = true
	}
	result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())

	if len(result.ToolCalls) > 0 {
		// Всегда отправляем результаты модели - даже если были ошибки
		// Это позволит модели понять что пошло не так и исправить запрос
		finalResponse, err := a.processToolResults(ctx, messages, parsed.Content, toolCalls, result.ToolCalls, session, executed)
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
	executed := make(map[string]bool)
	for _, tc := range toolCalls {
		executed[toolCallSignature(tc)] = true
	}

	fmt.Printf("[TOOL] XML fallback: detected %d tool calls in response text\n", len(toolCalls))

	result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())
	if len(result.ToolCalls) > 0 {
		// Проверяем что не все инструменты завершились с ошибкой
		allFailed := true
		for _, tr := range result.ToolCalls {
			if !tr.IsError {
				allFailed = false
				break
			}
		}
		if allFailed {
			// Все инструменты завершились с ошибкой — возвращаем что не использовали fallback
			return FunctionCallResult{}, false, nil
		}

		// Отправляем результаты модели
		finalResponse, err := a.processToolResults(ctx, messages, parsed.Content, toolCalls, result.ToolCalls, session, executed)
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
	executed := make(map[string]bool)
	for _, tc := range toolCalls {
		executed[toolCallSignature(tc)] = true
	}

	fmt.Printf("[TOOL] JSON fallback: detected %d tool calls in response text\n", len(toolCalls))

	result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())
	if len(result.ToolCalls) > 0 {
		// Всегда отправляем результаты модели - даже если были ошибки
		finalResponse, err := a.processToolResults(ctx, messages, parsed.Content, toolCalls, result.ToolCalls, session, executed)
		if err != nil {
			return FunctionCallResult{}, true, fmt.Errorf("process json tool results: %w", err)
		}
		return FunctionCallResult{Success: true, Response: finalResponse}, true, nil
	}

	return FunctionCallResult{}, false, nil
}

// processToolResults отправляет результат выполнения инструментов обратно в AI
// Поддерживает как NATIVE (OpenAI format), так и XML/JSON tool calls в ответе
// executed — карта сигнатур уже выполненных инструментов (для дедупликации между рекурсиями)
func (a *agentImpl) processToolResults(ctx context.Context, originalMessages []Message, assistantContent string, toolCalls []ToolCall, toolResults []ToolCallResult, session *sess.Session, executed map[string]bool) (string, error) {
	if a.config.LlamaServerURL == "" {
		return "", nil
	}

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
	session.AddAssistantMessageWithToolCalls(assistantContent, sessionToolCalls)

	// Сохраняем результаты инструментов в историю сессии
	for _, tr := range toolResults {
		session.AddToolMessage(tr.ToolCallID, tr.ToolName, tr.Content)
	}

	// Формируем сообщения для API
	messages := make([]Message, len(originalMessages))
	copy(messages, originalMessages)

	// Assistant message с tool_calls
	reqToolCalls := make([]ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		reqToolCalls[i] = buildToolCallForRequest(tc)
	}
	messages = append(messages, Message{
		Role:      "assistant",
		Content:   assistantContent,
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

	// Отправляем запрос в LLM
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

	logger.DebugToFile("\n--------------------------------")
	logger.DebugToFile("processToolResults: content=%d, reasoning=%d, tool_calls=%d, finish=%q",
		len(responseText), len(reasoningText), len(streamToolCalls), finishReason)

	// Если модель вернула новые NATIVE tool_calls — выполняем их рекурсивно
	if len(streamToolCalls) > 0 {
		fmt.Printf("[TOOL] NATIVE format: detected %d tool calls in tool results response\n", len(streamToolCalls))
		for _, tc := range streamToolCalls {
			executed[toolCallSignature(tc)] = true
		}
		result := a.executeAllTools(ctx, streamToolCalls, session.GetPeerID())
		if len(result.ToolCalls) > 0 {
			return a.processToolResults(ctx, messages, "", streamToolCalls, result.ToolCalls, session, executed)
		}
	}

	// Проверяем на XML/JSON tool calls в responseText и reasoningText
	textToCheck := responseText
	if len(reasoningText) > len(textToCheck) {
		textToCheck = reasoningText
	}

	if len(textToCheck) > 500 {
		textToCheck = textToCheck[:500] + "..."
	}
	logger.DebugToFile("processToolResults: textToCheck preview: %q", textToCheck)
	logger.DebugToFile("--------------------------------")

	parsed := ParseXMLToolCalls(textToCheck)
	logger.DebugToFile("processToolResults: parsed %d XML tool calls from textToCheck", len(parsed.ToolCalls))

	var jsonParsed XMLParseResult

	if len(parsed.ToolCalls) == 0 && responseText != reasoningText {
		parsed = ParseXMLToolCalls(responseText)
	}

	if len(parsed.ToolCalls) > 0 {
		fmt.Printf("[TOOL] XML fallback: detected %d tool calls in tool results response\n", len(parsed.ToolCalls))
		toolCalls := convertXMLToolCalls(parsed.ToolCalls)
		// Фильтруем дубли уже выполненных
		var uniqueCalls []ToolCall
		for _, tc := range toolCalls {
			sig := toolCallSignature(tc)
			if executed[sig] {
				fmt.Printf("[TOOL] XML duplicate skipped in tool results: %s\n", tc.Function.Name)
				continue
			}
			executed[sig] = true
			uniqueCalls = append(uniqueCalls, tc)
		}
		if len(uniqueCalls) == 0 {
			goto sendThinking
		}
		result := a.executeAllTools(ctx, uniqueCalls, session.GetPeerID())
		if len(result.ToolCalls) > 0 {
			return a.processToolResults(ctx, messages, parsed.Content, uniqueCalls, result.ToolCalls, session, executed)
		}
	}

	// JSON fallback в tool results response
	jsonParsed = ParseJSONToolCalls(responseText)
	if len(jsonParsed.ToolCalls) > 0 {
		fmt.Printf("[TOOL] JSON fallback: detected %d tool calls in tool results response\n", len(jsonParsed.ToolCalls))
		toolCalls := convertXMLToolCalls(jsonParsed.ToolCalls)
		var uniqueCalls []ToolCall
		for _, tc := range toolCalls {
			sig := toolCallSignature(tc)
			if executed[sig] {
				fmt.Printf("[TOOL] JSON duplicate skipped in tool results: %s\n", tc.Function.Name)
				continue
			}
			executed[sig] = true
			uniqueCalls = append(uniqueCalls, tc)
		}
		if len(uniqueCalls) > 0 {
			result := a.executeAllTools(ctx, uniqueCalls, session.GetPeerID())
			if len(result.ToolCalls) > 0 {
				return a.processToolResults(ctx, messages, jsonParsed.Content, uniqueCalls, result.ToolCalls, session, executed)
			}
		}
	}

sendThinking:

	// Отправляем очищенный reasoning в thinking
	cleanedReasoning := reasoningText
	if reasoningText != "" {
		parsedReasoning := ParseXMLToolCalls(reasoningText)
		if len(parsedReasoning.ToolCalls) > 0 {
			cleanedReasoning = parsedReasoning.Content
		}
	}
	if cleanedReasoning != "" && a.thinkingCallback != nil {
		logger.DebugToFile("[THINKING] Sending %d chars of reasoning to thinking chat", len(cleanedReasoning))
		if err := a.thinkingCallback(session.GetPeerID(), cleanedReasoning); err != nil {
			fmt.Printf("[WARN] Failed to send thinking message: %v\n", err)
			logger.DebugToFile("[THINKING] Failed to send: %v", err)
		}
	}

	// Если модель не вернула текст — возвращаем пустую строку
	if responseText == "" {
		return "", nil
	}

	session.AddAssistantMessage(responseText)
	return responseText, nil
}

// convertXMLToolCalls конвертирует XMLToolCall в ToolCall для executeAllTools
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
	logger.DebugToFile("[LLM RESPONSE] Starting to collect stream response...")
	var fullResponse strings.Builder
	var fullReasoning strings.Builder

	for event := range chunkChan {
		if event.IsError {
			logger.DebugToFile("[LLM RESPONSE] Stream error: %s", event.Content)
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
	logger.DebugToFile("[LLM RESPONSE] Collected: content=%d chars, reasoning=%d chars", len(response), len(reasoning))
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

	// Сохраняем ответ в debug файл если включён debug mode
	a.saveDebugResponse(response, reasoning, finishReason, allToolCalls)

	return response, reasoning, finishReason, allToolCalls, nil
}

// saveDebugResponse сохраняет последний ответ модели в debug_response.txt
func (a *agentImpl) saveDebugResponse(content, reasoning, finishReason string, toolCalls []ToolCall) {
	if !a.config.Debug {
		return
	}

	var sb strings.Builder
	sb.WriteString("=== LLM Response Debug ===\n\n")
	sb.WriteString(fmt.Sprintf("Finish Reason: %s\n\n", finishReason))
	sb.WriteString(fmt.Sprintf("Content (%d chars):\n", len(content)))
	sb.WriteString("---\n")
	sb.WriteString(content)
	sb.WriteString("\n---\n\n")
	sb.WriteString(fmt.Sprintf("Reasoning (%d chars):\n", len(reasoning)))
	sb.WriteString("---\n")
	sb.WriteString(reasoning)
	sb.WriteString("\n---\n\n")
	sb.WriteString(fmt.Sprintf("Tool Calls: %d\n", len(toolCalls)))
	for i, tc := range toolCalls {
		sb.WriteString(fmt.Sprintf("  %d. %s: %s\n", i+1, tc.Function.Name, ToolCallArgumentsStr(tc)))
	}

	if err := os.WriteFile("debug_response.txt", []byte(sb.String()), 0644); err != nil {
		fmt.Printf("[DEBUG] Failed to write debug_response.txt: %v\n", err)
	}
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
	logger.DebugToFile("[executeAllTools] Starting with %d tool calls", len(toolCalls))
	result := FunctionCallResult{
		Success:   true,
		ToolCalls: make([]ToolCallResult, 0),
	}

	for i, tc := range toolCalls {
		logger.DebugToFile("[executeAllTools] Executing tool %d/%d: %s", i+1, len(toolCalls), ToolCallName(tc))
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
	reqBody := a.buildBaseRequestJSON(a.config.Model, messages, false)

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
		path := args["path"]
		offset := args["offset"]
		limit := args["limit"]
		if offset != "" || limit != "" {
			return fmt.Sprintf("read_file(%q, offset=%s, limit=%s)", truncateStr(path, 60), offset, limit)
		}
		return fmt.Sprintf("read_file(%q)", truncateStr(path, 60))
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
		fmt.Printf("[TOOL] Result: %s success\n", toolName)
		a.sendThinking(peerID, "[TOOL] Result: "+toolName+" success")
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
