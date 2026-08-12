package agent

import (
	"fmt"
	"os"
	"strings"
)

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
		// Контент/reasoning/tool_calls обрабатываем ДО проверки IsDone: модель
		// может прислать финальный фрагмент контента вместе с finish_reason,
		// иначе он был бы потерян.
		if event.Content != "" {
			fullResponse.WriteString(event.Content)
		}
		if event.ReasoningContent != "" {
			fullReasoning.WriteString(event.ReasoningContent)
		}
		if len(event.ToolCalls) > 0 {
			allToolCalls = MergeToolCalls(allToolCalls, event.ToolCalls)
		}
		if event.IsDone {
			finishReason = event.FinishReason
			promptTokens = event.PromptTokens
			completionTokens = event.CompletionTokens
			break
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

	os.MkdirAll("debug", 0755)
	if err := os.WriteFile("debug/debug_response.txt", []byte(sb.String()), 0644); err != nil {
		a.debugLog.Debug("Failed to write debug/debug_response.txt: %v", err)
	}
}

func (a *agentImpl) isNonToolResponse(finishReason string) bool {
	if finishReason == "" {
		return false
	}
	return !strings.Contains(finishReason, "tool")
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
