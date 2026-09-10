package agentloop

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

type hangingLLM struct {
	server     *httptest.Server
	mu         sync.Mutex
	hangCalls  int
	releaseAll bool
	released   chan struct{}
}

func newHangingLLM(t *testing.T, toolChunk string) *hangingLLM {
	t.Helper()

	h := &hangingLLM{released: make(chan struct{})}

	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "chat/completions") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{}`))
			return
		}

		h.mu.Lock()
		h.hangCalls++
		call := h.hangCalls
		h.mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")

		if call == 1 {
			fmt.Fprint(w, "data: "+toolChunk+"\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}

		select {
		case <-h.released:
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"late\"},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(h.server.Close)
	t.Cleanup(h.release)

	return h
}

func (h *hangingLLM) release() {
	h.mu.Lock()
	h.releaseAll = true
	h.mu.Unlock()
	select {
	case <-h.released:
	default:
		close(h.released)
	}
}

func newCrashTestLoop(t *testing.T, st store.Store, url string) *agentLoop {
	t.Helper()

	modelHolder := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models: map[string]modelsconfig.ModelEntry{
			"test": {Name: "test-model", Host: url},
		},
	})

	reg := tools.NewRegistry()
	reg.Register(&tools.TimeGetTool{})

	loop, err := NewAgentLoop(LoopConfig{
		ModelHolder:       modelHolder,
		MaxTokens:         8192,
		Temperature:       0.7,
		EnableTools:       true,
		EnableCompression: false,
		SessionConfig:     sessionConfigForStore(st),
	}, nil, reg)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	return loop.(*agentLoop)
}

const timeToolCallChunk = `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"time_get","arguments":"{}"}}]},"finish_reason":null}]}`

func waitForStoreMessages(t *testing.T, st store.Store, peerID int64, want int) []store.MessageData {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msgs, err := st.GetMessages(peerID)
		if err != nil {
			t.Fatalf("GetMessages: %v", err)
		}
		if len(msgs) >= want {
			return msgs
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("store did not reach %d messages in time", want)
	return nil
}

func TestTurnMirrorPersistsToolRoundBeforeTurnEnds(t *testing.T) {
	st := newSubAgentToolTestStore(t)
	llm := newHangingLLM(t, timeToolCallChunk)
	loop := newCrashTestLoop(t, st, llm.server.URL)

	peerID := int64(91001)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		loop.ProcessPrompt(ctx, "который час?", peerID)
	}()

	msgs := waitForStoreMessages(t, st, peerID, 4)

	roles := make([]string, 0, len(msgs))
	toolCallsSeen := false
	for _, m := range msgs {
		roles = append(roles, m.Role)
		if m.Role == "tool" && m.ToolName == "time_get" {
			toolCallsSeen = true
		}
	}
	if !toolCallsSeen {
		t.Fatalf("expected tool result persisted mid-turn, roles=%v", roles)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ProcessPrompt did not return after cancellation")
	}

	after, err := st.GetMessages(peerID)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(after) < 4 {
		t.Fatalf("cancellation dropped mirrored history: %d messages", len(after))
	}
	if after[len(after)-1].Role == "tool" && after[len(after)-1].ToolName == "time_get" {
		return
	}
	for _, m := range after {
		if m.Role == "tool" && m.ToolName == "time_get" {
			return
		}
	}
	t.Fatalf("tool result lost after cancellation: %+v", after)
}

func TestResumePromptClearedOnlyAfterSuccessfulTurn(t *testing.T) {
	st := newSubAgentToolTestStore(t)
	llm := newHangingLLM(t, timeToolCallChunk)
	loop := newCrashTestLoop(t, st, llm.server.URL)

	peerID := int64(91002)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		loop.ProcessPrompt(ctx, "задача на весь вечер", peerID)
	}()

	waitForStoreMessages(t, st, peerID, 4)
	cancel()
	<-done

	sess := loop.GetSession(peerID)
	if sess == nil {
		t.Fatal("expected session")
	}
	if sess.GetResumePrompt() != "задача на весь вечер" {
		t.Fatalf("resume prompt lost on cancelled turn: %q", sess.GetResumePrompt())
	}

	llm.release()
	if _, err := loop.ProcessPrompt(context.Background(), "продолжай", peerID); err != nil {
		t.Fatalf("resumed turn failed: %v", err)
	}
	if got := loop.GetSession(peerID).GetResumePrompt(); got != "" {
		t.Fatalf("resume prompt not cleared after successful turn: %q", got)
	}
}
