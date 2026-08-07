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
	responseText, reasoningText, finishReason, streamToolCalls, promptTokens, completionTokens, err := a.collectStreamAndLog(ctx, messages)
	if err != nil {
		return FunctionCallResult{}, err
	}

	// Отправляем reasoning в thinking чат сразу после получения,
	// чтобы он не терялся при обработке tool_calls
	a.sendThinkingIfNeeded(session, reasoningText)

	// Отправляем количество токенов после ответа LLM
	a.sendThinkingTokens(session.GetPeerID(), promptTokens, completionTokens)

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
func (a *agentImpl) collectStreamAndLog(ctx context.Context, messages []Message) (string, string, string, []ToolCall, int, int, error) {
	toolsSchema := a.toolsRegistry.ToOpenAISchema()
	streamConfig := a.buildToolsStreamConfig(toolsSchema)

	responseText, reasoningText, finishReason, streamToolCalls, promptTokens, completionTokens, err := a.streamAndCollect(ctx, streamConfig, messages)
	if err != nil {
		return "", "", "", nil, 0, 0, err
	}

	prefix := a.agentPrefix()
	logger.DebugToFile("%sstreaming response: content=%d, reasoning=%d, tool_calls=%d, finish=%q, in=%d, out=%d",
		prefix, len(responseText), len(reasoningText), len(streamToolCalls), finishReason, promptTokens, completionTokens)
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

	return responseText, reasoningText, finishReason, streamToolCalls, promptTokens, completionTokens, nil
}

// handleNativeToolCalls обрабатывает NATIVE tool_calls из streaming ответа
// Возвращает nil если нет нативных tool_calls или их не удалось выполнить
func (a *agentImpl) handleNativeToolCalls(ctx context.Context, messages []Message, session *sess.Session,
	responseText, reasoningText, finishReason string, streamToolCalls []ToolCall, executedToolCalls map[string]bool) (*FunctionCallResult, error) {

	prefix := a.agentPrefix()
	if finishReason != "tool_calls" && len(streamToolCalls) == 0 {
		return nil, nil
	}

	toolCalls := streamToolCalls
	logger.DebugToFile("[FLOW] Entering tool_calls branch: finishReason=%q, len(streamToolCalls)=%d", finishReason, len(streamToolCalls))

	// Предупреждение: finish="" с tool_calls — возможна обрезка стрима
	if finishReason == "" && len(toolCalls) > 0 {
		fmt.Printf("%s[TOOL] WARNING: finish_reason is empty, tool calls may be truncated/incomplete\n", prefix)
		logger.DebugToFile("[TOOL] WARNING: finish_reason is empty (stream may be truncated)")
	}

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
		return &FunctionCallResult{Success: true, Response: ""}, nil
	}

	logger.DebugToFile("%s[TOOL] NATIVE format: detected %d tool calls", prefix, len(toolCalls))
	for _, tc := range toolCalls {
		sig := toolCallSignature(tc)
		executedToolCalls[sig] = true
	}

	logger.DebugToFile("%s[FLOW] Calling executeAllTools with %d tool calls", prefix, len(toolCalls))
	result := a.executeAllTools(ctx, toolCalls, session.GetPeerID())
	logger.DebugToFile("%s[FLOW] executeAllTools returned %d results", prefix, len(result.ToolCalls))
	if len(result.ToolCalls) > 0 {
		finalResponse, err := a.processToolResults(ctx, messages, "", toolCalls, result.ToolCalls, session, executedToolCalls)
		if err != nil {
			return nil, fmt.Errorf("process tool results: %w", err)
		}
		return &FunctionCallResult{Success: true, Response: finalResponse}, nil
	}

	logger.DebugToFile("%s[FLOW] No tool results, continuing...", prefix)
	return nil, nil
}

// handleXMLInReasoning проверяет reasoningText на наличие XML tool calls
// Возвращает результат если нашли и выполнили, иначе nil
func (a *agentImpl) handleXMLInReasoning(ctx context.Context, messages []Message, session *sess.Session, reasoningText string, executedToolCalls map[string]bool) *FunctionCallResult {
	prefix := a.agentPrefix()
	if reasoningText == "" {
		return nil
	}

	parsedReasoning := ParseXMLToolCalls(reasoningText)
	if len(parsedReasoning.ToolCalls) == 0 {
		return nil
	}

	fmt.Printf("%s[TOOL] XML in reasoning: detected %d tool calls\n", prefix, len(parsedReasoning.ToolCalls))

	var uniqueCalls []XMLToolCall
	for _, tc := range parsedReasoning.ToolCalls {
		sig := xmlToolCallSignature(tc)
		if !executedToolCalls[sig] {
			uniqueCalls = append(uniqueCalls, tc)
		} else {
			fmt.Printf("%s[TOOL] XML duplicate skipped: %s\n", prefix, tc.Name)
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
		fmt.Printf("%s[ERROR] process xml tool results: %v\n", prefix, err)
		return nil
	}
	return &FunctionCallResult{Success: true, Response: finalResponse}
}

// hasPartialToolCall проверяет, содержит ли текст фрагменты tool call XML
// без валидной структуры (напр. </tool_call> без открывающего, <parameter=...> вне контекста)
// Возвращает false для полностью валидных <tool_call><function=...>...</function></tool_call>
func hasPartialToolCall(text string) bool {
	if text == "" {
		return false
	}
	if strings.Contains(text, "</tool_call>") && !strings.Contains(text, "<tool_call>") {
		return true
	}
	if strings.Contains(text, "</function>") && !strings.Contains(text, "<function") {
		return true
	}
	if strings.Contains(text, "<tool_call") && !strings.Contains(text, "<tool_call>") {
		return true
	}
	// <tool_call> без </tool_call> или больше открывающих чем закрывающих — unclosed, partial
	if strings.Contains(text, "<tool_call>") {
		openCount := strings.Count(text, "<tool_call>")
		closeCount := strings.Count(text, "</tool_call>")
		if openCount > closeCount {
			return true
		}
	}
	// <parameter=...> или <parameter ...> вне контекста tool_call — partial
	if (strings.Contains(text, "<parameter=") || strings.Contains(text, "<parameter ")) && !strings.Contains(text, "<tool_call>") {
		return true
	}
	// <function=...> без полного tool_call контекста
	if strings.Contains(text, "<function=") && !strings.Contains(text, "<tool_call>") {
		return true
	}
	return false
}

// stripPartialToolCall удаляет только фрагменты tool call XML тегов из текста,
// не затрагивая окружающий текст. Работает на тексте, уже очищенном ParseXMLToolCalls.
// Удаляет: </tool_call>, </function>, </parameter>, <parameter=...>, <function=...>
func stripPartialToolCall(text string) string {
	result := text
	// Удаляем закрывающие теги
	for _, tag := range []string{"</tool_call>", "</function>", "</parameter>"} {
		result = strings.ReplaceAll(result, tag, "")
	}
	// Удаляем <parameter=...> целиком (до >)
	for {
		start := strings.Index(result, "<parameter=")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], ">")
		if end < 0 {
			result = result[:start]
			break
		}
		result = result[:start] + result[start+end+1:]
	}
	// Удаляем <function=...> целиком (до >)
	for {
		start := strings.Index(result, "<function=")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], ">")
		if end < 0 {
			result = result[:start]
			break
		}
		result = result[:start] + result[start+end+1:]
	}
	// Удаляем пустые строки от удалённых строк
	result = strings.ReplaceAll(result, "\n\n", "\n")
	result = strings.ReplaceAll(result, "\n\n", "\n")
	return strings.TrimSpace(result)
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

	// Дополнительно очищаем от partial tool call фрагментов
	if hasPartialToolCall(cleanedReasoning) {
		prefix := a.agentPrefix()
		fmt.Print(prefix + "[TOOL] Stripped partial/malformed tool call fragments from reasoning\n")
		logger.DebugToFile("%s[THINKING] Stripping partial tool call fragments from reasoning", prefix)
		cleanedReasoning = stripPartialToolCall(cleanedReasoning)
		if cleanedReasoning == "" {
			return
		}
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
	if responseText == "" && reasoningText != "" && strings.Contains(reasoningText, "<tool_call>") && finishReason != "" {
		reasoningSnippet := truncateStr(reasoningText, 300)
		a.debugLog.Error("[TOOL] Invalid XML tool call in reasoning: %s", reasoningSnippet)
		a.sendThinking(session.GetPeerID(), "[TOOL] Error: malformed XML tool call in reasoning, sending corrective feedback")
		result, err := a.handleInvalidXMLToolCall(ctx, messages, session, executedToolCalls)
		if err != nil {
			return nil, fmt.Errorf("handle invalid xml: %w", err)
		}
		return &result, nil
	}

	// Проверяем reasoning на partial/fragmented tool call XML (</tool_call>, <parameter=...> и т.п.)
	if responseText == "" && reasoningText != "" && hasPartialToolCall(reasoningText) && !strings.Contains(reasoningText, "<tool_call>") && finishReason != "" {
		prefix := a.agentPrefix()
		snippet := truncateStr(reasoningText, 200)
		fmt.Printf("%s[TOOL] Partial/malformed tool call fragments in reasoning: %s\n", prefix, snippet)
		logger.DebugToFile("%s[TOOL] Partial tool call fragments in reasoning: %s", prefix, snippet)
		a.debugLog.Error("[TOOL] Partial/malformed tool call fragments in reasoning")
		a.sendThinking(session.GetPeerID(), "[TOOL] Error: partial/incomplete tool call in reasoning, sending corrective feedback")

		// Очищаем reasoning от фрагментов и отправляем как thinking
		cleanedReasoning := stripPartialToolCall(reasoningText)
		if cleanedReasoning != "" {
			logger.DebugToFile("%s[THINKING] Sending cleaned reasoning (stripped partial fragments)", prefix)
			if err := a.thinkingCallback(session.GetPeerID(), cleanedReasoning); err != nil {
				fmt.Printf("%s[WARN] Failed to send thinking: %v\n", prefix, err)
			}
		}

		result, err := a.handleInvalidXMLToolCall(ctx, messages, session, executedToolCalls)
		if err != nil {
			return nil, fmt.Errorf("handle partial tool call: %w", err)
		}
		return &result, nil
	}

	// Если текст содержит битый <tool_call> без валидных инструментов
	// — модель сгенерировала неверный XML, отправляем ошибку формата
	if strings.Contains(responseText, "<tool_call") {
		stripped := ParseXMLToolCalls(responseText)
		if len(stripped.ToolCalls) == 0 {
			respSnippet := truncateStr(responseText, 300)
			a.debugLog.Error("[TOOL] Invalid XML tool call in response (no valid tools parsed): %s", respSnippet)
			a.sendThinking(session.GetPeerID(), "[TOOL] Error: invalid XML tool call format, sending corrective feedback")
			result, err := a.handleInvalidXMLToolCall(ctx, messages, session, executedToolCalls)
			if err != nil {
				return nil, fmt.Errorf("handle invalid xml: %w", err)
			}
			return &result, nil
		}
	}

	if !a.isNonToolResponse(finishReason) {
		return nil, nil
	}

	if responseText == "" {
		if a.config.Debug {
			fmt.Printf("[DEBUG] Empty response from LLM\n")
		}
		return &FunctionCallResult{Success: true, Response: ""}, nil
	}

	parsedResp := ParseXMLToolCalls(responseText)
	cleanText := parsedResp.Content
	cleanText = a.stripThinkingTags(cleanText, session.GetPeerID())
	if cleanText != "" {
		session.AddAssistantMessage(cleanText)
	}
	return &FunctionCallResult{Success: true, Response: cleanText}, nil
}

// makeTextResponse создаёт текстовый ответ с очисткой XML тегов
func (a *agentImpl) makeTextResponse(responseText string, session *sess.Session) FunctionCallResult {
	parsedResp := ParseXMLToolCalls(responseText)
	responseText = parsedResp.Content
	responseText = a.stripThinkingTags(responseText, session.GetPeerID())
	if responseText != "" {
		session.AddAssistantMessage(responseText)
	}
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
	prefix := a.agentPrefix()
	if len(parsed.ToolCalls) == 0 {
		return FunctionCallResult{}, false, nil
	}

	var uniqueCalls []XMLToolCall
	for _, tc := range parsed.ToolCalls {
		sig := xmlToolCallSignature(tc)
		if executed[sig] {
			fmt.Printf("%s[TOOL] XML duplicate skipped: %s\n", prefix, tc.Name)
			continue
		}
		uniqueCalls = append(uniqueCalls, tc)
	}

	if len(uniqueCalls) == 0 {
		return FunctionCallResult{}, false, nil
	}

	fmt.Printf("%s[TOOL] XML fallback: detected %d tool calls (%d duplicates skipped)\n", prefix, len(uniqueCalls), len(parsed.ToolCalls)-len(uniqueCalls))

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
	prefix := a.agentPrefix()
	parsed := ParseXMLToolCalls(responseText)
	if len(parsed.ToolCalls) == 0 {
		return FunctionCallResult{}, false, nil
	}

	toolCalls := convertXMLToolCalls(parsed.ToolCalls)
	executed := make(map[string]bool)
	for _, tc := range toolCalls {
		executed[toolCallSignature(tc)] = true
	}

	fmt.Printf("%s[TOOL] XML fallback: detected %d tool calls in response text\n", prefix, len(toolCalls))

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
	prefix := a.agentPrefix()
	parsed := ParseJSONToolCalls(responseText)
	if len(parsed.ToolCalls) == 0 {
		return FunctionCallResult{}, false, nil
	}

	toolCalls := convertXMLToolCalls(parsed.ToolCalls)
	executed := make(map[string]bool)
	for _, tc := range toolCalls {
		executed[toolCallSignature(tc)] = true
	}

	fmt.Printf("%s[TOOL] JSON fallback: detected %d tool calls in response text\n", prefix, len(toolCalls))

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
