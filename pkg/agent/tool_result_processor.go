package agent

import (
	"context"
	"fmt"

	"github.com/opencode/llama-client/pkg/compress"
	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/pkg/tokenizers"
	sess "github.com/opencode/llama-client/session"
)

type contextKey string

const toolCallDepthKey contextKey = "tool_call_depth"
const maxToolCallRecursion = 100

// processToolResults отправляет результат выполнения инструментов обратно в AI
// Поддерживает как NATIVE (OpenAI format), так и XML/JSON tool calls в ответе
// executed — карта сигнатур уже выполненных инструментов (для дедупликации между рекурсиями)
func (a *agentImpl) processToolResults(ctx context.Context, originalMessages []Message, assistantContent string, toolCalls []ToolCall, toolResults []ToolCallResult, session *sess.Session, executed map[string]bool) (string, error) {
	depth, _ := ctx.Value(toolCallDepthKey).(int)
	if depth >= maxToolCallRecursion {
		prefix := a.agentPrefix()
		fmt.Printf(prefix+"[WARN] Tool call recursion limit (%d) reached, stopping recursion\n", maxToolCallRecursion)
		logger.DebugToFile(prefix+"[FLOW] Tool call recursion limit reached at depth %d", depth)
		return "", nil
	}

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
	messages := a.buildToolResultMessages(originalMessages, assistantContent, toolCalls, toolResults)

	// Проверяем переполнение ДО отправки в LLM (как в opencode — перед каждым запросом)
	if a.compactor != nil {
		messages = a.compactIfNeededBeforeLLM(ctx, session, messages, assistantContent, toolCalls, toolResults)
	}

	// Отправляем запрос в LLM (с инструментами, чтобы модель могла продолжить)
	streamConfig := StreamingConfig{
		Model:       a.config.Model,
		MaxTokens:   a.config.MaxTokens,
		Temperature: a.config.Temperature,
		Tools:       a.toolsRegistry.ToOpenAISchema(),
		Stream:      true,
	}

	// Собираем ответ с проверкой на tool_calls (с бесконечным ретраем серверных ошибок)
	responseText, reasoningText, finishReason, streamToolCalls, promptTokens, completionTokens, err := a.streamAndCollect(ctx, streamConfig, messages)
	if err != nil {
		// Реактивная компакция при переполнении контекста (как compactAfterOverflow в opencode)
		if IsContextOverflowError(err) && a.compactor != nil {
			prefix := a.agentPrefix()
			fmt.Printf(prefix+"[OPENCODE-COMPACT] Reactive overflow recovery for peer %d\n", session.GetPeerID())
			logger.DebugToFile(prefix+"[OPENCODE-COMPACT] Peer %d: Reactive overflow recovery, compacting", session.GetPeerID())

			a.compactIfNeeded(ctx, session)

			// Восстанавливаем tool calls/results после компактизации
			history := session.GetHistory()
			hasLastResult := false
			if len(history) > 1 {
				if history[len(history)-2].Role == sess.AssistantRole {
					hasLastResult = true
				}
			}
			if !hasLastResult {
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
				for _, tr := range toolResults {
					session.AddToolMessage(tr.ToolCallID, tr.ToolName, tr.Content)
				}
			}

			messages = a.buildToolResultMessagesFromSession(session)

			// Повторный запрос после компактизации
			responseText, reasoningText, finishReason, streamToolCalls, promptTokens, completionTokens, err = a.streamAndCollect(ctx, streamConfig, messages)
			if err != nil {
				// Если после реактивной компактизации снова ошибка — это терминальная
				prefix := a.agentPrefix()
				fmt.Printf(prefix+"[ERROR] Context overflow after reactive compaction: %v\n", err)
				return "", fmt.Errorf("context overflow after compaction: %w", err)
			}
		} else {
			return "", err
		}
	}

	// Отправляем reasoning в thinking чат сразу после получения,
	// чтобы он не терялся при рекурсивных вызовах инструментов
	a.sendThinkingIfNeeded(session, reasoningText)

	// Отправляем количество токенов после ответа LLM
	a.sendThinkingTokens(session.GetPeerID(), promptTokens, completionTokens)

	prefix := a.agentPrefix()
	logger.DebugToFile("%sprocessToolResults: content=%d, reasoning=%d, tool_calls=%d, finish=%q",
		prefix, len(responseText), len(reasoningText), len(streamToolCalls), finishReason)
	if len(responseText) > 0 {
		logger.DebugToFile("\n---------------- response content ----------------------")
		logger.DebugToFile("%s%s", prefix, responseText)
	}
	if len(reasoningText) > 0 {
		logger.DebugToFile("\n---------------- response reasoning ------------------")
		logger.DebugToFile("%s%s", prefix, reasoningText)
	}
	logger.DebugToFile("\n====================================================")

	// Если модель вернула новые NATIVE tool_calls — выполняем их рекурсивно
	if len(streamToolCalls) > 0 {
		a.debugLog.Debug("NATIVE format: detected %d tool calls in tool results response", len(streamToolCalls))
		for _, tc := range streamToolCalls {
			executed[toolCallSignature(tc)] = true
		}
		result := a.executeAllTools(ctx, streamToolCalls, session.GetPeerID())
		if len(result.ToolCalls) > 0 {
			recursiveCtx := context.WithValue(ctx, toolCallDepthKey, depth+1)
			return a.processToolResults(recursiveCtx, messages, "", streamToolCalls, result.ToolCalls, session, executed)
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
		a.debugLog.Debug("XML fallback: detected %d tool calls in tool results response", len(parsed.ToolCalls))
		toolCalls := convertXMLToolCalls(parsed.ToolCalls)
		// Фильтруем дубли уже выполненных
		var uniqueCalls []ToolCall
		for _, tc := range toolCalls {
			sig := toolCallSignature(tc)
			if executed[sig] {
				a.debugLog.Debug("XML duplicate skipped in tool results: %s", tc.Function.Name)
				continue
			}
			executed[sig] = true
			uniqueCalls = append(uniqueCalls, tc)
		}
		if len(uniqueCalls) > 0 {
			result := a.executeAllTools(ctx, uniqueCalls, session.GetPeerID())
			if len(result.ToolCalls) > 0 {
				recursiveCtx := context.WithValue(ctx, toolCallDepthKey, depth+1)
				return a.processToolResults(recursiveCtx, messages, parsed.Content, uniqueCalls, result.ToolCalls, session, executed)
			}
		}
	}

	// JSON fallback в tool results response
	jsonParsed := ParseJSONToolCalls(responseText)
	if len(jsonParsed.ToolCalls) > 0 {
		a.debugLog.Debug("JSON fallback: detected %d tool calls in tool results response", len(jsonParsed.ToolCalls))
		responseText = jsonParsed.Content

		toolCalls := convertXMLToolCalls(jsonParsed.ToolCalls)
		var uniqueCalls []ToolCall
		for _, tc := range toolCalls {
			sig := toolCallSignature(tc)
			if executed[sig] {
				a.debugLog.Debug("JSON duplicate skipped in tool results: %s", tc.Function.Name)
				continue
			}
			executed[sig] = true
			uniqueCalls = append(uniqueCalls, tc)
		}
		if len(uniqueCalls) > 0 {
			result := a.executeAllTools(ctx, uniqueCalls, session.GetPeerID())
			if len(result.ToolCalls) > 0 {
				recursiveCtx := context.WithValue(ctx, toolCallDepthKey, depth+1)
				return a.processToolResults(recursiveCtx, messages, jsonParsed.Content, uniqueCalls, result.ToolCalls, session, executed)
			}
		}
	}

	// Safety net: вырезаем <tool_call> блоки из responseText перед сохранением
	if responseText != "" {
		parsedResp := ParseXMLToolCalls(responseText)
		responseText = parsedResp.Content
		responseText = a.stripThinkingTags(responseText, session.GetPeerID())
	}

	// Если модель не вернула content — пробуем последнее сообщение сессии
	if responseText == "" {
		hist := session.GetHistory()
		if len(hist) > 0 {
			last := hist[len(hist)-1]
			if last.Role == sess.AssistantRole && last.Content != "" {
				return last.Content, nil
			}
		}
		return "", nil
	}

	session.AddAssistantMessage(responseText)
	return responseText, nil
}

// handleInvalidXMLToolCall обрабатывает случай когда модель отправила
// невалидный XML tool call. Создаёт виртуальный tool call с ошибкой
// и отправляет модели через processToolResults.
func (a *agentImpl) handleInvalidXMLToolCall(ctx context.Context, messages []Message, session *sess.Session, executed map[string]bool) (FunctionCallResult, error) {
	a.sendThinking(session.GetPeerID(), "[TOOL] Error: Invalid XML tool call format. Send the model a corrective message.")
	logger.DebugToFile("%s[TOOL] Invalid XML: sending format error to model", a.agentPrefix())

	errMsg := "FORMAT ERROR: You tried to use XML tags (<tool_call>, <function=...>) for tool calls, but this format is incorrect. " +
		"You must use native function calling: declare the function name and arguments in the tool_calls array provided by the API. " +
		"Do NOT write XML tool tags yourself. If you included any text along with the tool call, re-send it with the correct format."

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

// buildToolResultMessages собирает список сообщений для API из оригинальных + assistant + tool results.
func (a *agentImpl) buildToolResultMessages(originalMessages []Message, assistantContent string, toolCalls []ToolCall, toolResults []ToolCallResult) []Message {
	messages := make([]Message, len(originalMessages))
	copy(messages, originalMessages)

	reqToolCalls := make([]ToolCall, len(toolCalls))
	for i, tc := range toolCalls {
		reqToolCalls[i] = buildToolCallForRequest(tc)
	}
	messages = append(messages, Message{
		Role:      "assistant",
		Content:   assistantContent,
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

// compactIfNeededBeforeLLM проверяет, не переполнит ли текущий набор сообщений контекст.
// Если да — компактирует сессию и пересобирает messages (сохраняя tool calls/results).
// Опционально, проверяет не переполнит ли context — если да, выполняет компакцию.
func (a *agentImpl) compactIfNeededBeforeLLM(ctx context.Context, session *sess.Session, messages []Message, assistantContent string, toolCalls []ToolCall, toolResults []ToolCallResult) []Message {
	tokenMessages := make([]tokenizers.Message, len(messages))
	for i, m := range messages {
		content := m.Content
		// Добавляем содержимое tool calls к оценке токенов
		for _, tc := range m.ToolCalls {
			content += string(tc.Function.Arguments)
		}
		tokenMessages[i] = tokenizers.Message{
			Role:    m.Role,
			Content: content,
		}
	}

	tokens := compress.EstimateMessagesTokensSimple(tokenMessages)
	// Safety margin: эвристика len/4 может недооценивать реальные токены
	// (особенно для JSON/структурированных данных), поэтому умножаем на 1.5
	tokens = int(float64(tokens) * 1.5)
	if !compress.IsOverflow(tokens, a.config.MaxTokens, a.config.CompactionReserved) {
		return messages
	}

	prefix := a.agentPrefix()
	fmt.Printf(prefix+"[OPENCODE-COMPACT] Tool results overflow (%d tokens), compacting before LLM request\n", tokens)
	logger.DebugToFile(prefix+"[OPENCODE-COMPACT] Peer %d: Tool results overflow (%d/%d), compacting",
		session.GetPeerID(), tokens, a.config.MaxTokens)

	// Компактируем сессию — она уже содержит tool calls/results (добавлены выше)
	a.compactIfNeeded(ctx, session)

	// После компактизации сессия сброшена, но tool calls/results добавленные до compactIfNeeded
	// могут быть утеряны. Нужно убедиться, что последние tool results восстановлены.
	// compactIfNeeded использует tailTurns — если tool messages попали в tail, они сохранятся.
	// Но если не попали — нужно добавить их заново.

	history := session.GetHistory()
	historyLen := len(history)

	// Проверяем, есть ли в истории tool message (последний добавленный)
	hasLastToolResult := false
	if historyLen > 0 {
		last := history[historyLen-1]
		// Tool messages добавляются как user role в сессии
		if last.Role == sess.UserRole && historyLen > 1 {
			prev := history[historyLen-2]
			if prev.Role == sess.AssistantRole {
				// Это assistant + tool result пара — есть в сессии
				hasLastToolResult = true
			}
		}
	}

	// Если tool results утеряны — добавляем их заново
	if !hasLastToolResult {
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

		for _, tr := range toolResults {
			session.AddToolMessage(tr.ToolCallID, tr.ToolName, tr.Content)
		}
	}

	// Пересобираем messages из обновлённой сессии
	return a.buildToolResultMessagesFromSession(session)
}

// buildToolResultMessagesFromSession пересобирает messages из истории сессии
// после компактизации.
func (a *agentImpl) buildToolResultMessagesFromSession(session *sess.Session) []Message {
	return a.convertHistoryToAPIMessages(session.GetContextMessages())
}
