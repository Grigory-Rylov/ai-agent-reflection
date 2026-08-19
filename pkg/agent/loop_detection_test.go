package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func makeLoopTestAgent(t *testing.T) (*agentImpl, *[]string) {
	t.Helper()
	a := NewAgent(Config{})
	sent := &[]string{}
	a.SetThinkingCallback(func(peerID int64, content string) error {
		*sent = append(*sent, content)
		return nil
	})
	return a, sent
}

func loopToolCalls(t *testing.T) []ToolCall {
	t.Helper()
	raw := []byte(`{"command":"ls"}`)
	return []ToolCall{{ID: "1", Type: "function", Function: ToolCallFunction{Name: "shell_execute", Arguments: raw}}}
}

func TestCheckResponseLoop(t *testing.T) {
	t.Run("no alert before threshold", func(t *testing.T) {
		a, sent := makeLoopTestAgent(t)
		for i := 0; i < loopThreshold-1; i++ {
			a.checkResponseLoop(1, "same response", "", nil)
		}
		if len(*sent) != 0 {
			t.Errorf("expected no alert, got %v", *sent)
		}
	})

	t.Run("alerts on threshold with text response", func(t *testing.T) {
		a, sent := makeLoopTestAgent(t)
		for i := 0; i < loopThreshold; i++ {
			a.checkResponseLoop(1, "same response", "", nil)
		}
		if len(*sent) != 1 {
			t.Fatalf("expected 1 alert, got %v", *sent)
		}
		if !strings.Contains((*sent)[0], "loop") {
			t.Errorf("alert should mention loop: %q", (*sent)[0])
		}
	})

	t.Run("alerts on repeated reasoning only", func(t *testing.T) {
		a, sent := makeLoopTestAgent(t)
		for i := 0; i < loopThreshold; i++ {
			a.checkResponseLoop(1, "", "same reasoning", nil)
		}
		if len(*sent) != 1 {
			t.Fatalf("expected 1 alert for reasoning loop, got %v", *sent)
		}
	})

	t.Run("alerts on repeated tool calls with empty text", func(t *testing.T) {
		a, sent := makeLoopTestAgent(t)
		calls := loopToolCalls(t)
		for i := 0; i < loopThreshold; i++ {
			a.checkResponseLoop(1, "", "", calls)
		}
		if len(*sent) != 1 {
			t.Fatalf("expected 1 alert for tool call loop, got %v", *sent)
		}
	})

	t.Run("no alert when tool call arguments differ", func(t *testing.T) {
		a, sent := makeLoopTestAgent(t)
		callsA := loopToolCalls(t)
		callsB := []ToolCall{{ID: "2", Type: "function", Function: ToolCallFunction{Name: "shell_execute", Arguments: json.RawMessage(`{"command":"pwd"}`)}}}
		for i := 0; i < loopThreshold; i++ {
			if i%2 == 0 {
				a.checkResponseLoop(1, "", "", callsA)
			} else {
				a.checkResponseLoop(1, "", "", callsB)
			}
		}
		if len(*sent) != 0 {
			t.Errorf("expected no alert for alternating calls, got %v", *sent)
		}
	})

	t.Run("no alert for different responses", func(t *testing.T) {
		a, sent := makeLoopTestAgent(t)
		a.checkResponseLoop(1, "first", "", nil)
		a.checkResponseLoop(1, "second", "", nil)
		a.checkResponseLoop(1, "third", "", nil)
		if len(*sent) != 0 {
			t.Errorf("expected no alert, got %v", *sent)
		}
	})

	t.Run("case and whitespace insensitive", func(t *testing.T) {
		a, sent := makeLoopTestAgent(t)
		variants := []string{"Same   Response", "same response", " SAME  RESPONSE "}
		for i := 0; i < loopThreshold; i++ {
			a.checkResponseLoop(1, variants[i%len(variants)], "", nil)
		}
		if len(*sent) != 1 {
			t.Fatalf("expected 1 alert, got %v", *sent)
		}
	})

	t.Run("alert sent only once per loop", func(t *testing.T) {
		a, sent := makeLoopTestAgent(t)
		for i := 0; i < loopThreshold+2; i++ {
			a.checkResponseLoop(1, "same", "", nil)
		}
		if len(*sent) != 1 {
			t.Errorf("expected exactly 1 alert, got %d: %v", len(*sent), *sent)
		}
	})

	t.Run("reset clears state", func(t *testing.T) {
		a, sent := makeLoopTestAgent(t)
		a.checkResponseLoop(1, "pair one", "", nil)
		a.resetResponseLoop(1)
		a.checkResponseLoop(1, "pair two", "", nil)
		if len(*sent) != 0 {
			t.Errorf("expected no alert after reset, got %v", *sent)
		}
	})

	t.Run("sessions are independent", func(t *testing.T) {
		a, sent := makeLoopTestAgent(t)
		a.checkResponseLoop(1, "same", "", nil)
		a.checkResponseLoop(2, "same", "", nil)
		a.checkResponseLoop(1, "other", "", nil)
		a.checkResponseLoop(2, "other", "", nil)
		if len(*sent) != 0 {
			t.Errorf("expected no alert, got %v", *sent)
		}
	})

	t.Run("empty responses are ignored", func(t *testing.T) {
		a, sent := makeLoopTestAgent(t)
		for i := 0; i < loopThreshold; i++ {
			a.checkResponseLoop(1, "", "", nil)
		}
		if len(*sent) != 0 {
			t.Errorf("expected no alert for empty responses, got %v", *sent)
		}
	})
}
