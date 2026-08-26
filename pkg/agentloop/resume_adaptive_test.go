package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type captureLLMServer struct {
	url     string
	mu      sync.Mutex
	bodies  []string
	content string
}

func newCaptureLLMServer(t *testing.T, content string) *captureLLMServer {
	t.Helper()
	srv := &captureLLMServer{content: content}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		srv.mu.Lock()
		srv.bodies = append(srv.bodies, string(body))
		srv.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", srv.content)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(ts.Close)
	srv.url = ts.URL
	return srv
}

func (s *captureLLMServer) joinedBodies() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.bodies, "\n")
}

func makeOrcWithPromptFiles(t *testing.T, llmHost string, st store.Store, vk VKClient) *Orchestrator {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"worker.txt", "qa.txt", "coordinator.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("You are a helpful assistant."), 0644); err != nil {
			t.Fatal(err)
		}
	}
	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: llmHost},
		},
	})
	reg := tools.NewRegistry()
	reg.Register(&tools.FileReadTool{})
	reg.Register(&tools.TimeGetTool{})
	return NewOrchestrator(OrchestratorConfig{
		ModelHolder:     modelHolder,
		MaxTokens:       8192,
		Temperature:     0.7,
		ToolRegistry:    reg,
		Debug:           false,
		SystemPromptDir: dir,
		Store:           st,
		VKClient:        vk,
	})
}

func assistantWithCalls(calls ...sess.MsgToolCall) sess.Message {
	msg := sess.Message{Role: sess.AssistantRole, Content: "calling tools"}
	msg.ToolCalls = calls
	return msg
}

func mkCall(id, name, args string) sess.MsgToolCall {
	return sess.MsgToolCall{ID: id, Type: "function", Function: sess.MsgToolCallFunc{Name: name, Arguments: args}}
}

func toolResultMsg(callID, name string) sess.Message {
	return sess.Message{Role: sess.ToolRole, ToolCallID: callID, Name: name, Content: "result"}
}

func serializeSessionForTest(t *testing.T, msgs []sess.Message) string {
	t.Helper()
	data, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	return string(data)
}

func TestSanitizeRestoredMessages(t *testing.T) {
	userHello := sess.Message{Role: sess.UserRole, Content: "hello"}
	pairA := assistantWithCalls(mkCall("ca1", "file_read", `{"path":"/a"}`), mkCall("ca2", "file_read", `{"path":"/a2"}`))
	pairB := assistantWithCalls(mkCall("cb1", "file_read", `{"path":"/b"}`))
	pairC := assistantWithCalls(mkCall("cc1", "file_read", `{"path":"/c"}`))

	tests := []struct {
		name      string
		input     []sess.Message
		wantRoles []sess.Role
	}{
		{
			name:      "drops trailing assistant with unmatched tool calls",
			input:     []sess.Message{userHello, pairA, toolResultMsg(pairA.ToolCalls[0].ID, "file_read"), pairB},
			wantRoles: []sess.Role{sess.UserRole, sess.AssistantRole, sess.ToolRole},
		},
		{
			name:      "drops several stacked dangling assistant tool-call messages",
			input:     []sess.Message{userHello, pairA, toolResultMsg(pairA.ToolCalls[0].ID, "file_read"), pairB, pairC},
			wantRoles: []sess.Role{sess.UserRole, sess.AssistantRole, sess.ToolRole},
		},
		{
			name:      "keeps matched tool result pairs intact",
			input:     []sess.Message{userHello, pairA, toolResultMsg(pairA.ToolCalls[0].ID, "file_read"), pairB, toolResultMsg(pairB.ToolCalls[0].ID, "file_read")},
			wantRoles: []sess.Role{sess.UserRole, sess.AssistantRole, sess.ToolRole, sess.AssistantRole, sess.ToolRole},
		},
		{
			name:      "plain assistant tail untouched",
			input:     []sess.Message{userHello, pairA, toolResultMsg(pairA.ToolCalls[0].ID, "file_read"), {Role: sess.AssistantRole, Content: "final answer"}},
			wantRoles: []sess.Role{sess.UserRole, sess.AssistantRole, sess.ToolRole, sess.AssistantRole},
		},
		{
			name:      "multi-call assistant kept when all results present",
			input:     []sess.Message{userHello, pairA, toolResultMsg(pairA.ToolCalls[0].ID, "file_read"), toolResultMsg(pairA.ToolCalls[1].ID, "file_read")},
			wantRoles: []sess.Role{sess.UserRole, sess.AssistantRole, sess.ToolRole, sess.ToolRole},
		},
		{
			name:      "empty history yields empty",
			input:     nil,
			wantRoles: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeRestoredMessages(tt.input)
			if len(got) != len(tt.wantRoles) {
				t.Fatalf("len = %d, want %d: roles=%v", len(got), len(tt.wantRoles), rolesOf(got))
			}
			for i := range tt.wantRoles {
				if got[i].Role != tt.wantRoles[i] {
					t.Errorf("msg %d role = %s, want %s", i, got[i].Role, tt.wantRoles[i])
				}
			}
		})
	}
}

func rolesOf(msgs []sess.Message) []sess.Role {
	out := make([]sess.Role, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role
	}
	return out
}

func TestRestoreSessionMessages_TrimsCrashedTail(t *testing.T) {
	o := makeOrcWithPromptFiles(t, "http://127.0.0.1:1", nil, nil)

	dangling := assistantWithCalls(mkCall("tc-crash", "file_read", `{"path":"/crashed"}`))
	payload := serializeSessionForTest(t, []sess.Message{
		{Role: sess.SystemRole, Content: "You are a helpful assistant."},
		{Role: sess.UserRole, Content: "do X"},
		assistantWithCalls(mkCall("tc-a", "file_read", `{"path":"/a"}`)),
		toolResultMsg("tc-a", "file_read"),
		dangling,
	})

	s := sess.NewSession(sess.DefaultConfig())
	o.restoreSessionMessages(s, payload)

	history := s.GetHistory()
	if len(history) != 4 {
		t.Fatalf("history len = %d, want 4: roles=%v", len(history), rolesOf(history))
	}
	if history[len(history)-1].Role != sess.ToolRole {
		t.Errorf("last message role = %s, want tool", history[len(history)-1].Role)
	}
	for _, m := range history {
		if m.Role == sess.AssistantRole && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				found := false
				for _, tm := range history {
					if tm.Role == sess.ToolRole && tm.ToolCallID == tc.ID {
						found = true
					}
				}
				if !found {
					t.Errorf("restored history contains dangling tool call %s without result", tc.ID)
				}
			}
		}
	}
}

func TestRestoreSessionMessages_UnparsablePayloadLeavesSessionUntouched(t *testing.T) {
	o := makeOrcWithPromptFiles(t, "http://127.0.0.1:1", nil, nil)
	s := sess.NewSession(sess.DefaultConfig())
	s.AddUserMessage("pre-existing")
	s.AddAssistantMessage("kept")

	before := s.GetHistory()
	o.restoreSessionMessages(s, "{broken-json")
	o.restoreSessionMessages(s, "")

	after := s.GetHistory()
	if len(after) != len(before) {
		t.Fatalf("history mutated despite bad payload: before=%v after=%v", rolesOf(before), rolesOf(after))
	}
	for i := range before {
		if after[i].Role != before[i].Role || after[i].Content != before[i].Content {
			t.Fatalf("message %d changed: %+v -> %+v", i, before[i], after[i])
		}
	}
}

func TestPickResumeContinuationPrompt(t *testing.T) {
	const defaultText = "The process was restarted. Continue your task from where you left off."

	tests := []struct {
		name         string
		childResult  string
		lastRole     sess.Role
		lastToolCall string
		lastPrompt   string
		wantContains string
	}{
		{name: "tool-role tail prefers tool-results wording", lastRole: sess.ToolRole, lastPrompt: "build it", wantContains: "review their results"},
		{name: "assistant tail falls back to last prompt", lastRole: sess.AssistantRole, lastPrompt: "build it", wantContains: "Continue your task: build it"},
		{name: "assistant tail without prompt uses default", lastRole: sess.AssistantRole, lastToolCall: "file_read", wantContains: defaultText},
		{name: "empty history uses default", lastRole: "", lastToolCall: "file_read", lastPrompt: "anything", wantContains: defaultText},
		{name: "child result wins over everything", lastRole: sess.ToolRole, lastToolCall: "file_read", lastPrompt: "build it", childResult: "CHILD-DONE", wantContains: "CHILD-DONE"},
		{name: "user-role tail falls back to last prompt", lastRole: sess.UserRole, lastPrompt: "investigate", wantContains: "Continue your task: investigate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickResumeContinuationPrompt(tt.childResult, tt.lastRole, tt.lastToolCall, tt.lastPrompt)
			if !strings.Contains(got, tt.wantContains) {
				t.Errorf("prompt = %q, want substring %q", got, tt.wantContains)
			}
			if tt.childResult != "" && !strings.Contains(got, "sub-agent completed") {
				t.Errorf("child result case lost framing: %q", got)
			}
		})
	}
}

func seedCrashedWorkerSession(t *testing.T, st store.Store, peerID int64, id, lastPrompt, msgsJSON string) {
	t.Helper()
	now := time.Now()
	if err := st.SaveAgentSession(&store.AgentSessionData{
		ID:           id,
		AgentName:    "worker",
		PeerID:       peerID,
		SystemPrompt: "You are a helpful assistant.",
		LastPrompt:   lastPrompt,
		Status:       "active",
		CreatedAt:    now,
		UpdatedAt:    now,
		Messages:     msgsJSON,
	}); err != nil {
		t.Fatalf("SaveAgentSession: %v", err)
	}
	if err := st.SaveAgentChain(peerID, []string{id}); err != nil {
		t.Fatalf("SaveAgentChain: %v", err)
	}
}

func TestRunResumedAgent_ToolTailGetsAdaptivePrompt(t *testing.T) {
	server := newCaptureLLMServer(t, "Resumed result")
	vkClient := NewStubVKClient(filepath.Join(t.TempDir(), "vk.log"))
	dbStore := newSubAgentToolTestStore(t)
	orchestrator := makeOrcWithPromptFiles(t, server.url, dbStore, vkClient)

	crashedTail := assistantWithCalls(mkCall("tc-crash", "file_read", `{"path":"/never-finished"}`))
	msgsJSON := serializeSessionForTest(t, []sess.Message{
		{Role: sess.SystemRole, Content: "You are a helpful assistant."},
		{Role: sess.UserRole, Content: "analyze the code"},
		assistantWithCalls(mkCall("tc-a", "file_read", `{"path":"/done"}`)),
		toolResultMsg("tc-a", "file_read"),
		crashedTail,
	})
	seedCrashedWorkerSession(t, dbStore, 4242, "worker-resume-adapt", "analyze the code", msgsJSON)

	if err := orchestrator.ResumeActiveChains(context.Background()); err != nil {
		t.Fatalf("ResumeActiveChains: %v", err)
	}

	combined := server.joinedBodies()
	if !strings.Contains(combined, "review their results") {
		t.Errorf("adaptive tool-result continuation prompt missing from LLM requests: %s", combined)
	}
	if !vkClient.Contains("Resumed result") {
		t.Errorf("expected resumed result delivery, log: %v", vkClient.ReadLog())
	}
}

func TestRunResumedAgent_LastPromptUsedWhenNoToolTail(t *testing.T) {
	server := newCaptureLLMServer(t, "Resumed result")
	vkClient := NewStubVKClient(filepath.Join(t.TempDir(), "vk.log"))
	dbStore := newSubAgentToolTestStore(t)
	orchestrator := makeOrcWithPromptFiles(t, server.url, dbStore, vkClient)

	msgsJSON := serializeSessionForTest(t, []sess.Message{
		{Role: sess.SystemRole, Content: "You are a helpful assistant."},
		{Role: sess.UserRole, Content: "draft report"},
		{Role: sess.AssistantRole, Content: "partial draft"},
	})
	seedCrashedWorkerSession(t, dbStore, 4243, "worker-lp", "draft quarterly report", msgsJSON)

	if err := orchestrator.ResumeActiveChains(context.Background()); err != nil {
		t.Fatalf("ResumeActiveChains: %v", err)
	}

	combined := server.joinedBodies()
	if !strings.Contains(combined, "Continue your task: draft quarterly report") {
		t.Errorf("last-prompt continuation missing from LLM requests: %s", combined)
	}
}
