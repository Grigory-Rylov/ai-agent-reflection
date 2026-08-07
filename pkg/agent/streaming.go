package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/opencode/llama-client/pkg/compress"
	"github.com/opencode/llama-client/pkg/logger"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // ID инструмента для сообщений с role=tool
	Name       string     `json:"name,omitempty"`         // Имя инструмента для сообщений с role=tool
}

type StreamingConfig struct {
	Model       string
	MaxTokens   int
	Temperature float64
	Tools       []map[string]interface{}
	Stream      bool
}

type StreamChunkEvent struct {
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
	FinishReason     string
	PromptTokens     int
	CompletionTokens int
	IsDone           bool
	IsError          bool
	ErrorCode        string
	Timestamp        time.Time
}

func (a *agentImpl) streamingRequest(ctx context.Context, config StreamingConfig, messages []Message) (<-chan StreamChunkEvent, error) {
	reqBody := a.buildRequestJSON(config, messages)

	contentChars := 0
	for _, m := range messages {
		contentChars += len(m.Content)
	}

	logger.DebugToFile("%s[LLM REQUEST] Sending request to %s, model=%s, messages=%d, chars=%d, tokens=%d", a.agentPrefix(), a.config.LlamaServerURL, config.Model, len(messages), len(reqBody), contentChars/3)

	req, err := a.createStreamingRequest(ctx, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		logger.DebugToFile("%s[LLM REQUEST] Failed to send: %v", a.agentPrefix(), err)
		// Недоступность сервера — ретрабильная ошибка.
		return nil, &retryableError{err: fmt.Errorf("send request: %w", err)}
	}

	if resp.StatusCode != http.StatusOK {
		// Читаем тело ответа для логирования ошибки
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		a.debugLog.Error("API ERROR: Status %d, response: %s", resp.StatusCode, string(body))
		logger.DebugToFile("%s[LLM REQUEST] API error: status %d", a.agentPrefix(), resp.StatusCode)
		apiErr := fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
		// HTTP 5xx (сервер перезагружается/модель грузится) — ретрабильная ошибка.
		// HTTP 4xx (клиентская ошибка) — не ретраим.
		if resp.StatusCode >= 500 {
			return nil, &retryableError{err: apiErr}
		}
		return nil, apiErr
	}

	logger.DebugToFile("%s[LLM REQUEST] Request successful, reading stream...", a.agentPrefix())
	chunkChan := make(chan StreamChunkEvent, 100)
	go a.readStreamResponse(ctx, resp, chunkChan)
	return chunkChan, nil
}

func (a *agentImpl) buildRequestJSON(config StreamingConfig, messages []Message) []byte {
	reqBody := a.buildBaseRequestJSON(config.Model, messages, true)

	if len(config.Tools) > 0 {
		reqBody["tools"] = config.Tools
	}

	jsonData, _ := json.Marshal(reqBody)

	// В режиме отладки сохраняем промпт в файл
	if a.config.Debug {
		a.saveDebugPrompt(jsonData)
	}

	return jsonData
}

// buildBaseRequestJSON формирует базовый JSON запрос для llama-server API
func (a *agentImpl) buildBaseRequestJSON(model string, messages []Message, stream bool) map[string]interface{} {
	// max_tokens для output: OUTPUT_TOKEN_MAX, но не больше контекста модели
	maxOutput := compress.OUTPUT_TOKEN_MAX
	if a.config.MaxTokens > 0 && a.config.MaxTokens < maxOutput {
		maxOutput = a.config.MaxTokens
	}
	req := map[string]interface{}{
		"model":       model,
		"messages":    messages,
		"temperature": a.config.Temperature,
		"max_tokens":  maxOutput,
		"stream":      stream,
	}
	// Pin to specific slot for KV-cache continuity (multi-agent support)
	if a.config.SlotID >= 0 {
		req["slot_id"] = a.config.SlotID
	}
	return req
}

// saveDebugPrompt сохраняет промпт в debug_prompt.txt
func (a *agentImpl) saveDebugPrompt(jsonData []byte) {
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, jsonData, "", "  "); err != nil {
		// Если не удалось форматировать - сохраняем как есть
		os.WriteFile("debug_prompt.txt", jsonData, 0644)
		return
	}
	os.WriteFile("debug_prompt.txt", prettyJSON.Bytes(), 0644)
}

func (a *agentImpl) createStreamingRequest(ctx context.Context, jsonData []byte) (*http.Request, error) {
	reqURL := fmt.Sprintf("%s/v1/chat/completions", a.config.LlamaServerURL)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	return req, nil
}

func (a *agentImpl) readStreamResponse(ctx context.Context, resp *http.Response, chunkChan chan StreamChunkEvent) {
	defer resp.Body.Close()
	defer close(chunkChan)

	reader := bufio.NewReader(resp.Body)
	readCh := make(chan struct {
		line []byte
		err  error
	}, 1)

	for {
		go func() {
			line, err := reader.ReadSlice('\n')
			readCh <- struct {
				line []byte
				err  error
			}{line, err}
		}()

		select {
		case <-ctx.Done():
			return
		case result := <-readCh:
			if result.err != nil {
				if result.err == io.EOF {
					return
				}
				a.sendStreamError(chunkChan, result.err)
				return
			}
			a.processStreamLine(result.line, chunkChan)
		}
	}
}

func (a *agentImpl) processStreamLine(line []byte, chunkChan chan StreamChunkEvent) {
	lineStr := strings.TrimSpace(string(line))

	if lineStr == "" {
		return
	}
	if lineStr == "[DONE]" {
		a.sendDoneEvent(chunkChan)
		return
	}
	if !strings.HasPrefix(lineStr, "data: ") {
		return
	}

	a.processSSEData(lineStr, chunkChan)
}

func (a *agentImpl) sendStreamError(chunkChan chan StreamChunkEvent, err error) {
	chunkChan <- StreamChunkEvent{
		Content:   fmt.Sprintf("Stream error: %v", err),
		IsDone:    true,
		Timestamp: time.Now(),
	}
}

func (a *agentImpl) sendDoneEvent(chunkChan chan StreamChunkEvent) {
	chunkChan <- StreamChunkEvent{
		Content:  "",
		IsDone:   true,
		Timestamp: time.Now(),
	}
}

func (a *agentImpl) processSSEData(lineStr string, chunkChan chan StreamChunkEvent) {
	jsonData := strings.TrimPrefix(lineStr, "data: ")
	if len(jsonData) == 0 {
		return
	}

	event := a.parseSSEEvent(jsonData)

	// Проверяем на ошибку (например, context_length_exceeded)
	if event != nil && event.Error != nil {
		chunkChan <- StreamChunkEvent{
			Content:      fmt.Sprintf("API Error: %s", event.Error.Message),
			IsError:      true,
			ErrorCode:    event.Error.Code,
			IsDone:       true,
			Timestamp:    time.Now(),
		}
		return
	}

	if event == nil || len(event.Choices) == 0 {
		return
	}

	choice := event.Choices[0]
	content := choice.Delta.Content
	toolCalls := choice.Delta.ToolCalls

	finishReason := ""
	if choice.FinishReason != nil {
		finishReason = *choice.FinishReason
	}

	if finishReason != "" {
		// ВАЖНО: отправляем finish_reason ВМЕСТЕ с tool_calls если они есть
		promptTokens, completionTokens := tokenCounts(event)
		chunkChan <- StreamChunkEvent{
			Content:          content,
			ReasoningContent: choice.Delta.ReasoningContent,
			ToolCalls:        toolCalls,
			FinishReason:     finishReason,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			IsDone:           true,
			Timestamp:        time.Now(),
		}
		return
	}

	if content == "" && choice.Delta.ReasoningContent == "" && len(toolCalls) == 0 {
		return
	}

	chunkChan <- StreamChunkEvent{
		Content:          content,
		ReasoningContent: choice.Delta.ReasoningContent,
		ToolCalls:        toolCalls,
		IsDone:           false,
		Timestamp:        time.Now(),
	}
}

type SSEEvent struct {
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
	Choices []struct {
		Delta struct {
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
			ToolCalls        []ToolCall `json:"tool_calls"`
			ToolCallID       string     `json:"tool_call_id"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	// Usage — стандартное поле OpenAI (не-стриминг).
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	// Timings — llama.cpp-специфичное поле в финальном чанке стрима.
	Timings *struct {
		PromptN    int `json:"prompt_n"`
		PredictedN int `json:"predicted_n"`
	} `json:"timings"`
}

// tokenCounts возвращает токены (подано, ответ) из события.
// Приоритет: usage (OpenAI), затем timings (llama.cpp).
func tokenCounts(event *SSEEvent) (int, int) {
	if event == nil {
		return 0, 0
	}
	if event.Usage != nil && event.Usage.TotalTokens > 0 {
		return event.Usage.PromptTokens, event.Usage.CompletionTokens
	}
	if event.Timings != nil {
		return event.Timings.PromptN, event.Timings.PredictedN
	}
	return 0, 0
}

func (a *agentImpl) parseSSEEvent(jsonData string) *SSEEvent {
	var event SSEEvent
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		return nil
	}
	return &event
}
