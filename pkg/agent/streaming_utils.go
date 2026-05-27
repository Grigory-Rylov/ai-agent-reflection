package agent

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencode/llama-client/pkg/logger"
	"github.com/opencode/llama-client/session"
)

func (a *agentImpl) collectStreamResponse(chunkChan <-chan StreamChunkEvent) (string, string, error) {
	logger.DebugToFile("[LLM RESPONSE] Starting to collect stream response...")
	var fullResponse strings.Builder
	var fullReasoning strings.Builder

	for event := range chunkChan {
		if event.IsError {
			logger.DebugToFile("[LLM RESPONSE] Stream error: %s", event.Content)
			return "", "", fmt.Errorf("API error: %s (code: %s)", event.Content, event.ErrorCode)
		}
		if event.IsDone {
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
	logger.DebugToFile("[LLM RESPONSE] Collected: content=%d chars, reasoning=%d chars", len(response), len(reasoning))
	return response, reasoning, nil
}

func (a *agentImpl) collectStreamResponseWithToolCalls(chunkChan <-chan StreamChunkEvent) (string, string, string, []ToolCall, error) {
	var fullResponse strings.Builder
	var fullReasoning strings.Builder
	var finishReason string
	var allToolCalls []ToolCall

	for event := range chunkChan {
		if event.IsError {
			return "", "", "", nil, fmt.Errorf("API error: %s (code: %s)", event.Content, event.ErrorCode)
		}
		if event.IsDone {
			finishReason = event.FinishReason
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

	return response, reasoning, finishReason, allToolCalls, nil
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
		fmt.Printf("[DEBUG] Failed to write debug_response.txt: %v\n", err)
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
