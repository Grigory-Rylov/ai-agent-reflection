package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)


type retryableError struct {
	err error
}

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }


func isRetryableError(err error) bool {
	var re *retryableError
	return errors.As(err, &re)
}


func (a *agentImpl) streamAndCollect(ctx context.Context, config StreamingConfig, messages []Message) (string, string, string, []ToolCall, int, int, error) {
	retryDelay := a.config.RetryDelay
	if retryDelay <= 0 {
		retryDelay = 5 * time.Second
	}
	var lastErr error
	for attempt := 1; ; attempt++ {
		responseText, reasoningText, finishReason, toolCalls, promptTokens, completionTokens, err := a.streamAndCollectOnce(ctx, config, messages)
		if err != nil {
			
			
			if !isRetryableError(err) {
				return "", "", "", nil, 0, 0, err
			}
			lastErr = err
			a.logRetry(attempt, err)
			if !a.sleepBeforeRetry(ctx, retryDelay) {
				if cancelErr := a.interruptedByCancel(ctx); cancelErr != nil {
					return "", "", "", nil, 0, 0, cancelErr
				}
				break
			}
			continue
		}
		
		
		if cancelErr := a.interruptedByCancel(ctx); cancelErr != nil && finishReason == "" && len(toolCalls) == 0 {
			return "", "", "", nil, 0, 0, cancelErr
		}
		if isTruncatedStream(responseText, reasoningText, finishReason, toolCalls) {
			lastErr = errors.New("empty/truncated stream from LLM")
			a.logRetry(attempt, lastErr)
			if !a.sleepBeforeRetry(ctx, retryDelay) {
				if cancelErr := a.interruptedByCancel(ctx); cancelErr != nil {
					return "", "", "", nil, 0, 0, cancelErr
				}
				break
			}
			continue
		}
		
		
		
		if a.config.SlotSave && a.config.SlotSaver != nil {
			a.config.SlotSaver.SaveSlot(ctx)
		}
		a.appendReasoning(ctx, reasoningText)
		return responseText, reasoningText, finishReason, toolCalls, promptTokens, completionTokens, nil
	}
	if lastErr != nil {
		return "", "", "", nil, 0, 0, fmt.Errorf("LLM request exhausted: %w", lastErr)
	}
	return "", "", "", nil, 0, 0, ctx.Err()
}


func (a *agentImpl) streamAndCollectOnce(ctx context.Context, config StreamingConfig, messages []Message) (string, string, string, []ToolCall, int, int, error) {
	chunkChan, err := a.streamingRequest(ctx, config, messages)
	if err != nil {
		return "", "", "", nil, 0, 0, err
	}
	return a.collectStreamResponseWithToolCalls(chunkChan)
}


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


func (a *agentImpl) logRetry(attempt int, err error) {
	prefix := a.agentPrefix()
	logger.DebugToFile(prefix+"[RETRY] LLM request attempt %d failed, retrying: %v", attempt, err)
	a.debugLog.Warn("%s[RETRY] LLM request attempt %d failed, retrying: %v", prefix, attempt, err)
}


func (a *agentImpl) interruptedByCancel(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return ctx.Err()
	}
	return nil
}


func (a *agentImpl) sleepBeforeRetry(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
