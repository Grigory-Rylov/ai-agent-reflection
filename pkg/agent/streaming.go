package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` 
	Name       string     `json:"name,omitempty"`         
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
		
		
		
		
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		readableErr := fmt.Errorf("send request: %w", err)
		if errors.Is(err, context.DeadlineExceeded) {
			readableErr = fmt.Errorf("LLM server was shutdown or unreachable")
		}
		return nil, &retryableError{err: readableErr}
	}

	if resp.StatusCode != http.StatusOK {
		
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		a.debugLog.Error("API ERROR: Status %d, response: %s", resp.StatusCode, string(body))
		logger.DebugToFile("%s[LLM REQUEST] API error: status %d", a.agentPrefix(), resp.StatusCode)
		apiErr := fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
		
		
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

	
	if a.config.Debug {
		a.saveDebugPrompt(jsonData)
	}

	return jsonData
}


func (a *agentImpl) buildBaseRequestJSON(model string, messages []Message, stream bool) map[string]interface{} {
	
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
	if a.config.EngineType == "ninfer" {
		req["enable_thinking"] = true
	} else {
		req["chat_template_kwargs"] = map[string]interface{}{
			"enable_thinking": true,
		}
	}
	
	if a.config.SlotID >= 0 {
		req["slot_id"] = a.config.SlotID
	}
	return req
}


func (a *agentImpl) saveDebugPrompt(jsonData []byte) {
	debugDir := filepath.Join(tools.BaseDir, "debug")
	promptPath := filepath.Join(debugDir, "debug_prompt.txt")
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, jsonData, "", "  "); err != nil {
		os.MkdirAll(debugDir, 0755)
		os.WriteFile(promptPath, jsonData, 0644)
		return
	}
	os.MkdirAll(debugDir, 0755)
	os.WriteFile(promptPath, prettyJSON.Bytes(), 0644)
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

const maxSSELineBytes = 16 * 1024 * 1024

const DefaultStreamIdleTimeout = 5 * time.Minute

const ErrCodeStreamIdleTimeout = "stream_idle_timeout"

func (a *agentImpl) streamIdleTimeout() time.Duration {
	if a.config.StreamIdleTimeout > 0 {
		return a.config.StreamIdleTimeout
	}
	if a.config.StreamIdleTimeout < 0 {
		return 0
	}
	return DefaultStreamIdleTimeout
}

func resetIdleTimer(t *time.Timer, d time.Duration) {
	if t == nil {
		return
	}
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

func (a *agentImpl) sendStreamIdleError(chunkChan chan StreamChunkEvent, idleFor time.Duration) {
	logger.DebugToFile("%s[LLM STREAM] no SSE data for %s, aborting stream", a.agentPrefix(), idleFor)
	chunkChan <- StreamChunkEvent{
		Content:   fmt.Sprintf("LLM stream stalled: no SSE data for %s", idleFor),
		IsDone:    true,
		IsError:   true,
		ErrorCode: ErrCodeStreamIdleTimeout,
		Timestamp: time.Now(),
	}
}

func (a *agentImpl) readStreamResponse(ctx context.Context, resp *http.Response, chunkChan chan StreamChunkEvent) {
	defer resp.Body.Close()
	defer close(chunkChan)

	reader := bufio.NewReaderSize(resp.Body, 256*1024)
	readCh := make(chan struct {
		line []byte
		err  error
	}, 1)

	idleTimeout := a.streamIdleTimeout()
	var idleTimer *time.Timer
	var idleCh <-chan time.Time
	if idleTimeout > 0 {
		idleTimer = time.NewTimer(idleTimeout)
		defer idleTimer.Stop()
		idleCh = idleTimer.C
	}

	for {
		go func() {
			line, err := readFullSSELine(reader)
			readCh <- struct {
				line []byte
				err  error
			}{line, err}
		}()

		select {
		case <-ctx.Done():
			return
		case <-idleCh:
			a.sendStreamIdleError(chunkChan, idleTimeout)
			return
		case result := <-readCh:
			resetIdleTimer(idleTimer, idleTimeout)
			if len(result.line) > 0 {
				a.processStreamLine(result.line, chunkChan)
			}
			if result.err != nil {
				if result.err == io.EOF || errors.Is(result.err, context.Canceled) {
					return
				}
				a.sendStreamError(chunkChan, result.err)
				return
			}
		}
	}
}

func readFullSSELine(reader *bufio.Reader) ([]byte, error) {
	var line []byte

	for {
		segment, err := reader.ReadSlice('\n')
		if len(segment) > 0 {
			line = append(line, segment...)
			if len(line) > maxSSELineBytes {
				return nil, fmt.Errorf("SSE line exceeds %d bytes", maxSSELineBytes)
			}
		}

		switch {
		case err == nil:
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			if len(line) > 0 && errors.Is(err, io.EOF) {
				return line, nil
			}
			return nil, err
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
		Content:   fmt.Sprintf("LLM stream read failed: %v", err),
		IsDone:    true,
		IsError:   true,
		ErrorCode: "stream_read_error",
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

type SSEDelta struct {
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content"`
	ToolCalls        []ToolCall `json:"tool_calls"`
	ToolCallID       string     `json:"tool_call_id"`
}

func (d *SSEDelta) UnmarshalJSON(data []byte) error {
	var raw struct {
		Content          string     `json:"content"`
		ReasoningContent string     `json:"reasoning_content"`
		Reasoning        string     `json:"reasoning"`
		ToolCalls        []ToolCall `json:"tool_calls"`
		ToolCallID       string     `json:"tool_call_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	d.Content = raw.Content
	d.ReasoningContent = raw.ReasoningContent
	if d.ReasoningContent == "" {
		d.ReasoningContent = raw.Reasoning
	}
	d.ToolCalls = raw.ToolCalls
	d.ToolCallID = raw.ToolCallID
	return nil
}

type SSEEvent struct {
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
	Choices []struct {
		Delta        SSEDelta `json:"delta"`
		FinishReason *string  `json:"finish_reason"`
	} `json:"choices"`
	
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	
	Timings *struct {
		PromptN    int `json:"prompt_n"`
		PredictedN int `json:"predicted_n"`
	} `json:"timings"`
}


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
