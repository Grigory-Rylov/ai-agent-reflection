package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)


func bigUserMsg(i int) string {
	return fmt.Sprintf("%d-%s", i, strings.Repeat("u", 490))
}

func bigAssistantMsg(i int) string {
	return fmt.Sprintf("%d-%s", i, strings.Repeat("a", 490))
}


func findLastSummaryIndex(t *testing.T, hist []session.Message) int {
	t.Helper()
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Role == session.AssistantRole && hist[i].Summary {
			return i
		}
	}
	return -1
}


func assertNoOldHeadInContext(t *testing.T, visible []session.Message, headTurns int) {
	t.Helper()
	for i := 0; i < headTurns; i++ {
		prefix := fmt.Sprintf("%d-", i)
		for _, m := range visible {
			if strings.HasPrefix(m.Content, prefix) {
				t.Fatalf("old head message %q leaked into visible context after repeated compaction", m.Content)
			}
		}
	}
}


func assertTailInOrder(t *testing.T, hist []session.Message, tsid int, visible []session.Message) {
	t.Helper()
	var tail []session.Message
	for _, m := range hist[tsid:] {
		if m.Summary || (m.Role == session.UserRole && m.Content == tokenizers.CompactionUserMessage) {
			continue
		}
		tail = append(tail, m)
	}
	if len(tail) > len(visible) {
		t.Fatalf("tail (%d) longer than visible context (%d)", len(tail), len(visible))
	}
	suffix := visible[len(visible)-len(tail):]
	for i := range tail {
		if suffix[i].Content != tail[i].Content || suffix[i].Role != tail[i].Role {
			t.Fatalf("tail mismatch at %d: visible=%q(%s) raw=%q(%s)",
				i, suffix[i].Content, suffix[i].Role, tail[i].Content, tail[i].Role)
		}
	}
}


func TestRepeatedCompaction_TailStartIDAlignment(t *testing.T) {
	agent := newPinnedTestAgent(t)
	s := session.NewSession(session.DefaultConfig())

	
	for i := 0; i < 8; i++ {
		s.AddUserMessage(bigUserMsg(i))
		s.AddAssistantMessage(bigAssistantMsg(i))
	}

	ctx := context.Background()
	agent.compactIfNeeded(ctx, s, false)

	histAfterFirst := s.GetHistory()
	firstMarker := findLastSummaryIndex(t, histAfterFirst)
	if firstMarker < 0 {
		t.Fatal("expected compaction marker after first compactIfNeeded")
	}
	if firstTailStart := histAfterFirst[firstMarker].TailStartID; firstTailStart <= 0 {
		t.Fatalf("expected tail_start_id > 0 after first compaction, got %d", firstTailStart)
	}

	
	for i := 8; i < 14; i++ {
		s.AddUserMessage(bigUserMsg(i))
		s.AddAssistantMessage(bigAssistantMsg(i))
	}

	
	agent.compactIfNeeded(ctx, s, false)

	hist := s.GetHistory()

	
	if len(hist) <= len(histAfterFirst) {
		t.Errorf("history should grow across compactions (no Reset), %d -> %d", len(histAfterFirst), len(hist))
	}

	lastSummaryIdx := findLastSummaryIndex(t, hist)
	if lastSummaryIdx < 0 {
		t.Fatal("expected a new compaction marker after second compactIfNeeded")
	}
	tsid := hist[lastSummaryIdx].TailStartID
	if tsid <= 0 {
		t.Fatalf("expected tail_start_id > 0 after second compaction, got %d", tsid)
	}
	if hist[tsid].Role != session.UserRole {
		t.Errorf("expected tail to start at a user message, got role=%s at index %d (tail_start_id=%d)",
			hist[tsid].Role, tsid, tsid)
	}

	visible := s.GetContextMessages()
	if len(visible) < 3 {
		t.Fatalf("expected at least [system, marker, summary], got %d messages", len(visible))
	}
	if visible[0].Role != session.SystemRole {
		t.Errorf("first context message should be system, got %s", visible[0].Role)
	}
	if visible[1].Role != session.UserRole || visible[1].Content != tokenizers.CompactionUserMessage {
		t.Errorf("second context message should be the compaction marker, got %q", visible[1].Content)
	}

	
	assertNoOldHeadInContext(t, visible, 8)
	assertTailInOrder(t, hist, tsid, visible)
}


func TestRepeatedCompaction_RawIndexAlignment(t *testing.T) {
	agent := newPinnedTestAgent(t)
	s := session.NewSession(session.DefaultConfig())

	for i := 0; i < 8; i++ {
		s.AddUserMessage(bigUserMsg(i))
		s.AddAssistantMessage(bigAssistantMsg(i))
	}
	agent.compactIfNeeded(context.Background(), s, false)
	for i := 8; i < 14; i++ {
		s.AddUserMessage(bigUserMsg(i))
		s.AddAssistantMessage(bigAssistantMsg(i))
	}
	agent.compactIfNeeded(context.Background(), s, false)

	history := s.GetHistory()
	raw := agent.convertSessionHistoryRaw(history)
	if len(raw) != len(history) {
		t.Fatalf("raw conversion length mismatch: %d != %d", len(raw), len(history))
	}

	marker := findLastSummaryIndex(t, history)
	tsid := history[marker].TailStartID
	if tsid <= 0 || tsid >= len(history) {
		t.Fatalf("TailStartID %d out of raw history bounds [1, %d)", tsid, len(history))
	}
	if raw[tsid].Role != "user" {
		t.Errorf("expected tail to start at user message, got %q at index %d", raw[tsid].Role, tsid)
	}
}
