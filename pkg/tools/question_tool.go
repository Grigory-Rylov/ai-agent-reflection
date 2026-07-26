package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

var (
	questionCallback func(peerID int64, question map[string]interface{}) (map[string]interface{}, error)
	questionPeerID   int64
	questionMu       sync.RWMutex
)

func SetQuestionCallback(cb func(peerID int64, question map[string]interface{}) (map[string]interface{}, error)) {
	questionMu.Lock()
	defer questionMu.Unlock()
	questionCallback = cb
}

func SetQuestionPeerID(peerID int64) {
	questionMu.Lock()
	defer questionMu.Unlock()
	questionPeerID = peerID
}

func GetQuestionState() (func(peerID int64, question map[string]interface{}) (map[string]interface{}, error), int64) {
	questionMu.RLock()
	defer questionMu.RUnlock()
	return questionCallback, questionPeerID
}

func getQuestionState() (func(peerID int64, question map[string]interface{}) (map[string]interface{}, error), int64) {
	return GetQuestionState()
}

type QuestionTool struct{}

func (t *QuestionTool) Name() string {
	return "question"
}

func (t *QuestionTool) Description() string {
	return "Ask the user a question and get their response. " +
		"Use when you need clarification, approval, or input. " +
		"Supports multiple choice options and free-form text."
}

func (t *QuestionTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"question": CreateStringParameter("question", "The question to ask", true),
			"header":   CreateStringParameter("header", "Short label (max 30 chars)", false),
			"multiple": CreateBooleanParameter("multiple", "Allow multiple selections", false),
			"options": arrayParam("Answer options", map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"label":       CreateStringParameter("label", "Display text", true),
					"description": CreateStringParameter("description", "Explanation", false),
				},
				"required": []string{"label"},
			}),
		},
		"required": []string{"question"},
	}
}

func (t *QuestionTool) Execute(ctx context.Context, inputs map[string]string) (ToolResult, error) {
	question, ok := inputs["question"]
	if !ok || question == "" {
		return ToolResult{Success: false, Error: "question parameter is required"}, nil
	}

	cb, peerID := getQuestionState()
	if cb == nil {
		return ToolResult{
			Success: false,
			Error:   "question system unavailable",
		}, nil
	}

	q := buildQuestion(inputs)

	select {
	case <-ctx.Done():
		return ToolResult{Success: false, Error: "cancelled"}, nil
	default:
	}

	answer, err := cb(peerID, q)
	if err != nil {
		return ToolResult{
			Success: false,
			Error:   fmt.Sprintf("user response error: %v", err),
		}, nil
	}

	return ToolResult{Success: true, Data: answer}, nil
}

func buildQuestion(inputs map[string]string) map[string]interface{} {
	q := map[string]interface{}{
		"question": inputs["question"],
		"custom":   true,
	}
	if h, ok := inputs["header"]; ok && h != "" {
		q["header"] = h
	}
	if m, ok := inputs["multiple"]; ok && m != "" {
		q["multiple"] = m == "true"
	}
	if o, ok := inputs["options"]; ok && o != "" {
		var opts []map[string]interface{}
		if err := json.Unmarshal([]byte(o), &opts); err == nil {
			q["options"] = opts
			q["custom"] = false
		}
	}
	return q
}

func arrayParam(desc string, items map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":        "array",
		"description": desc,
		"items":       items,
	}
}
