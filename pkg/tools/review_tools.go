package tools

import (
	"context"
)

type ReviewApproveTool struct{}

func (t *ReviewApproveTool) Name() string {
	return "review_approve"
}

func (t *ReviewApproveTool) Description() string {
	return "Call this when the review is complete and the work is approved. No further changes needed."
}

func (t *ReviewApproveTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"message": CreateStringParameter("message", "Optional approval message or summary", false),
		},
	}
}

func (t *ReviewApproveTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	message := "Работа одобрена. Замечаний нет."
	if m, ok := inputs["message"]; ok && m != "" {
		message = m
	}

	return ToolResult{
		Success: true,
		Data: map[string]interface{}{
			"approved": true,
			"message":  message,
		},
	}, nil
}
