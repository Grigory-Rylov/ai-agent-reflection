package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

// retryableError — ошибка LLM-запроса, которую стоит повторить
// (сервер недоступен/перезагружается). HTTP 4xx и ошибки SSE
// (например, context_length_exceeded) сюда НЕ оборачиваются.
type retryableError struct {
	err error
}

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

// isRetryableError проверяет, является ли ошибка серверной (ретрабильной).
func isRetryableError(err error) bool {
	var re *retryableError
	return errors.As(err, &re)
}

// streamAndCollect отправляет streaming-запрос и собирает ответ.
// При серверных ошибках (недоступность, HTTP 5xx, пустой/оборванный стрим)
// ретраит бесконечно с паузой RetryDelay до успеха или отмены контекста.
func (a *agentImpl) streamAndCollect(ctx context.Context, config StreamingConfig, messages []Message) (string, string, string, []ToolCall, int, int, error) {
	retryDelay := a.config.RetryDelay
	if retryDelay <= 0 {
		retryDelay = 5 * time.Second
	}
	for attempt := 1; ; attempt++ {
		responseText, reasoningText, finishReason, toolCalls, promptTokens, completionTokens, err := a.streamAndCollectOnce(ctx, config, messages)
		if err != nil {
			if !isRetryableError(err) {
				return "", "", "", nil, 0, 0, err
			}
			a.logRetry(attempt, err)
			if !a.sleepBeforeRetry(ctx, retryDelay) {
				return "", "", "", nil, 0, 0, ctx.Err()
			}
			continue
		}
		if isTruncatedStream(responseText, reasoningText, finishReason, toolCalls) {
			a.logRetry(attempt, errors.New("empty/truncated stream from LLM"))
			if !a.sleepBeforeRetry(ctx, retryDelay) {
				return "", "", "", nil, 0, 0, ctx.Err()
			}
			continue
		}
		// Сохраняем KV-cache слота после каждого ответа LLM, пока слот держит
		// актуальный кэш. Только при включённом slot-save (SlotSaver выставлен
		// вызывающим кодом только когда models.json slot-save: true).
		if a.config.SlotSave && a.config.SlotSaver != nil {
			a.config.SlotSaver.SaveSlot(ctx)
		}
		return responseText, reasoningText, finishReason, toolCalls, promptTokens, completionTokens, nil
	}
}

// streamAndCollectOnce выполняет одну попытку: streaming-запрос + сбор ответа.
func (a *agentImpl) streamAndCollectOnce(ctx context.Context, config StreamingConfig, messages []Message) (string, string, string, []ToolCall, int, int, error) {
	chunkChan, err := a.streamingRequest(ctx, config, messages)
	if err != nil {
		return "", "", "", nil, 0, 0, err
	}
	return a.collectStreamResponseWithToolCalls(chunkChan)
}

// isTruncatedStream определяет, что стрим оборван или пуст:
// нет finish_reason и нет ни контента, ни reasoning, ни tool_calls,
// либо пришёл маркер mid-stream разрыва.
func isTruncatedStream(responseText, reasoningText, finishReason string, toolCalls []ToolCall) bool {
	if finishReason != "" {
		return false
	}
	if len(toolCalls) > 0 {
		return false
	}
	if responseText == "" && reasoningText == "" {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(responseText), "Stream error:")
}

// logRetry логирует попытку ретрая LLM-запроса.
func (a *agentImpl) logRetry(attempt int, err error) {
	prefix := a.agentPrefix()
	logger.DebugToFile(prefix+"[RETRY] LLM request attempt %d failed, retrying: %v", attempt, err)
	a.debugLog.Warn("%s[RETRY] LLM request attempt %d failed, retrying: %v", prefix, attempt, err)
}

// sleepBeforeRetry ждёт паузу перед следующим ретраем.
// Возвращает false, если контекст отменён.
func (a *agentImpl) sleepBeforeRetry(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
