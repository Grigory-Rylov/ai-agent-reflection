package agent

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/session"
)

func (a *agentImpl) collectStreamResponse(chunkChan <-chan StreamChunkEvent) (string, string, int, int, error) {
	logger.DebugToFile(a.agentPrefix()+"[LLM RESPONSE] Starting to collect stream response...")
	var fullResponse strings.Builder
	var fullReasoning strings.Builder
	var promptTokens, completionTokens int

	for event := range chunkChan {
		if event.IsError {
			logger.DebugToFile(a.agentPrefix()+"[LLM RESPONSE] Stream error: %s", event.Content)
			return "", "", 0, 0, fmt.Errorf("API error: %s (code: %s)", event.Content, event.ErrorCode)
		}
		if event.IsDone {
			promptTokens = event.PromptTokens
			completionTokens = event.CompletionTokens
			break
		}
		if event.Content != "" {
			fullResponse.WriteString(event.Content)
		}
		if event.ReasoningContent != "" {
			fullReasoning.WriteString(event.ReasoningContent)
		}
	}

	response := fullResponse.String()
	reasoning := fullReasoning.String()
	logger.DebugToFile(a.agentPrefix()+"[LLM RESPONSE] Collected: content=%d chars, reasoning=%d chars, in=%d, out=%d", len(response), len(reasoning), promptTokens, completionTokens)
	return response, reasoning, promptTokens, completionTokens, nil
}

func (a *agentImpl) collectStreamResponseWithToolCalls(chunkChan <-chan StreamChunkEvent) (string, string, string, []ToolCall, int, int, error) {
	var fullResponse strings.Builder
	var fullReasoning strings.Builder
	var finishReason string
	var allToolCalls []ToolCall
	var promptTokens, completionTokens int

	for event := range chunkChan {
		if event.IsError {
			return "", "", "", nil, 0, 0, fmt.Errorf("API error: %s (code: %s)", event.Content, event.ErrorCode)
		}
		if event.IsDone {
			finishReason = event.FinishReason
			promptTokens = event.PromptTokens
			completionTokens = event.CompletionTokens
			break
		}
		if event.Content != "" {
			fullResponse.WriteString(event.Content)
		}
		if event.ReasoningContent != "" {
			fullReasoning.WriteString(event.ReasoningContent)
		}
		if len(event.ToolCalls) > 0 {
			allToolCalls = MergeToolCalls(allToolCalls, event.ToolCalls)
		}
	}

	response := fullResponse.String()
	reasoning := fullReasoning.String()

	a.saveDebugResponse(response, reasoning, finishReason, allToolCalls)

	return response, reasoning, finishReason, allToolCalls, promptTokens, completionTokens, nil
}

func (a *agentImpl) saveDebugResponse(content, reasoning, finishReason string, toolCalls []ToolCall) {
	if !a.config.Debug {
		return
	}

	var sb strings.Builder
	sb.WriteString("=== LLM Response Debug ===\n\n")
	sb.WriteString(fmt.Sprintf("Finish Reason: %s\n\n", finishReason))
	sb.WriteString(fmt.Sprintf("Content (%d chars):\n", len(content)))
	sb.WriteString("---\n")
	sb.WriteString(content)
	sb.WriteString("\n---\n\n")
	sb.WriteString(fmt.Sprintf("Reasoning (%d chars):\n", len(reasoning)))
	sb.WriteString("---\n")
	sb.WriteString(reasoning)
	sb.WriteString("\n---\n\n")
	sb.WriteString(fmt.Sprintf("Tool Calls: %d\n", len(toolCalls)))
	for i, tc := range toolCalls {
		sb.WriteString(fmt.Sprintf("  %d. %s: %s\n", i+1, tc.Function.Name, ToolCallArgumentsStr(tc)))
	}

	if err := os.WriteFile("debug_response.txt", []byte(sb.String()), 0644); err != nil {
		a.debugLog.Debug("Failed to write debug_response.txt: %v", err)
	}
}

func (a *agentImpl) isNonToolResponse(finishReason string) bool {
	if finishReason == "" {
		return false
	}
	return !strings.Contains(finishReason, "tool")
}

func (a *agentImpl) returnTextResponse(session *session.Session, responseText string) FunctionCallResult {
	session.AddAssistantMessage(responseText)
	return FunctionCallResult{
		Success:  true,
		Response: responseText,
	}
}

func (a *agentImpl) stripThinkingTags(text string, peerID int64) string {
	if a.thinkingCallback == nil || !strings.Contains(text, "<thinking>") {
		return text
	}

	var clean strings.Builder
	var thinkingContent strings.Builder
	remaining := text

	for {
		startIdx := strings.Index(remaining, "<thinking>")
		if startIdx < 0 {
			clean.WriteString(remaining)
			break
		}

		clean.WriteString(remaining[:startIdx])
		afterOpen := remaining[startIdx+len("<thinking>"):]

		endIdx := strings.Index(afterOpen, "</thinking>")
		if endIdx < 0 {
			clean.WriteString("<thinking>" + afterOpen)
			break
		}

		thinkingContent.WriteString(afterOpen[:endIdx])
		thinkingContent.WriteString("\n")

		remaining = afterOpen[endIdx+len("</thinking>"):]
	}

	extracted := strings.TrimSpace(thinkingContent.String())
	if extracted != "" {
		a.thinkingCallback(peerID, extracted)
	}

	return strings.TrimSpace(clean.String())
}
