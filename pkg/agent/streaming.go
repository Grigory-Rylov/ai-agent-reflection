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

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
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
	IsDone           bool
	Timestamp        time.Time
}

func (a *agentImpl) streamingRequest(ctx context.Context, config StreamingConfig, messages []Message) (<-chan StreamChunkEvent, error) {
	reqBody := a.buildRequestJSON(config, messages)

	req, err := a.createStreamingRequest(ctx, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	chunkChan := make(chan StreamChunkEvent, 100)
	go a.readStreamResponse(ctx, resp, chunkChan)
	return chunkChan, nil
}

func (a *agentImpl) buildRequestJSON(config StreamingConfig, messages []Message) []byte {
	reqBody := map[string]interface{}{
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
		chunkChan <- StreamChunkEvent{
			FinishReason: finishReason,
			IsDone:       true,
			Timestamp:    time.Now(),
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
	Choices []struct {
		Delta struct {
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
			ToolCalls        []ToolCall `json:"tool_calls"`
			ToolCallID       string     `json:"tool_call_id"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

func (a *agentImpl) parseSSEEvent(jsonData string) *SSEEvent {
	var event SSEEvent
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		return nil
	}
	return &event
}
