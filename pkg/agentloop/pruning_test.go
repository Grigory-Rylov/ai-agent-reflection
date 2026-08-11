package agentloop

import (
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

func pruneTestUser() string { return "u" + strings.Repeat("x", 200) }
func pruneTestAssistant() string { return "a" + strings.Repeat("y", 200) }
func pruneTestHugeOutput() string { return strings.Repeat("TOOLOUTPUT", 30000) }
func pruneTestCallID(i int) string { return "call-" + string(rune('a'+i)) }

// TestRunPruning_PreservesCompactedHead проверяет, что runPruning не затирает
// контент сообщений, уже помеченных compacted предыдущей компактизацией
// (исходный head нужен для будущих summaries). Регрессия: раньше apply-цикл
// применял placeholder ко всем pruned[i].Compacted, включая уже compacted.
func TestRunPruning_PreservesCompactedHead(t *testing.T) {
	config := DefaultLoopConfig()
	config.EnablePruning = true
	al := &agentLoop{config: config}

	s := session.NewSession(session.DefaultConfig())
	for i := 0; i < 8; i++ {
		s.AddUserMessage(pruneTestUser())
		s.AddAssistantMessage(pruneTestAssistant())
	}
	// head (индексы 1..12) помечается compacted, tail сохраняется
	s.MarkCompaction(13, "summary-old")
	for i := 0; i < 6; i++ {
		s.AddUserMessage(pruneTestUser())
		s.AddAssistantMessage(pruneTestAssistant())
		s.AddToolMessage(pruneTestCallID(i), "read_file", pruneTestHugeOutput())
	}

	al.runPruning(s)

	hist := s.GetHistory()
	if hist[1].Content == compress.PRUNED_OUTPUT_PLACEHOLDER {
		t.Fatalf("compacted head message content overwritten with prune placeholder: %q", hist[1].Content)
	}
	// head по-прежнему помечен как compacted
	if !hist[1].Compacted {
		t.Errorf("expected head message to stay compacted")
	}
}

// TestRunPruning_PrunesOnlyNewToolOutputs проверяет, что runPruning обрезает
// только новые (не compacted) большие tool-выводы, заменяя их на placeholder.
func TestRunPruning_PrunesOnlyNewToolOutputs(t *testing.T) {
	config := DefaultLoopConfig()
	config.EnablePruning = true
	al := &agentLoop{config: config}

	s := session.NewSession(session.DefaultConfig())
	for i := 0; i < 6; i++ {
		s.AddUserMessage(pruneTestUser())
		s.AddAssistantMessage(pruneTestAssistant())
		s.AddToolMessage(pruneTestCallID(i), "read_file", pruneTestHugeOutput())
	}

	al.runPruning(s)

	hist := s.GetHistory()
	prunedAny := false
	for i := 3; i < len(hist); i++ { // пропускаем system + первый user/assistant
		if hist[i].Role == session.ToolRole && hist[i].Content == compress.PRUNED_OUTPUT_PLACEHOLDER {
			prunedAny = true
			break
		}
	}
	if !prunedAny {
		t.Fatal("expected pruning to replace a large tool output with placeholder")
	}
	// Последние 2 оборота защищены — их tool-выводы остаются нетронутыми.
	lastTool := hist[len(hist)-1]
	if lastTool.Content == compress.PRUNED_OUTPUT_PLACEHOLDER {
		t.Error("expected last (protected) tool output to stay intact")
	}
}
