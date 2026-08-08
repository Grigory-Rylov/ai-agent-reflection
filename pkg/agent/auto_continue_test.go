package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/opencode/llama-client/pkg/tokenizers"
	sess "github.com/opencode/llama-client/session"
)

// ============================================================
// Test: compactIfNeeded adds auto-continue when called from ProcessMessage path
// ============================================================

func TestCompactIfNeeded_AddAutoContinue_ProcessMessagePath(t *testing.T) {
	agent := newAutoContinueTestAgent(t)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test system prompt")

	// Наполняем сессию сообщениями, чтобы компактизация сработала.
	for i := 0; i < 10; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("user message %d: ", i), 20))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("assistant reply %d: ", i), 20))
	}

	ctx := context.Background()

	// Вызываем compactIfNeeded с addAutoContinue=true (ProcessMessage path)
	result := agent.compactIfNeeded(ctx, s, true)

	if !result {
		t.Fatal("expected compaction to succeed")
	}

	// Проверяем, что в сессии появилось CompactionAutoContinueText.
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

// ============================================================
// Test: compactIfNeeded does NOT add auto-continue when called from tool loop path
// ============================================================

func TestCompactIfNeeded_NoAutoContinue_ToolLoopPath(t *testing.T) {
	agent := newAutoContinueTestAgent(t)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test system prompt")

	// Наполняем сессию сообщениями.
	for i := 0; i < 10; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("user message %d: ", i), 20))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("assistant reply %d: ", i), 20))
	}

	ctx := context.Background()

	// Вызываем compactIfNeeded с addAutoContinue=false (tool loop / proactive path)
	result := agent.compactIfNeeded(ctx, s, false)

	if !result {
		t.Fatal("expected compaction to succeed")
	}

	// Проверяем, что CompactionAutoContinueText НЕ появился.
	history := s.GetHistory()
	for _, msg := range history {
		if msg.Role == sess.UserRole && msg.Content == tokenizers.CompactionAutoContinueText {
			t.Error("CompactionAutoContinueText should NOT appear when addAutoContinue=false")
		}
	}
}

// ============================================================
// Test: compactIfNeeded allows second auto-continue after assistant response
// When assistant messages were added after the first auto-continue (model responded/continued),
// a second compaction should correctly add another auto-continue.
// ============================================================

func TestCompactIfNeeded_AutoContinue_AllowsSecondAfterAssistantResponse(t *testing.T) {
	agent := newAutoContinueTestAgent(t)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test system prompt")

	// Наполняем сессию для первой компактизации.
	for i := 0; i < 10; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("user message %d: ", i), 20))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("assistant reply %d: ", i), 20))
	}

	ctx := context.Background()

	// Первая компактизация — добавляет auto-continue.
	result1 := agent.compactIfNeeded(ctx, s, true)
	if !result1 {
		t.Fatal("expected first compaction to succeed")
	}

	countAfterFirst := countUserMessages(s.GetHistory(), tokenizers.CompactionAutoContinueText)
	if countAfterFirst != 1 {
		t.Errorf("expected exactly 1 CompactionAutoContinueText after first compaction, got %d", countAfterFirst)
	}

	// Наполняем сессию ещё сообщениями для второй компактизации.
	for i := 0; i < 20; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("more user message %d: ", i), 30))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("more assistant reply %d: ", i), 30))
	}

	// Вторая компактизация — shouldAddAutoContinue вернёт true, т.к. между
	// первой auto-continue и текущим моментом есть assistant ответы (модель
	// продолжила работу). Это корректное поведение: модель ответила на первый
	// auto-continue, значит можно добавить новый.
	result2 := agent.compactIfNeeded(ctx, s, true)
	if !result2 {
		t.Fatal("expected second compaction to succeed")
	}

	// Ожидаем 2 CompactionAutoContinueText (модель продолжила работу между компактизациями).
	countAfterSecond := countUserMessages(s.GetHistory(), tokenizers.CompactionAutoContinueText)
	if countAfterSecond != 2 {
		t.Errorf("expected exactly 2 CompactionAutoContinueText (model responded between compactions), got %d", countAfterSecond)
	}
}

// ============================================================
// Test: compactIfNeeded guard — NO assistant response → no duplicate
// If we manually add user messages after auto-continue but NO assistant response,
// the guard should prevent adding a second auto-continue.
// ============================================================

func TestCompactIfNeeded_AutoContinue_GuardNoAssistantResponse(t *testing.T) {
	agent := newAutoContinueTestAgent(t)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test system prompt")

	// Наполняем сессию для первой компактизации.
	for i := 0; i < 10; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("user message %d: ", i), 20))
		s.AddAssistantMessage(strings.Repeat(fmt.Sprintf("assistant reply %d: ", i), 20))
	}

	ctx := context.Background()

	// Первая компактизация — добавляет auto-continue.
	result1 := agent.compactIfNeeded(ctx, s, true)
	if !result1 {
		t.Fatal("expected first compaction to succeed")
	}

	countAfterFirst := countUserMessages(s.GetHistory(), tokenizers.CompactionAutoContinueText)
	if countAfterFirst != 1 {
		t.Errorf("expected exactly 1 CompactionAutoContinueText after first compaction, got %d", countAfterFirst)
	}

	// Добавляем ТОЛЬКО user-сообщения (без assistant ответов).
	// shouldAddAutoContinue должен вернуть false: модель не ответила на auto-continue.
	for i := 0; i < 20; i++ {
		s.AddUserMessage(strings.Repeat(fmt.Sprintf("only user message %d: ", i), 30))
	}

	result2 := agent.compactIfNeeded(ctx, s, true)
	if !result2 {
		t.Fatal("expected second compaction to succeed")
	}

	// Ожидаем всё ещё 1 CompactionAutoContinueText (дублирования не должно быть).
	countAfterSecond := countUserMessages(s.GetHistory(), tokenizers.CompactionAutoContinueText)
	if countAfterSecond != 1 {
		t.Errorf("expected exactly 1 CompactionAutoContinueText (no assistant response), got %d", countAfterSecond)
	}
}

// ============================================================
// Test: compactIfNeeded no overflow — no auto-continue added even with addAutoContinue=true
// ============================================================

func TestCompactIfNeeded_NoOverflow_NoAutoContinue(t *testing.T) {
	agent := newAutoContinueTestAgent(t)

	s := sess.NewSession(sess.DefaultConfig())
	s.UpdateSystemPrompt("test")

	// Маленький контекст — не переполнит.
	s.AddUserMessage("hello")

	ctx := context.Background()

	result := agent.compactIfNeeded(ctx, s, true)

	if result {
		t.Error("expected no compaction when there is no overflow")
	}

	// Не должно быть auto-continue.
	history := s.GetHistory()
	for _, msg := range history {
		if msg.Role == sess.UserRole && msg.Content == tokenizers.CompactionAutoContinueText {
			t.Error("CompactionAutoContinueText should NOT appear when there is no overflow")
		}
	}
}
