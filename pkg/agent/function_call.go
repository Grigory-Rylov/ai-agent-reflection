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
	responseText, reasoningText, finishReason, streamToolCalls, err := a.collectStreamAndLog(ctx, messages)
	if err != nil {
		return FunctionCallResult{}, err
	}

	executedToolCalls := make(map[string]bool)

	if result, err := a.handleNativeToolCalls(ctx, messages, session, responseText, reasoningText, finishReason, streamToolCalls, executedToolCalls); result != nil || err != nil {
		if err != nil {
			return FunctionCallResult{}, err
		}
		return *result, nil
	}

	cleanedResult := a.handleXMLInReasoning(ctx, messages, session, reasoningText, executedToolCalls)
	if cleanedResult != nil {
		return *cleanedResult, nil
	}

	a.sendThinkingIfNeeded(session, reasoningText)

	if result, err := a.handleXMLFallback(ctx, responseText, reasoningText, messages, session, executedToolCalls); result != nil || err != nil {
		if err != nil {
			return FunctionCallResult{}, err
		}
		return *result, nil
	}

	if result, err := a.handleJSONFallback(ctx, responseText, messages, session); result != nil || err != nil {
		if err != nil {
			return FunctionCallResult{}, err
		}
		return *result, nil
	}

	if result, err := a.handleInvalidOrTextResponse(ctx, messages, responseText, reasoningText, finishReason, session, executedToolCalls); result != nil || err != nil {
		if err != nil {
			return FunctionCallResult{}, err
		}
		return *result, nil
	}

	return a.makeTextResponse(responseText, session), nil
}

// collectStreamAndLog отправляет streaming запрос, собирает ответ и логирует
func (a *agentImpl) collectStreamAndLog(ctx context.Context, messages []Message) (string, string, string, []ToolCall, error) {
	toolsSchema := a.toolsRegistry.ToOpenAISchema()
	streamConfig := a.buildToolsStreamConfig(toolsSchema)

	chunkChan, err := a.streamingRequest(ctx, streamConfig, messages)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("streaming request: %w", err)
	}

	responseText, reasoningText, finishReason, streamToolCalls, err := a.collectStreamResponseWithToolCalls(chunkChan)
	if err != nil {
		return "", "", "", nil, err
	}

	prefix := a.agentPrefix()
	logger.DebugToFile("%sstreaming response: content=%d, reasoning=%d, tool_calls=%d, finish=%q",
		prefix, len(responseText), len(reasoningText), len(streamToolCalls), finishReason)
	if len(responseText) > 0 {
		logger.DebugToFile("\n---------------- response content ----------------------")
		logger.DebugToFile("%s%s", prefix, responseText)
	}
	if len(reasoningText) > maxReasoningLength {
		fmt.Printf("[WARN] %sreasoningText too long (%d chars), truncating to %d\n", prefix, len(reasoningText), maxReasoningLength)
		logger.DebugToFile("%sreasoningText too long (%d chars), truncating to %d", prefix, len(reasoningText), maxReasoningLength)
		reasoningText = reasoningText[:maxReasoningLength]
	}
	if len(reasoningText) > 0 {
		logger.DebugToFile("\n---------------- response reasoning ------------------")
		logger.DebugToFile("%s%s", prefix, reasoningText)
	}
	logger.DebugToFile("\n====================================================")

	return responseText, reasoningText, finishReason, streamToolCalls, nil
}

// handleNativeToolCalls обрабатывает NATIVE tool_calls из streaming ответа
// Возвращает nil если нет нативных tool_calls или их не удалось выполнить
func (a *agentImpl) handleNativeToolCalls(ctx context.Context, messages []Message, session *sess.Session,
	responseText, reasoningText, finishReason string, streamToolCalls []ToolCall, executedToolCalls map[string]bool) (*FunctionCallResult, error) {

	if finishReason != "tool_calls" && len(streamToolCalls) == 0 {
		return nil, nil
	}

	toolCalls := streamToolCalls
	logger.DebugToFile("[FLOW] Entering tool_calls branch: finishReason=%q, len(streamToolCalls)=%d", finishReason, len(streamToolCalls))
	if len(toolCalls) == 0 {
		fmt.Printf("[WARN] LLM returned tool_calls but none collected from stream, trying non-streaming\n")
		logger.DebugToFile("[FLOW] Trying non-streaming fallback")
		var err error
		toolCalls, err = a.getToolCallsFromResponse(ctx, messages, a.toolsRegistry.ToOpenAISchema())
		if err != nil {
			fmt.Printf("[WARN] Non-streaming tool_calls failed: %v\n", err)
		}
	}
	if len(toolCalls) == 0 {
		logger.DebugToFile("[FLOW] No tool_calls after all attempts, returning empty")
		if reasoningText != "" {
			session.AddAssistantMessage(reasoningText)
			return &FunctionCallResult{Success: true, Response: reasoningText}, nil
		}
		return &FunctionCallResult{Success: true, Response: ""}, nil
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
			return nil, fmt.Errorf("process tool results: %w", err)
		}
		return &FunctionCallResult{Success: true, Response: finalResponse}, nil
	}

	logger.DebugToFile("[FLOW] No tool results, continuing...")
	return nil, nil
}

// handleXMLInReasoning проверяет reasoningText на наличие XML tool calls
// Возвращает результат если нашли и выполнили, иначе nil
func (a *agentImpl) handleXMLInReasoning(ctx context.Context, messages []Message, session *sess.Session, reasoningText string, executedToolCalls map[string]bool) *FunctionCallResult {
	if reasoningText == "" {
		return nil
	}

	parsedReasoning := ParseXMLToolCalls(reasoningText)
	if len(parsedReasoning.ToolCalls) == 0 {
		return nil
	}

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

	if len(uniqueCalls) == 0 {
		return nil
	}

	toolCalls := convertXMLToolCalls(uniqueCalls)
	result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())
	if len(result.ToolCalls) == 0 {
		return nil
	}

	for _, tc := range toolCalls {
		executedToolCalls[toolCallSignature(tc)] = true
	}
	finalResponse, err := a.processToolResults(ctx, messages, parsedReasoning.Content, toolCalls, result.ToolCalls, session, executedToolCalls)
	if err != nil {
		fmt.Printf("[ERROR] process xml tool results: %v\n", err)
		return nil
	}
	return &FunctionCallResult{Success: true, Response: finalResponse}
}

// sendThinkingIfNeeded отправляет очищенный reasoning в thinking чат
func (a *agentImpl) sendThinkingIfNeeded(session *sess.Session, reasoningText string) {
	if a.thinkingCallback == nil {
		return
	}
	parsed := ParseXMLToolCalls(reasoningText)
	cleanedReasoning := parsed.Content
	if cleanedReasoning == "" {
		return
	}

	logger.DebugToFile("%s[THINKING] Sending %d chars of reasoning to thinking chat", a.agentPrefix(), len(cleanedReasoning))
	if err := a.thinkingCallback(session.GetPeerID(), cleanedReasoning); err != nil {
		fmt.Printf("%s[WARN] Failed to send thinking message: %v\n", a.agentPrefix(), err)
		logger.DebugToFile("%s[THINKING] Failed to send: %v", a.agentPrefix(), err)
	}
}

// handleXMLFallback ищет XML tool calls в responseText и reasoningText
func (a *agentImpl) handleXMLFallback(ctx context.Context, responseText, reasoningText string, messages []Message, session *sess.Session, executedToolCalls map[string]bool) (*FunctionCallResult, error) {
	textToCheck := responseText
	if len(reasoningText) > len(textToCheck) {
		textToCheck = reasoningText
		logger.DebugToFile("Using reasoningText for XML check (%d chars)", len(reasoningText))
	}

	xmlResult, xmlUsed, err := a.xmlFallbackFiltered(ctx, textToCheck, messages, session, executedToolCalls)
	if xmlUsed {
		if err != nil {
			return nil, fmt.Errorf("xml fallback: %w", err)
		}
		return &xmlResult, nil
	}
	// Если проверяли reasoningText и не нашли — пробуем responseText
	if !xmlUsed && textToCheck != responseText {
		xmlResult, xmlUsed, err = a.xmlFallbackFiltered(ctx, responseText, messages, session, executedToolCalls)
		if xmlUsed {
			if err != nil {
				return nil, fmt.Errorf("xml fallback response: %w", err)
			}
			return &xmlResult, nil
		}
	}

	return nil, nil
}

// handleJSONFallback ищет JSON tool calls в responseText
func (a *agentImpl) handleJSONFallback(ctx context.Context, responseText string, messages []Message, session *sess.Session) (*FunctionCallResult, error) {
	if result, used, err := a.jsonFallback(ctx, responseText, messages, session); used {
		if err != nil {
			return nil, fmt.Errorf("json fallback: %w", err)
		}
		return &result, nil
	}
	return nil, nil
}

// handleInvalidOrTextResponse обрабатывает финальный текстовый ответ или ошибки формата
func (a *agentImpl) handleInvalidOrTextResponse(ctx context.Context, messages []Message, responseText, reasoningText, finishReason string, session *sess.Session, executedToolCalls map[string]bool) (*FunctionCallResult, error) {
	if responseText == "" && reasoningText != "" && strings.Contains(reasoningText, "<tool_call>") {
		logger.DebugToFile("%smalformed <tool_call> detected in reasoning", a.agentPrefix())
		result, err := a.handleInvalidXMLToolCall(ctx, messages, session, executedToolCalls)
		if err != nil {
			return nil, fmt.Errorf("handle invalid xml: %w", err)
		}
		return &result, nil
	}

	if !a.isNonToolResponse(finishReason) {
		return nil, nil
	}

	if responseText == "" {
		parsed := ParseXMLToolCalls(reasoningText)
		if parsed.Content != "" {
			logger.DebugToFile("%sresponseText is empty, using reasoning as response (%d chars)", a.agentPrefix(), len(parsed.Content))
			session.AddAssistantMessage(parsed.Content)
			return &FunctionCallResult{Success: true, Response: parsed.Content}, nil
		}
		return &FunctionCallResult{Success: true, Response: ""}, nil
	}

	parsedResp := ParseXMLToolCalls(responseText)
	responseText = parsedResp.Content
	session.AddAssistantMessage(responseText)
	return &FunctionCallResult{Success: true, Response: responseText}, nil
}

// makeTextResponse создаёт текстовый ответ с очисткой XML тегов
func (a *agentImpl) makeTextResponse(responseText string, session *sess.Session) FunctionCallResult {
	parsedResp := ParseXMLToolCalls(responseText)
	responseText = parsedResp.Content
	session.AddAssistantMessage(responseText)
	return FunctionCallResult{Success: true, Response: responseText}
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
