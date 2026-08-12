package tools

import (
	"context"
	"testing"
	"time"
)

func TestQuestionTool_Name(t *testing.T) {
	tool := &QuestionTool{}
	if tool.Name() != "question" {
		t.Errorf("expected 'question', got %s", tool.Name())
	}
}

func TestQuestionTool_Schema(t *testing.T) {
	tool := &QuestionTool{}
	schema := tool.Schema()
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expected properties")
	}
	if _, ok := props["question"]; !ok {
		t.Error("expected question property")
	}
}

func TestQuestionTool_NoCallback(t *testing.T) {
	SetQuestionCallback(nil)

	tool := &QuestionTool{}
	_, err := tool.Execute(context.Background(), map[string]string{
		"question": "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestQuestionTool_NoQuestion(t *testing.T) {
	tool := &QuestionTool{}
	result, err := tool.Execute(context.Background(), map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure without question")
	}
}

func TestQuestionTool_WithCallback(t *testing.T) {
	callCount := 0
	SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		callCount++
		if peerID != 42 {
			t.Errorf("expected peerID 42, got %d", peerID)
		}
		if q["question"] != "Are you sure?" {
			t.Errorf("unexpected question: %v", q["question"])
		}
		return map[string]interface{}{"answer": "yes", "selected": []string{"yes"}}, nil
	})
	SetQuestionPeerID(42)
	defer SetQuestionCallback(nil)

	tool := &QuestionTool{}
	result, err := tool.Execute(context.Background(), map[string]string{
		"question": "Are you sure?",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatal("expected success")
	}
	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
}

func TestQuestionTool_CancelledContext(t *testing.T) {
	SetQuestionCallback(func(peerID int64, q map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"answer": "ok"}, nil
	})
	defer SetQuestionCallback(nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tool := &QuestionTool{}
	result, err := tool.Execute(ctx, map[string]string{"question": "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected failure on cancelled context")
	}
}

func TestBuildQuestion(t *testing.T) {
	q := buildQuestion(map[string]string{"question": "Pick one"})
	if q["question"] != "Pick one" {
		t.Error("wrong question")
	}
	if q["custom"] != true {
		t.Error("expected custom=true")
	}
}

func TestBuildQuestionWithOptions(t *testing.T) {
	q := buildQuestion(map[string]string{
		"question": "Pick",
		"options":  `[{"label":"A","description":"First"}]`,
	})
	if q["custom"] != false {
		t.Error("expected custom=false with options")
	}
	opts, ok := q["options"].([]map[string]interface{})
	if !ok {
		t.Fatal("expected options array")
	}
	if len(opts) != 1 || opts[0]["label"] != "A" {
		t.Error("wrong option")
	}
}

func TestSetGetQuestionPeerID(t *testing.T) {
	SetQuestionPeerID(0)
	_, pid := getQuestionState()
	if pid != 0 {
		t.Errorf("expected 0, got %d", pid)
	}

	SetQuestionPeerID(99)
	_, pid = getQuestionState()
	if pid != 99 {
		t.Errorf("expected 99, got %d", pid)
	}
}

func TestResolvePendingQuestionIsSingleShot(t *testing.T) {
	peerID := int64(7)
	RegisterPendingQuestion(peerID)
	defer UnregisterPendingQuestion(peerID)

	if !ResolvePendingQuestion(peerID, "first") {
		t.Fatal("expected first resolve to succeed")
	}
	if HasPendingQuestion(peerID) {
		t.Error("expected question unregistered after successful resolve")
	}
	if ResolvePendingQuestion(peerID, "second") {
		t.Error("expected second resolve to fail after question was already resolved")
	}
}

func TestUnregisterPendingQuestionUnblocksWaiter(t *testing.T) {
	peerID := int64(8)
	ch := RegisterPendingQuestion(peerID)

	waitDone := make(chan struct{})
	go func() {
		_, ok := <-ch
		if ok {
			t.Error("expected closed channel (cancelled question)")
		}
		close(waitDone)
	}()

	UnregisterPendingQuestion(peerID)

	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Error("expected waiter to be unblocked after UnregisterPendingQuestion")
	}
}

func TestArrayParam(t *testing.T) {
	p := arrayParam("test items", map[string]interface{}{
		"type": "string",
	})
	if p["type"] != "array" {
		t.Error("expected array type")
	}
	if p["description"] != "test items" {
		t.Error("wrong description")
	}
}
