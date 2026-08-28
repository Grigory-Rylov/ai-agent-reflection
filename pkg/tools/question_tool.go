package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
)

var (
	questionCallback func(peerID int64, question map[string]interface{}) (map[string]interface{}, error)
	questionPeerID   int64
	questionMu       sync.RWMutex

	pendingQuestions   map[int64]chan map[string]interface{}
	pendingQuestionsMu sync.Mutex

	grantPersistPath func(peerID int64, path string)
	grantRemove      func(peerID int64)
)

func init() {
	pendingQuestions = make(map[int64]chan map[string]interface{})
}

func HasPendingQuestion(peerID int64) bool {
	pendingQuestionsMu.Lock()
	_, ok := pendingQuestions[peerID]
	pendingQuestionsMu.Unlock()
	return ok
}

func ResolvePendingQuestion(peerID int64, text string) bool {
	answer := map[string]interface{}{
		"answer":   text,
		"selected": []string{text},
	}

	pendingQuestionsMu.Lock()
	defer pendingQuestionsMu.Unlock()

	ch, ok := pendingQuestions[peerID]
	if !ok {
		return false
	}

	select {
	case ch <- answer:
		delete(pendingQuestions, peerID)
		return true
	default:
		return false
	}
}

func RegisterPendingQuestion(peerID int64) chan map[string]interface{} {
	ch := make(chan map[string]interface{}, 1)
	pendingQuestionsMu.Lock()
	pendingQuestions[peerID] = ch
	pendingQuestionsMu.Unlock()
	return ch
}

func UnregisterPendingQuestion(peerID int64) {
	pendingQuestionsMu.Lock()
	defer pendingQuestionsMu.Unlock()

	ch, ok := pendingQuestions[peerID]
	delete(pendingQuestions, peerID)
	if ok {
		close(ch)
	}
}

func IsPathGranted(peerID int64, toolPath string) bool {
	if toolPath == "" {
		return false
	}
	ctrl := GetAccessController()
	if ctrl == nil {
		return false
	}
	resolved, err := resolveRawPath(toolPath)
	if err != nil {
		return false
	}
	return ctrl.CheckAccessForPeer(peerID, resolved).Allowed
}

func ClearGrants(peerID int64) {
	ctrl := GetAccessController()
	if ctrl != nil {
		ctrl.ClearPeer(peerID)
	}
	if grantRemove != nil {
		grantRemove(peerID)
	}
}

func GrantPath(peerID int64, path string) {
	if path == "" {
		return
	}
	ctrl := GetAccessController()
	if ctrl != nil {
		ctrl.GrantPathForPeer(peerID, path)
	}
	if grantPersistPath != nil {
		grantPersistPath(peerID, path)
	}
}

func ApplyPathGrant(peerID int64, path string) {
	if path == "" {
		return
	}
	ctrl := GetAccessController()
	if ctrl != nil {
		ctrl.GrantPathForPeer(peerID, path)
	}
}

func resolveRawPath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		cleaned = filepath.Join(WorkingDir, cleaned)
	}
	return filepath.Clean(cleaned), nil
}

func SetGrantPersistence(persistPath func(peerID int64, path string), remove func(peerID int64)) {
	grantPersistPath = persistPath
	grantRemove = remove
}

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
