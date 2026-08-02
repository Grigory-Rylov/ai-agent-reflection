package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opencode/llama-client/pkg/compress"
	"github.com/opencode/llama-client/pkg/tokenizers"
	"github.com/opencode/llama-client/session"
)

// mockPinnedCompressor — стаб LLM-компрессора для компакции.
type mockPinnedCompressor struct {
	compressFunc func(ctx context.Context, req *compress.CompressionRequest) (*compress.CompressionResult, error)
}

func (m *mockPinnedCompressor) Compress(ctx context.Context, req *compress.CompressionRequest) (*compress.CompressionResult, error) {
	if m.compressFunc != nil {
		return m.compressFunc(ctx, req)
	}
	return &compress.CompressionResult{
		OriginalTokens:   100,
		CompressedTokens: 50,
		CompressionRatio: 0.5,
		CompressedMessages: []tokenizers.Message{
			{Role: "assistant", Content: "[SUMMARY] old conversation"},
		},
		Summary:      "[SUMMARY] old conversation",
		CompressedAt: time.Now(),
	}, nil
}

// newPinnedTestAgent создаёт агента с подставленным компактором.
func newPinnedTestAgent(t *testing.T) *agentImpl {
	t.Helper()
	config := DefaultConfig()
	config.LlamaServerURL = "127.0.0.1:8080"
	config.Model = "test-model"
	config.MaxTokens = 1000 // маленький лимит — компактизация сработает

	agent := NewAgent(config)
	agent.compactor = compress.NewCompactor(&mockPinnedCompressor{})
	return agent
}

// TestPinnedPromptsSurviveCompaction проверяет, что pinned промпты остаются
// в начале контекста после компактизации.
func TestPinnedPromptsSurviveCompaction(t *testing.T) {
	agent := newPinnedTestAgent(t)

	s := session.NewSession(session.DefaultConfig())
	s.AddPinned("Pin that must survive")

	for i := 0; i < 30; i++ {
		s.AddUserMessage(strings.Repeat("x", 500))
		s.AddAssistantMessage(strings.Repeat("y", 500))
	}

	ctx := context.Background()
	agent.compactIfNeeded(ctx, s)

	pinned := s.GetPinned()
	if len(pinned) != 1 || pinned[0] != "Pin that must survive" {
		t.Errorf("pinned prompts should survive compaction, got %v", pinned)
	}

	msgs := s.GetContextMessages()
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 context messages, got %d", len(msgs))
	}
	if msgs[1].Role != session.UserRole || msgs[1].Content != "Pin that must survive" {
		t.Errorf("expected pinned prompt at start of context, got role=%s content=%q", msgs[1].Role, msgs[1].Content)
	}
}

// TestPinnedPromptsInContextAfterCompaction проверяет, что после компактизации
// pinned промпты попадают в API-сообщения в начале контекста.
func TestPinnedPromptsInContextAfterCompaction(t *testing.T) {
	agent := newPinnedTestAgent(t)

	s := session.NewSession(session.DefaultConfig())
	s.AddPinned("Rule one")
	s.AddPinned("Rule two")

	for i := 0; i < 30; i++ {
		s.AddUserMessage(strings.Repeat("x", 500))
		s.AddAssistantMessage(strings.Repeat("y", 500))
	}

	ctx := context.Background()
	agent.compactIfNeeded(ctx, s)

	apiMessages := agent.convertHistoryToAPIMessages(s.GetContextMessages())

	if len(apiMessages) < 3 {
		t.Fatalf("expected at least 3 API messages, got %d", len(apiMessages))
	}
	if apiMessages[0].Role != "system" {
		t.Errorf("first API message should be system, got %s", apiMessages[0].Role)
	}
	if apiMessages[1].Role != "user" || apiMessages[1].Content != "Rule one" {
		t.Errorf("expected pinned 'Rule one' at index 1, got %v", apiMessages[1])
	}
	if apiMessages[2].Role != "user" || apiMessages[2].Content != "Rule two" {
		t.Errorf("expected pinned 'Rule two' at index 2, got %v", apiMessages[2])
	}
}
