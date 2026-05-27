package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/opencode/llama-client/pkg/logger"
	sess "github.com/opencode/llama-client/session"
)

// ============================================================
// Function Calling — оркестрация вызовов инструментов
// ============================================================

// maxReasoningLength — максимальная длина reasoningText после обрезки.
// Предотвращает передачу зацикленного/мусорного reasoning (50k+ chars).
const maxReasoningLength = 5000

// FunctionCallResult представляет результат function calling
type FunctionCallResult struct {
	Success   bool
	Response  string
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

	responseText, reasoningText, finishReason, streamToolCalls, err := a.collectStreamResponseWithToolCalls(chunkChan)
	if err != nil {
		return FunctionCallResult{}, err
	}

	prefix := a.agentPrefix()
	logger.DebugToFile("%sstreaming response: content=%d, reasoning=%d, tool_calls=%d, finish=%q",
		prefix, len(responseText), len(reasoningText), len(streamToolCalls), finishReason)
	if len(responseText) > 0 {
		logger.DebugToFile("\n--------------------------------")
		logger.DebugToFile("%scontent: %s", prefix, responseText)
		logger.DebugToFile("--------------------------------")
	}
	if len(reasoningText) > maxReasoningLength {
		fmt.Printf("[WARN] %sreasoningText too long (%d chars), truncating to %d\n", prefix, len(reasoningText), maxReasoningLength)
		logger.DebugToFile("%sreasoningText too long (%d chars), truncating to %d", prefix, len(reasoningText), maxReasoningLength)
		reasoningText = reasoningText[:maxReasoningLength]
	}
	if len(reasoningText) > 0 {
		logger.DebugToFile("%sreasoning: %s", prefix, reasoningText)
	}

	executedToolCalls := make(map[string]bool)

	// 1. NATIVE tool_calls
	if finishReason == "tool_calls" || len(streamToolCalls) > 0 {
		toolCalls := streamToolCalls
		logger.DebugToFile("[FLOW] Entering tool_calls branch: finishReason=%q, len(streamToolCalls)=%d", finishReason, len(streamToolCalls))
		if len(toolCalls) == 0 {
			fmt.Printf("[WARN] LLM returned tool_calls but none collected from stream, trying non-streaming\n")
			logger.DebugToFile("[FLOW] Trying non-streaming fallback")
			var err error
			toolCalls, err = a.getToolCallsFromResponse(ctx, messages, toolsSchema)
			if err != nil {
				fmt.Printf("[WARN] Non-streaming tool_calls failed: %v\n", err)
			}
		}
		if len(toolCalls) == 0 {
			logger.DebugToFile("[FLOW] No tool_calls after all attempts, returning empty")
			if reasoningText != "" {
				session.AddAssistantMessage(reasoningText)
				return FunctionCallResult{Success: true, Response: reasoningText}, nil
			}
			return FunctionCallResult{Success: true, Response: ""}, nil
		}

		logger.DebugToFile("[TOOL] NATIVE format: detected %d tool calls", len(toolCalls))

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
			return FunctionCallResult{Success: true, Response: finalResponse}, nil
		}
		logger.DebugToFile("[FLOW] No tool results, continuing...")
	}

	// 2. XML в reasoning
	cleanedReasoning := reasoningText
	if reasoningText != "" {
		parsedReasoning := ParseXMLToolCalls(reasoningText)
		cleanedReasoning = parsedReasoning.Content

		if len(parsedReasoning.ToolCalls) > 0 {
			fmt.Printf("[TOOL] XML in reasoning: detected %d tool calls\n", len(parsedReasoning.ToolCalls))

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
		}
	}

	// Отправляем очищенный reasoning в thinkingPeerID
	if cleanedReasoning != "" && a.thinkingCallback != nil {
		logger.DebugToFile("%s[THINKING] Sending %d chars of reasoning to thinking chat", a.agentPrefix(), len(cleanedReasoning))
		if err := a.thinkingCallback(session.GetPeerID(), cleanedReasoning); err != nil {
			fmt.Printf("%s[WARN] Failed to send thinking message: %v\n", a.agentPrefix(), err)
			logger.DebugToFile("%s[THINKING] Failed to send: %v", a.agentPrefix(), err)
		}
	}

	// 3. XML fallback в response/reasoning
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

	// 4. JSON fallback
	if result, used, err := a.jsonFallback(ctx, responseText, messages, session); used {
		if err != nil {
			return FunctionCallResult{}, fmt.Errorf("json fallback: %w", err)
		}
		return result, nil
	}

	// 5. Проверка на сломанный XML Tool Call в reasoning
	// Если модель вернула reasoning с <tool_call> блоками, но они не распарсились
	// как валидные XML tool calls — значит формат битый, отправляем модели ошибку
	if responseText == "" && reasoningText != "" && strings.Contains(reasoningText, "<tool_call>") {
		logger.DebugToFile("%smalformed <tool_call> detected in reasoning, calling handleInvalidXMLToolCall", a.agentPrefix())
		return a.handleInvalidXMLToolCall(ctx, messages, session, executedToolCalls)
	}

	if responseText == "" || a.isNonToolResponse(finishReason) {
		if responseText == "" && cleanedReasoning != "" {
			logger.DebugToFile("%sresponseText is empty, using reasoning as response (%d chars)", a.agentPrefix(), len(cleanedReasoning))
			session.AddAssistantMessage(cleanedReasoning)
			return FunctionCallResult{Success: true, Response: cleanedReasoning}, nil
		}
		if responseText == "" {
			return FunctionCallResult{Success: true, Response: ""}, nil
		}
		parsedResp := ParseXMLToolCalls(responseText)
		responseText = parsedResp.Content
		session.AddAssistantMessage(responseText)
		return FunctionCallResult{
			Success:  true,
			Response: responseText,
		}, nil
	}

	parsedResp := ParseXMLToolCalls(responseText)
	responseText = parsedResp.Content

	session.AddAssistantMessage(responseText)
	return FunctionCallResult{
		Success:  true,
		Response: responseText,
	}, nil
}

// buildToolsStreamConfig создаёт конфигурацию для streaming с инструментами
func (a *agentImpl) buildToolsStreamConfig(toolsSchema []map[string]interface{}) StreamingConfig {
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

// xmlFallbackFiltered проверяет responseText на наличие XML tool calls,
// фильтрует дубли уже выполненных инструментов, выполняет оставшиеся
func (a *agentImpl) xmlFallbackFiltered(ctx context.Context, responseText string, messages []Message, session *sess.Session, executed map[string]bool) (FunctionCallResult, bool, error) {
	parsed := ParseXMLToolCalls(responseText)
	if len(parsed.ToolCalls) == 0 {
		return FunctionCallResult{}, false, nil
	}

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
		allFailed := true
		for _, tr := range result.ToolCalls {
			if !tr.IsError {
				allFailed = false
				break
			}
		}
		if allFailed {
			return FunctionCallResult{}, false, nil
		}

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
		finalResponse, err := a.processToolResults(ctx, messages, parsed.Content, toolCalls, result.ToolCalls, session, executed)
		if err != nil {
			return FunctionCallResult{}, true, fmt.Errorf("process json tool results: %w", err)
		}
		return FunctionCallResult{Success: true, Response: finalResponse}, true, nil
	}

	return FunctionCallResult{}, false, nil
}
