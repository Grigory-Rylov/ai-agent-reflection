package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type checkpointRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *checkpointRecorder) Record(ltc string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, ltc)
}

func (r *checkpointRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

func newScriptedMultiRoundAgent(t *testing.T, scripts [][]string) *agentImpl {
	t.Helper()
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		chunks := []string{`{"choices":[{"delta":{"content":"fallback"},"finish_reason":"stop"}]}`}
		if len(scripts) > 0 {
			chunks = scripts[0]
			mu.Lock()
			scripts = scripts[1:]
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			fmt.Fprint(w, "data: "+c+"\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	config := DefaultConfig()
	config.LlamaServerURL = server.URL
	config.Model = "test-model"
	config.MaxTokens = 10000
	config.RetryDelay = 5 * time.Millisecond

	a := NewAgent(config)
	a.mu.Lock()
	a.sessions[77] = sess.NewSession(sess.DefaultConfig())
	a.mu.Unlock()
	return a
}

func scriptedToolCallDelta(idx int, id, name, args string) string {
	return fmt.Sprintf(
		"{\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":%d,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]},\"finish_reason\":null}]}",
		idx, id, name, args)
}

func scriptedToolCallRound(specs ...[2]string) []string {
	deltas := make([]string, 0, len(specs)*2+1)
	for i, spec := range specs {
		deltas = append(deltas, scriptedToolCallDelta(i, fmt.Sprintf("id_%d", i), spec[0], spec[1]))
	}
	deltas = append(deltas, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
	return deltas
}

func scriptedFinalRound(content string) []string {
	return []string{
		fmt.Sprintf("{\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}", content),
	}
}

func TestProcessToolResults_CheckpointFiredPerRound(t *testing.T) {
	tests := []struct {
		name       string
		scripts    [][]string
		setHook    bool
		wantCalls  []string
	}{
		{
			name:      "single tool round fires once",
			scripts:   [][]string{scriptedToolCallRound([2]string{"time_get", "{"}), scriptedFinalRound("finished")},
			setHook:   true,
			wantCalls: []string{"time_get"},
		},
		{
			name:      "two tool rounds fire twice",
			scripts: [][]string{
				scriptedToolCallRound([2]string{"time_get", "{}"}),
				scriptedToolCallRound([2]string{"calc", "{\"expression\":\"2+2\"}"}, [2]string{"file_read", "{\"path\":\"/etc/hostname\"}"}),
				scriptedFinalRound("done"),
			},
			setHook:   true,
			wantCalls: []string{"time_get", "calc,file_read"},
		},
		{
			name:      "nil checkpoint hook stays silent",
			scripts:   [][]string{scriptedToolCallRound([2]string{"time_get", "{}"}), scriptedFinalRound("done")},
			setHook:   false,
			wantCalls: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &checkpointRecorder{}
			a := newScriptedMultiRoundAgent(t, tt.scripts)
			if tt.setHook {
				var iface interface{} = a
				cs, ok := iface.(CheckpointSetter)
				if !ok {
					t.Fatal("*agentImpl does not satisfy CheckpointSetter")
				}
				cs.SetCheckpoint(rec.Record)
			}

			ctx := context.Background()
			result, err := a.processWithTools(ctx, []Message{{Role: "user", Content: "start"}}, a.getSession(77))
			if err != nil {
				t.Fatalf("processWithTools: %v", err)
			}
			if result.Response != "finished" && result.Response != "done" {
				t.Errorf("unexpected final response %q", result.Response)
			}

			got := rec.recorded()
			if len(got) != len(tt.wantCalls) {
				t.Fatalf("checkpoint fired %d times, want %d: %v", len(got), len(tt.wantCalls), got)
			}
			for i := range got {
				if got[i] != tt.wantCalls[i] {
					t.Errorf("checkpoint #%d = %q, want %q", i+1, got[i], tt.wantCalls[i])
				}
			}

			history := a.getSession(77).GetHistory()
			hasPlainAssistant := false
			for _, m := range history {
				if m.Role == sess.AssistantRole && len(m.ToolCalls) == 0 && m.Content != "" {
					hasPlainAssistant = true
				}
			}
			if !hasPlainAssistant {
				t.Error("expected terminal plain assistant message in session history")
			}
		})
	}
}
