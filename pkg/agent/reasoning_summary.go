package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/util/stringutil"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

const (
	reasoningSummaryMinLen    = 200
	reasoningSummaryMaxInput  = 12000
	reasoningSummaryMaxOut    = 1200
	reasoningSummaryTimeout   = 90 * time.Second
	reasoningSummaryLabel     = "[REASONING SUMMARY]"
	reasoningSummaryTemp      = 0.2
	reasoningSummaryStatusCap = 1024
)

const reasoningSummaryPrompt = "You condense a language model's private chain-of-thought reasoning into a short third-person summary that the model will read in its future context. " +
	"Keep only decisions, plans, constraints, open questions and results that matter for the ongoing task. " +
	"No tool-call XML, no raw code dumps. Write 3-6 compact bullet lines, at most 150 words."

type reasoningContextKey struct{ name string }

var reasoningBufferKey = reasoningContextKey{"reasoningBuffer"}

type reasoningBuffer struct {
	mu    sync.Mutex
	parts []string
}

func (b *reasoningBuffer) add(part string) {
	if strings.TrimSpace(part) == "" {
		return
	}
	b.mu.Lock()
	b.parts = append(b.parts, part)
	b.mu.Unlock()
}

func (b *reasoningBuffer) drain() string {
	b.mu.Lock()
	joined := strings.Join(b.parts, "\n\n---\n\n")
	b.parts = nil
	b.mu.Unlock()
	return joined
}

func (a *agentImpl) withReasoningBuffer(ctx context.Context) context.Context {
	return context.WithValue(ctx, reasoningBufferKey, &reasoningBuffer{})
}

func (a *agentImpl) reasoningBufferFrom(ctx context.Context) *reasoningBuffer {
	buf, _ := ctx.Value(reasoningBufferKey).(*reasoningBuffer)
	return buf
}

func (a *agentImpl) appendReasoning(ctx context.Context, reasoningText string) {
	buf := a.reasoningBufferFrom(ctx)
	if buf == nil {
		return
	}
	cleaned := cleanReasoningForSummary(reasoningText)
	if cleaned == "" {
		return
	}
	buf.add(cleaned)
}

func cleanReasoningForSummary(text string) string {
	if text == "" {
		return ""
	}
	cleaned := ParseXMLToolCalls(text).Content
	if cleaned == "" {
		cleaned = text
	}
	if hasPartialToolCall(cleaned) {
		cleaned = stripPartialToolCall(cleaned)
	}
	return strings.TrimSpace(cleaned)
}

func (a *agentImpl) flushReasoningSummary(ctx context.Context, s *session.Session) {
	buf := a.reasoningBufferFrom(ctx)
	if buf == nil {
		return
	}
	raw := buf.drain()
	if len(raw) < reasoningSummaryMinLen {
		logger.DebugToFile("%s[REASONING SUMMARY] skipped: %d chars below min %d", a.agentPrefix(), len(raw), reasoningSummaryMinLen)
		a.debugLog.Debug("reasoning summary skipped: %d chars below min %d", len(raw), reasoningSummaryMinLen)
		return
	}

	summaryCtx := context.WithoutCancel(ctx)
	summary, err := a.summarizeReasoning(summaryCtx, raw)
	if err != nil {
		logger.DebugToFile("%s[REASONING SUMMARY] summarization failed: %v, storing truncated raw reasoning", a.agentPrefix(), err)
		a.debugLog.Warn("reasoning summarization failed: %v, storing truncated raw reasoning", err)
		summary = stringutil.Truncate(raw, reasoningSummaryMaxInput, "…")
	}
	if strings.TrimSpace(summary) == "" {
		return
	}

	s.AddAssistantMessage(reasoningSummaryLabel + "\n" + strings.TrimSpace(summary))
	logger.DebugToFile("%s[REASONING SUMMARY] stored in history: %d chars", a.agentPrefix(), len(summary))
	a.debugLog.Debug("reasoning summary stored: %d chars", len(summary))
}

func (a *agentImpl) summarizeReasoning(ctx context.Context, raw string) (string, error) {
	input := raw
	if len(input) > reasoningSummaryMaxInput {
		input = input[len(input)-reasoningSummaryMaxInput:]
	}

	messages := []Message{
		{Role: "system", Content: reasoningSummaryPrompt},
		{Role: "user", Content: input},
	}
	reqBody := a.buildSummaryRequestJSON(messages)
	a.saveDebugSummaryPrompt(reqBody)

	rawBody, err := a.doSummaryRequest(ctx, reqBody, len(input))
	if err != nil {
		return "", err
	}

	rawResponse, err := a.decodeResponse(bytes.NewReader(rawBody))
	if err != nil {
		return "", fmt.Errorf("decode summary response: %w", err)
	}

	summary := extractSummaryContent(rawResponse)
	if summary == "" {
		return "", fmt.Errorf("summary response has empty content")
	}
	return summary, nil
}

func (a *agentImpl) doSummaryRequest(ctx context.Context, reqBody []byte, inputChars int) ([]byte, error) {
	prefix := a.agentPrefix()
	logger.DebugToFile("%s[REASONING SUMMARY] Sending summary request to %s, model=%s, inputChars=%d",
		prefix, a.config.LlamaServerURL, a.config.Model, inputChars)

	reqCtx, cancel := context.WithTimeout(ctx, reasoningSummaryTimeout)
	defer cancel()

	req, err := a.createReasoningSummaryRequest(reqCtx, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create summary request: %w", err)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		logger.DebugToFile("%s[REASONING SUMMARY] send failed: %v", prefix, err)
		return nil, fmt.Errorf("send summary request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, reasoningSummaryStatusCap))
		logger.DebugToFile("%s[REASONING SUMMARY] API error: status %d, body: %s", prefix, resp.StatusCode, string(body))
		return nil, fmt.Errorf("summary API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read summary response: %w", err)
	}
	a.saveDebugSummaryResponse(rawBody)
	logger.DebugToFile("%s[REASONING SUMMARY] response received: %d bytes", prefix, len(rawBody))
	return rawBody, nil
}

func (a *agentImpl) buildSummaryRequestJSON(messages []Message) []byte {
	req := map[string]interface{}{
		"model":       a.config.Model,
		"messages":    messages,
		"temperature": reasoningSummaryTemp,
		"max_tokens":  reasoningSummaryMaxOut,
		"stream":      false,
	}
	if a.config.EngineType == "ninfer" {
		req["enable_thinking"] = false
	} else {
		req["chat_template_kwargs"] = map[string]interface{}{
			"enable_thinking": false,
		}
	}
	if a.config.SlotID >= 0 {
		req["slot_id"] = a.config.SlotID
	}
	jsonData, _ := json.Marshal(req)
	return jsonData
}

func (a *agentImpl) createReasoningSummaryRequest(ctx context.Context, jsonData []byte) (*http.Request, error) {
	reqURL := fmt.Sprintf("%s/v1/chat/completions", a.config.LlamaServerURL)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, fmt.Errorf("create summary request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func extractSummaryContent(rawResponse map[string]interface{}) string {
	choices, ok := rawResponse["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return ""
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return ""
	}
	message, ok := choice["message"].(map[string]interface{})
	if !ok {
		return ""
	}
	content, _ := message["content"].(string)
	return strings.TrimSpace(content)
}

func (a *agentImpl) saveDebugSummaryPrompt(jsonData []byte) {
	if !a.config.Debug {
		return
	}
	debugDir := filepath.Join(tools.BaseDir, "debug")
	promptPath := filepath.Join(debugDir, "debug_summary_prompt.txt")
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, jsonData, "", "  "); err != nil {
		os.MkdirAll(debugDir, 0755)
		os.WriteFile(promptPath, jsonData, 0644)
		return
	}
	os.MkdirAll(debugDir, 0755)
	os.WriteFile(promptPath, prettyJSON.Bytes(), 0644)
}

func (a *agentImpl) saveDebugSummaryResponse(jsonData []byte) {
	if !a.config.Debug {
		return
	}
	debugDir := filepath.Join(tools.BaseDir, "debug")
	responsePath := filepath.Join(debugDir, "debug_summary_response.txt")
	var prettyJSON bytes.Buffer
	if err := json.Indent(&prettyJSON, jsonData, "", "  "); err != nil {
		os.MkdirAll(debugDir, 0755)
		os.WriteFile(responsePath, jsonData, 0644)
		return
	}
	os.MkdirAll(debugDir, 0755)
	os.WriteFile(responsePath, prettyJSON.Bytes(), 0644)
}