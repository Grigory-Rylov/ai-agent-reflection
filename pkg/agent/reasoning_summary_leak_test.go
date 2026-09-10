package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/internalmsg"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

const leakedSummaryEcho = internalmsg.Label + "\n" +
	"- The user asked for a calculation, so I invoked calc with expression 2+2.\n" +
	"- The stub tool returned success and I will report the result."

func newSummaryLeakAgent(t *testing.T, peerID int64, mainCall func(w http.ResponseWriter, call int)) (*agentImpl, *StubToolExecutor) {
	t.Helper()

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), reasoningSummaryPrompt) {
			writeSummaryLLMResponse(w, "SUMMARY-OK")
			return
		}
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		mainCall(w, callCount)
		w.Write([]byte("[DONE]\n"))
	}))
	t.Cleanup(server.Close)

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.RetryDelay = 5 * time.Millisecond
	config.EnableTools = true
	config.EnableCompression = false
	config.SummarizeReasoning = true
	config.SessionConfig = session.DefaultConfig()
	config.SessionConfig.PeerID = peerID

	return newTestAgentWithStub(t, config)
}

func TestLastAssistantContentSkipsUnpublishableMessages(t *testing.T) {
	realAnswer := session.Message{Role: session.AssistantRole, Content: "Real answer for the user"}
	summary := session.Message{Role: session.AssistantRole, Content: internalmsg.Label + "\n- decided to inspect the files"}
	internalMsg := session.Message{Role: session.AssistantRole, Content: "Flagged internal without label", Internal: true}
	secondSummary := session.Message{Role: session.AssistantRole, Content: internalmsg.Label + "\n- second summary body"}
	toolCallTagged := session.Message{Role: session.AssistantRole, Content: "Let me compute.\n<tool_call>\n<function=calc>\n</function>\n</tool_call>"}
	functionTagged := session.Message{Role: session.AssistantRole, Content: "invoking <function=calc> now"}
	userQuestion := session.Message{Role: session.UserRole, Content: "what is 2+2?"}
	emptyAssistant := session.Message{Role: session.AssistantRole, Content: ""}

	cases := []struct {
		name    string
		history []session.Message
		want    string
		wantOK  bool
	}{
		{
			name:    "trailing summary is skipped and earlier answer returned",
			history: []session.Message{userQuestion, realAnswer, summary},
			want:    "Real answer for the user",
			wantOK:  true,
		},
		{
			name:    "several trailing summaries skipped down to real answer",
			history: []session.Message{userQuestion, realAnswer, summary, secondSummary},
			want:    "Real answer for the user",
			wantOK:  true,
		},
		{
			name:    "internal flagged message without label is skipped",
			history: []session.Message{userQuestion, realAnswer, internalMsg},
			want:    "Real answer for the user",
			wantOK:  true,
		},
		{
			name:    "only summary in history yields nothing",
			history: []session.Message{userQuestion, summary},
			want:    "",
			wantOK:  false,
		},
		{
			name:    "assistant message with tool_call tags skipped",
			history: []session.Message{userQuestion, realAnswer, toolCallTagged},
			want:    "Real answer for the user",
			wantOK:  true,
		},
		{
			name:    "assistant message with function tags only yields nothing",
			history: []session.Message{userQuestion, functionTagged},
			want:    "",
			wantOK:  false,
		},
		{
			name:    "empty assistant message skipped",
			history: []session.Message{userQuestion, realAnswer, emptyAssistant},
			want:    "Real answer for the user",
			wantOK:  true,
		},
		{
			name:    "no assistant messages yield nothing",
			history: []session.Message{userQuestion},
			want:    "",
			wantOK:  false,
		},
		{
			name:    "tool messages are not candidates",
			history: []session.Message{userQuestion, {Role: session.ToolRole, Content: "tool output", Name: "calc"}},
			want:    "",
			wantOK:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := session.NewSession(session.DefaultConfig())
			s.RestoreMessages(tc.history)

			got, ok := lastAssistantContent(s)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (content %q)", ok, tc.wantOK, got)
			}
			if got != tc.want {
				t.Errorf("content = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProcessToolResultsNeverReturnsReasoningSummary(t *testing.T) {
	t.Run("summary echoed as final content is never sent to user", func(t *testing.T) {
		const peerID = 424221
		a, _ := newSummaryLeakAgent(t, peerID, func(w http.ResponseWriter, call int) {
			switch call {
			case 1:
				writeMainCallStream(w, longTestReasoning, "FIRST ANSWER")
			case 2:
				writeNativeCallEvent(w, "calc_call", "calc", `{"expression": "2 + 2"}`)
			default:
				writeMainCallStream(w, "", leakedSummaryEcho)
			}
		})

		if _, err := a.ProcessMessage(context.Background(), "hello", peerID); err != nil {
			t.Fatalf("first ProcessMessage failed: %v", err)
		}

		resp, err := a.ProcessMessage(context.Background(), "compute it", peerID)
		if err != nil {
			t.Fatalf("second ProcessMessage failed: %v", err)
		}

		if strings.Contains(resp, internalmsg.Label) {
			t.Fatalf("BUG: reasoning summary leaked to user: %q", resp)
		}
		if strings.Contains(resp, "invoked calc") {
			t.Fatalf("BUG: summary body leaked to user: %q", resp)
		}
		if resp != "FIRST ANSWER" {
			t.Errorf("expected fallback to earlier real assistant answer, got %q", resp)
		}
	})

	t.Run("real answer after echoed summary block is returned", func(t *testing.T) {
		const peerID = 424222
		a, _ := newSummaryLeakAgent(t, peerID, func(w http.ResponseWriter, call int) {
			switch call {
			case 1:
				writeMainCallStream(w, longTestReasoning, "FIRST ANSWER")
			case 2:
				writeNativeCallEvent(w, "calc_call", "calc", `{"expression": "2 + 2"}`)
			default:
				writeMainCallStream(w, "", "The calculation result is 4.\n\n"+leakedSummaryEcho)
			}
		})

		if _, err := a.ProcessMessage(context.Background(), "hello", peerID); err != nil {
			t.Fatalf("first ProcessMessage failed: %v", err)
		}

		resp, err := a.ProcessMessage(context.Background(), "compute it", peerID)
		if err != nil {
			t.Fatalf("second ProcessMessage failed: %v", err)
		}

		if strings.Contains(resp, internalmsg.Label) {
			t.Fatalf("BUG: reasoning summary leaked to user: %q", resp)
		}
		if resp != "The calculation result is 4." {
			t.Errorf("response = %q, want %q", resp, "The calculation result is 4.")
		}
	})
}
