package agentloop

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

type fakeCompactor struct {
	mu    sync.Mutex
	calls int
	delay time.Duration
}

func (f *fakeCompactor) CompactWithOpenCode(ctx context.Context, messages []tokenizers.Message, maxTokens int, tailTurns int, preserveRecentTokens *int) (*compress.OpenCodeCompactResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	tailStartID := len(messages) - 2
	if tailStartID < 0 {
		tailStartID = 0
	}
	return &compress.OpenCodeCompactResult{
		Summary:     "fake-summary",
		TailStartID: tailStartID,
	}, nil
}

func (f *fakeCompactor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func specTestLoop(t *testing.T, ratio float64) *agentLoop {
	t.Helper()
	config := DefaultLoopConfig()
	config.ModelHolder = modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "test",
		Models:  map[string]modelsconfig.ModelEntry{"test": {Name: "test-model", Host: "http://127.0.0.1:1"}},
	})
	config.MaxTokens = 1000
	config.ModelLimitInput = 800
	config.SpeculativeCompactRatio = ratio
	loop, err := NewAgentLoop(config, nil, nil)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	al := loop.(*agentLoop)
	al.compactor = &fakeCompactor{}
	return al
}

func fillSessionToTokens(t *testing.T, s *session.Session, targetTokens int) {
	t.Helper()
	for {
		s.AddUserMessage("u" + specRepeat("x", 396))
		s.AddAssistantMessage("a" + specRepeat("y", 396))
		visible := compress.FilterCompacted(rawMessagesFromSession(s))
		if compress.EstimateMessagesTokensSimple(visible) >= targetTokens {
			return
		}
	}
}

func specRepeat(s string, n int) string {
	out := make([]byte, 0, n)
	for len(out) < n {
		out = append(out, s...)
	}
	return string(out[:n])
}

func rawMessagesFromSession(s *session.Session) []tokenizers.Message {
	history := s.GetHistory()
	messages := make([]tokenizers.Message, len(history))
	for i, msg := range history {
		content := msg.Content
		for _, tc := range msg.ToolCalls {
			content += tc.Function.Arguments
		}
		messages[i] = tokenizers.Message{
			Role:        string(msg.Role),
			Content:     content,
			Summary:     msg.Summary,
			Compacted:   msg.Compacted,
			TailStartID: msg.TailStartID,
		}
	}
	return messages
}

func TestSpeculativeCompactionTriggersAtRatio(t *testing.T) {
	al := specTestLoop(t, 0.5)
	peerID := int64(1)
	sess := al.EnsureSession(peerID)

	fillSessionToTokens(t, sess, 450)
	al.maybeStartSpeculativeCompact(context.Background(), sess, peerID)

	comp, _ := al.compactor.(*fakeCompactor)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && comp.callCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if comp.callCount() != 1 {
		t.Errorf("compactor calls = %d, want 1 (trigger at 50%% of usable)", comp.callCount())
	}
}

func TestSpeculativeCompactionNotBelowRatio(t *testing.T) {
	al := specTestLoop(t, 0.9)
	peerID := int64(1)
	sess := al.EnsureSession(peerID)

	fillSessionToTokens(t, sess, 200)
	al.maybeStartSpeculativeCompact(context.Background(), sess, peerID)

	comp, _ := al.compactor.(*fakeCompactor)
	if comp.callCount() != 0 {
		t.Errorf("compactor calls = %d, want 0 (below 90%% of usable)", comp.callCount())
	}
}

func TestSpeculativeCompactionDisabledWhenRatioZero(t *testing.T) {
	al := specTestLoop(t, 0)
	peerID := int64(1)
	sess := al.EnsureSession(peerID)

	fillSessionToTokens(t, sess, 900)
	al.maybeStartSpeculativeCompact(context.Background(), sess, peerID)

	comp, _ := al.compactor.(*fakeCompactor)
	if comp.callCount() != 0 {
		t.Errorf("compactor calls = %d, want 0 (ratio=0 disables speculation)", comp.callCount())
	}
}

func TestSpeculativeCompactionAppliedOnOverflow(t *testing.T) {
	al := specTestLoop(t, 0.5)
	peerID := int64(1)
	sess := al.EnsureSession(peerID)

	fillSessionToTokens(t, sess, 450)
	al.maybeStartSpeculativeCompact(context.Background(), sess, peerID)
	time.Sleep(50 * time.Millisecond)

	if !compress.IsOverflowWithLimits(900, al.config.MaxTokens, al.config.ModelLimitInput, al.config.CompactionReserved) {
		t.Fatal("test setup: expected overflow at 900 tokens")
	}
	applied := al.tryApplySpeculativeCompact(sess, peerID)
	if !applied {
		t.Fatal("expected speculative result to be applied on overflow")
	}

	hist := sess.GetHistory()
	foundSummary := false
	for _, m := range hist {
		if m.Summary && m.Content == "fake-summary" {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Error("expected compaction summary in session history")
	}
}

func TestSpeculativeCompactionDiscardedOnGrowth(t *testing.T) {
	al := specTestLoop(t, 0.5)
	peerID := int64(1)
	sess := al.EnsureSession(peerID)

	fillSessionToTokens(t, sess, 450)
	al.maybeStartSpeculativeCompact(context.Background(), sess, peerID)
	time.Sleep(50 * time.Millisecond)

	for i := 0; i < 20; i++ {
		sess.AddUserMessage("growth" + specRepeat("g", 396))
	}

	if applied := al.tryApplySpeculativeCompact(sess, peerID); applied {
		t.Error("expected speculative result to be discarded after history growth")
	}
}

func TestSpeculativeCompactionNotAppliedBeforeReady(t *testing.T) {
	al := specTestLoop(t, 0.5)
	peerID := int64(1)
	sess := al.EnsureSession(peerID)

	al.compactor = &fakeCompactor{delay: 5 * time.Second}
	fillSessionToTokens(t, sess, 450)
	al.maybeStartSpeculativeCompact(context.Background(), sess, peerID)

	if applied := al.tryApplySpeculativeCompact(sess, peerID); applied {
		t.Error("expected not to apply while compaction is still in flight")
	}
}