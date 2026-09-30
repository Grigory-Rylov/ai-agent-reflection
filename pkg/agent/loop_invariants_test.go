package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func pairedAssistantCall(id string) Message {
	return Message{Role: "assistant", ToolCalls: []ToolCall{
		{ID: id, Type: "function", Function: ToolCallFunction{Name: "bash", Arguments: []byte("{}")}},
	}}
}

func TestRepairToolCallPairing(t *testing.T) {
	tests := []struct {
		name          string
		messages      []Message
		wantLen       int
		wantRecovered int
	}{
		{
			name: "paired call untouched",
			messages: []Message{
				{Role: "user", Content: "run"},
				pairedAssistantCall("call_1"),
				{Role: "tool", ToolCallID: "call_1", Name: "bash", Content: "done"},
			},
			wantLen:       3,
			wantRecovered: 0,
		},
		{
			name: "missing result gets recovered message",
			messages: []Message{
				{Role: "user", Content: "run"},
				pairedAssistantCall("call_1"),
				{Role: "user", Content: "next"},
			},
			wantLen:       4,
			wantRecovered: 1,
		},
		{
			name: "tool result directly after assistant",
			messages: []Message{
				pairedAssistantCall("call_2"),
				{Role: "tool", ToolCallID: "call_2", Content: "ok"},
			},
			wantLen:       2,
			wantRecovered: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := repairToolCallPairing(tt.messages)
			if len(out) != tt.wantLen {
				t.Fatalf("expected %d messages, got %d", tt.wantLen, len(out))
			}
			recovered := 0
			for _, m := range out {
				if strings.Contains(m.Content, "[RECOVERED]") {
					recovered++
					if m.Role != "tool" {
						t.Errorf("recovered message must have tool role, got %s", m.Role)
					}
				}
			}
			if recovered != tt.wantRecovered {
				t.Errorf("expected %d recovered messages, got %d", tt.wantRecovered, recovered)
			}
		})
	}
}

func TestRepairToolCallPairingTwoCallsOneMissing(t *testing.T) {
	messages := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "bash", Arguments: []byte("{}")}},
			{ID: "call_2", Type: "function", Function: ToolCallFunction{Name: "time_get", Arguments: []byte("{}")}},
		}},
		{Role: "tool", ToolCallID: "call_2", Name: "time_get", Content: "12:00"},
	}

	out := repairToolCallPairing(messages)
	if len(out) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(out))
	}
	if out[1].Role != "tool" || out[1].ToolCallID != "call_1" {
		t.Errorf("expected recovered tool message for call_1 right after assistant, got %+v", out[1])
	}
}

func TestResolveToolCalls_MalformedCorrectionCappedAtThree(t *testing.T) {
	a := NewAgent(Config{})
	s := session.NewSession(session.DefaultConfig())
	s.UpdateSystemPrompt("sys")
	tc := a.newTurnContext(s)

	round := roundResult{text: "I will run it now.\n<tool_call>", finishReason: "stop"}

	for i := 0; i < maxMalformedCallRetries; i++ {
		calls := a.resolveToolCalls(context.Background(), tc, nil, round)
		if !calls.retryRequested {
			t.Fatalf("round %d: expected retryRequested=true for malformed response", i+1)
		}
	}

	calls := a.resolveToolCalls(context.Background(), tc, nil, round)
	if calls.retryRequested {
		t.Error("fourth malformed response must not request another correction")
	}
	if n := countSessionMessagesWithContent(s.GetHistory(), formatCorrectionMessage); n != maxMalformedCallRetries {
		t.Errorf("expected exactly %d format corrections in session, got %d", maxMalformedCallRetries, n)
	}
}

func TestRunTurn_LengthTruncationCappedAtEight(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"chunk-%d \"},\"finish_reason\":\"length\"}]}\n\n", n)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 4096

	a := NewAgent(config)
	s := session.NewSession(session.DefaultConfig())
	s.UpdateSystemPrompt("sys")
	s.AddUserMessage("write a long report")

	result, err := a.runTurn(context.Background(), s)
	if err != nil {
		t.Fatalf("runTurn: %v", err)
	}

	if got := int(calls.Load()); got != maxLengthContinuations+1 {
		t.Errorf("expected %d LLM calls (8 continuations + final), got %d", maxLengthContinuations+1, got)
	}
	if n := countSessionMessagesWithContent(s.GetHistory(), lengthTruncationContinuation); n != maxLengthContinuations {
		t.Errorf("expected exactly %d continuation prompts, got %d", maxLengthContinuations, n)
	}
	if result == "" {
		t.Error("expected non-empty final response after length cap")
	}
}

func TestConvertHistoryToAPIMessages_CompactionRendering(t *testing.T) {
	s := session.NewSession(session.DefaultConfig())
	s.UpdateSystemPrompt("BASE")
	s.AddUserMessage("old question")
	s.AddAssistantMessage("old answer")
	s.MarkCompaction(3, "Earlier discussion was compacted.")
	s.AddUserMessage("new question")

	api := NewAgent(Config{}).convertHistoryToAPIMessages(s.GetContextMessages())

	for _, m := range api {
		if m.Content == tokenizers.CompactionUserMessage {
			t.Fatal("compaction marker must not reach the API payload")
		}
		if strings.Contains(m.Content, "old question") {
			t.Error("compacted messages must not reach the API payload")
		}
	}

	rendered := compress.RenderSummaryForContext("Earlier discussion was compacted.")
	summaryCount := 0
	summaryIdx, newIdx := -1, -1
	for i, m := range api {
		if m.Content == rendered {
			summaryCount++
			summaryIdx = i
			if m.Role != "user" {
				t.Errorf("summary must be rendered as user message, got %s", m.Role)
			}
		}
		if m.Content == "new question" {
			newIdx = i
		}
	}
	if summaryCount != 1 {
		t.Fatalf("expected exactly 1 rendered summary message, got %d", summaryCount)
	}
	if summaryIdx > newIdx {
		t.Errorf("summary (%d) must come before the new tail message (%d)", summaryIdx, newIdx)
	}
}
