package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	sess "github.com/Grigory-Rylov/ai-agent-reflection/session"
)


func TestCompactIfNeeded_AddAutoContinue_ProcessMessagePath(t *testing.T) {
	agent := newAutoContinueTestAgent(t)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test system prompt")

	
	for i := 0; i < 10; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("user message %d: ", i), 20))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("assistant reply %d: ", i), 20))
	}

	ctx := context.Background()

	
	result := agent.compactIfNeeded(ctx, s, true)

	if !result {
		t.Fatal("expected compaction to succeed")
	}

	
	history := s.GetHistory()
	found := false
	for _, msg := range history {
		if msg.Role == sess.UserRole && msg.Content == tokenizers.CompactionAutoContinueText {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected user message with CompactionAutoContinueText after compaction in ProcessMessage path")
		for i, msg := range history {
			t.Logf("  [%d] role=%s content=%q (summary=%v compacted=%v)", i, msg.Role, msg.Content, msg.Summary, msg.Compacted)
		}
	}
}


func TestCompactIfNeeded_NoAutoContinue_ToolLoopPath(t *testing.T) {
	agent := newAutoContinueTestAgent(t)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test system prompt")

	
	for i := 0; i < 10; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("user message %d: ", i), 20))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("assistant reply %d: ", i), 20))
	}

	ctx := context.Background()

	
	result := agent.compactIfNeeded(ctx, s, false)

	if !result {
		t.Fatal("expected compaction to succeed")
	}

	
	history := s.GetHistory()
	for _, msg := range history {
		if msg.Role == sess.UserRole && msg.Content == tokenizers.CompactionAutoContinueText {
			t.Error("CompactionAutoContinueText should NOT appear when addAutoContinue=false")
		}
	}
}


func TestCompactIfNeeded_AutoContinue_AllowsSecondAfterAssistantResponse(t *testing.T) {
	agent := newAutoContinueTestAgent(t)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test system prompt")

	
	for i := 0; i < 10; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("user message %d: ", i), 20))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("assistant reply %d: ", i), 20))
	}

	ctx := context.Background()

	
	result1 := agent.compactIfNeeded(ctx, s, true)
	if !result1 {
		t.Fatal("expected first compaction to succeed")
	}

	countAfterFirst := countUserMessages(s.GetHistory(), tokenizers.CompactionAutoContinueText)
	if countAfterFirst != 1 {
		t.Errorf("expected exactly 1 CompactionAutoContinueText after first compaction, got %d", countAfterFirst)
	}

	
	for i := 0; i < 20; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("more user message %d: ", i), 30))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("more assistant reply %d: ", i), 30))
	}

	
	
	
	
	result2 := agent.compactIfNeeded(ctx, s, true)
	if !result2 {
		t.Fatal("expected second compaction to succeed")
	}

	
	countAfterSecond := countUserMessages(s.GetHistory(), tokenizers.CompactionAutoContinueText)
	if countAfterSecond != 2 {
		t.Errorf("expected exactly 2 CompactionAutoContinueText (model responded between compactions), got %d", countAfterSecond)
	}
}


func TestCompactIfNeeded_AutoContinue_GuardNoAssistantResponse(t *testing.T) {
	agent := newAutoContinueTestAgent(t)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test system prompt")

	
	for i := 0; i < 10; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("user message %d: ", i), 20))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("assistant reply %d: ", i), 20))
	}

	ctx := context.Background()

	
	result1 := agent.compactIfNeeded(ctx, s, true)
	if !result1 {
		t.Fatal("expected first compaction to succeed")
	}

	countAfterFirst := countUserMessages(s.GetHistory(), tokenizers.CompactionAutoContinueText)
	if countAfterFirst != 1 {
		t.Errorf("expected exactly 1 CompactionAutoContinueText after first compaction, got %d", countAfterFirst)
	}

	
	
	for i := 0; i < 20; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("only user message %d: ", i), 30))
	}

	result2 := agent.compactIfNeeded(ctx, s, true)
	if !result2 {
		t.Fatal("expected second compaction to succeed")
	}

	
	countAfterSecond := countUserMessages(s.GetHistory(), tokenizers.CompactionAutoContinueText)
	if countAfterSecond != 1 {
		t.Errorf("expected exactly 1 CompactionAutoContinueText (no assistant response), got %d", countAfterSecond)
	}
}


func TestCompactIfNeeded_NoOverflow_NoAutoContinue(t *testing.T) {
	agent := newAutoContinueTestAgent(t)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test")

	
	s.AddUserMessage("hello")

	ctx := context.Background()

	result := agent.compactIfNeeded(ctx, s, true)

	if result {
		t.Error("expected no compaction when there is no overflow")
	}

	
	history := s.GetHistory()
	for _, msg := range history {
		if msg.Role == sess.UserRole && msg.Content == tokenizers.CompactionAutoContinueText {
			t.Error("CompactionAutoContinueText should NOT appear when there is no overflow")
		}
	}
}
