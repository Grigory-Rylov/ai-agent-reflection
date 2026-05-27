package agent

import (
	"context"
	"fmt"

	"github.com/opencode/llama-client/pkg/logger"
	sess "github.com/opencode/llama-client/session"
)

// ============================================================
// ToolResultProcessor — обработка результатов инструментов
// ============================================================

// ToolResultProcessor определяет интерфейс для рекурсивной обработки результатов
type ToolResultProcessor interface {
	ProcessResults(ctx context.Context, originalMessages []Message, assistantContent string, toolCalls []ToolCall, toolResults []ToolCallResult, session *sess.Session, executed map[string]bool) (string, error)
}

// agentToolResultProcessor реализует ToolResultProcessor через агента
type agentToolResultProcessor struct {
	agent    *agentImpl
	executor ToolExecutor
}

func newAgentToolResultProcessor(a *agentImpl, executor ToolExecutor) *agentToolResultProcessor {
	return &agentToolResultProcessor{
		agent:    a,
		executor: executor,
	}
}

func (p *agentToolResultProcessor) ProcessResults(ctx context.Context, originalMessages []Message, assistantContent string, toolCalls []ToolCall, toolResults []ToolCallResult, session *sess.Session, executed map[string]bool) (string, error) {
	return p.agent.processToolResults(ctx, originalMessages, assistantContent, toolCalls, toolResults, session, executed)
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

	prefix := a.agentPrefix()
	logger.DebugToFile("\n--------------------------------")
	logger.DebugToFile("%sprocessToolResults: content=%d, reasoning=%d, tool_calls=%d, finish=%q",
		prefix, len(responseText), len(reasoningText), len(streamToolCalls), finishReason)
	if len(responseText) > 0 {
		logger.DebugToFile("%sresponse content: %s", prefix, responseText)
	}
	if len(reasoningText) > 0 {
		logger.DebugToFile("%sresponse reasoning: %s", prefix, reasoningText)
	}

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

	preview := textToCheck
	if len(preview) > 500 {
		preview = preview[:500] + "..."
	}
	logger.DebugToFile("processToolResults: textToCheck preview: %q", preview)
	logger.DebugToFile("--------------------------------")

	parsed := ParseXMLToolCalls(textToCheck)
	logger.DebugToFile("processToolResults: parsed %d XML tool calls from textToCheck", len(parsed.ToolCalls))

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
		if len(uniqueCalls) > 0 {
			result := a.executeAllTools(ctx, uniqueCalls, session.GetPeerID())
			if len(result.ToolCalls) > 0 {
				return a.processToolResults(ctx, messages, parsed.Content, uniqueCalls, result.ToolCalls, session, executed)
			}
		}
	}

	// JSON fallback в tool results response
	jsonParsed := ParseJSONToolCalls(responseText)
	if len(jsonParsed.ToolCalls) > 0 {
		fmt.Printf("[TOOL] JSON fallback: detected %d tool calls in tool results response\n", len(jsonParsed.ToolCalls))
		responseText = jsonParsed.Content

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

	// Отправляем очищенный reasoning в thinking
	cleanedReasoning := reasoningText
	if reasoningText != "" {
		parsedReasoning := ParseXMLToolCalls(reasoningText)
		cleanedReasoning = parsedReasoning.Content
	}
	if cleanedReasoning != "" && a.thinkingCallback != nil {
		logger.DebugToFile("%s[THINKING] Sending %d chars of reasoning to thinking chat", a.agentPrefix(), len(cleanedReasoning))
		if err := a.thinkingCallback(session.GetPeerID(), cleanedReasoning); err != nil {
			fmt.Printf("%s[WARN] Failed to send thinking message: %v\n", a.agentPrefix(), err)
			logger.DebugToFile("%s[THINKING] Failed to send: %v", a.agentPrefix(), err)
		}
	}

	// Safety net: вырезаем <tool_call> блоки из responseText перед сохранением
	if responseText != "" {
		parsedResp := ParseXMLToolCalls(responseText)
		responseText = parsedResp.Content
	}

	// Если модель не вернула content — используем очищенный reasoning
	if responseText == "" {
		if cleanedReasoning != "" {
			logger.DebugToFile("%sresponseText is empty, using reasoning as response (%d chars)", a.agentPrefix(), len(cleanedReasoning))
			session.AddAssistantMessage(cleanedReasoning)
			return cleanedReasoning, nil
		}
		return "", nil
	}

	session.AddAssistantMessage(responseText)
	return responseText, nil
}

// handleInvalidXMLToolCall обрабатывает случай когда модель отправила
// невалидный XML tool call (<tool_call> обёртку без <function=name> формата).
// Создаёт виртуальный tool call с ошибкой и отправляет модели через processToolResults.
func (a *agentImpl) handleInvalidXMLToolCall(ctx context.Context, messages []Message, session *sess.Session, executed map[string]bool) (FunctionCallResult, error) {
	errMsg := "ERROR: Invalid tool call format. You used <tool_call> XML format which is not supported. Use the native tool_calls format instead (function name and arguments in the tool_calls array). Do NOT use XML tags for tool calls."

	toolCall := ToolCall{
		ID:   "format_error",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "__format_error",
			Arguments: []byte(`{}`),
		},
	}
	toolResult := ToolCallResult{
		ToolCallID: "format_error",
		ToolName:   "__format_error",
		Content:    errMsg,
		IsError:    true,
	}

	executed[toolCallSignature(toolCall)] = true

	finalResponse, err := a.processToolResults(ctx, messages, "", []ToolCall{toolCall}, []ToolCallResult{toolResult}, session, executed)
	if err != nil {
		return FunctionCallResult{}, fmt.Errorf("process format error: %w", err)
	}
	return FunctionCallResult{Success: true, Response: finalResponse}, nil
}
