package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message представляет сообщение в формате OpenAI
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ============================================================
// SSE Streaming — SSE-стриминг ответов от llama-server
// ============================================================

// StreamingConfig содержит настройки для streaming запроса
type StreamingConfig struct {
	Model       string
	MaxTokens   int
	Temperature float64
	// Tools для function calling
	Tools       []map[string]interface{}
	// Stream — флаг включения streaming
	Stream      bool
}

// StreamChunkEvent представляет событие из streaming ответа
type StreamChunkEvent struct {
	Content      string
	FinishReason string
	IsDone       bool
	Timestamp    time.Time
}

// streamingRequest отправляет streaming запрос к llama-server
func (a *agentImpl) streamingRequest(ctx context.Context, config StreamingConfig, messages []Message) (<-chan StreamChunkEvent, error) {
	reqBody := a.buildRequestJSON(config, messages)
	req, err := a.createStreamingRequest(reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	chunkChan := make(chan StreamChunkEvent, 100)
	go a.readStreamResponse(resp, chunkChan)
	return chunkChan, nil
}

// buildRequestJSON формирует JSON тело для streaming запроса
func (a *agentImpl) buildRequestJSON(config StreamingConfig, messages []Message) []byte {
	reqBody := map[string]interface{}{
		"model":       config.Model,
		"messages":    messages,
		"temperature": config.Temperature,
		"max_tokens":  config.MaxTokens,
		"stream":      true,
	}

	if len(config.Tools) > 0 {
		reqBody["tools"] = config.Tools
	}

	jsonData, _ := json.Marshal(reqBody)
	return jsonData
}

// createStreamingRequest создаёт HTTP запрос для streaming
func (a *agentImpl) createStreamingRequest(jsonData []byte) (*http.Request, error) {
	reqURL := fmt.Sprintf("%s/v1/chat/completions", a.config.LlamaServerURL)
	req, err := http.NewRequestWithContext(context.Background(), "POST", reqURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	return req, nil
}

// readStreamResponse читает SSE-поток и отправляет события в канал
func (a *agentImpl) readStreamResponse(resp *http.Response, chunkChan chan StreamChunkEvent) {
	defer resp.Body.Close()
	defer close(chunkChan)

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadSlice('\n')
		if err != nil {
			if err == io.EOF {
				return
			}
			a.sendStreamError(chunkChan, err)
			return
		}

		a.processStreamLine(line, chunkChan)
	}
}

// processStreamLine обрабатывает одну строку из потока
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

// sendStreamError отправляет событие ошибки
func (a *agentImpl) sendStreamError(chunkChan chan StreamChunkEvent, err error) {
	chunkChan <- StreamChunkEvent{
		Content:   fmt.Sprintf("Stream error: %v", err),
		IsDone:    true,
		Timestamp: time.Now(),
	}
}

// sendDoneEvent отправляет событие завершения
func (a *agentImpl) sendDoneEvent(chunkChan chan StreamChunkEvent) {
	chunkChan <- StreamChunkEvent{
		Content:  "",
		IsDone:   true,
		Timestamp: time.Now(),
	}
}

// processSSEData обрабатывает данные из SSE события
func (a *agentImpl) processSSEData(lineStr string, chunkChan chan StreamChunkEvent) {
	jsonData := strings.TrimPrefix(lineStr, "data: ")
	if len(jsonData) == 0 {
		return
	}

	event := a.parseSSEEvent(jsonData)
	if event == nil || len(event.Choices) == 0 {
		return
	}

	choice := event.Choices[0]
	content := choice.Delta.Content
	if content == "" {
		return
	}

	finishReason := ""
	if choice.FinishReason != nil {
		finishReason = *choice.FinishReason
	}

	chunkChan <- StreamChunkEvent{
		Content:      content,
		FinishReason: finishReason,
		IsDone:       choice.FinishReason != nil,
		Timestamp:    time.Now(),
	}
}

// SSEEvent структура для парсинга SSE данных
type SSEEvent struct {
	Choices []struct {
		Delta struct {
			Content      string     `json:"content"`
			ToolCalls    []ToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// parseSSEEvent парсит JSON из SSE события
func (a *agentImpl) parseSSEEvent(jsonData string) *SSEEvent {
	var event SSEEvent
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		return nil
	}
	return &event
}
