package agentloop

import (
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

// TestBuildAPIMessages_TruncatesLargeToolOutput (п.11) проверяет, что
// buildAPIMessages (основной путь рендера главного агента) обрезает большой
// tool-вывод при построении сообщений для LLM.
func TestBuildAPIMessages_TruncatesLargeToolOutput(t *testing.T) {
	large := strings.Repeat("DATA", 5000)
	short := "ok"

	s := session.NewSession(session.DefaultConfig())
	s.UpdateSystemPrompt("system")
	s.AddUserMessage("user")
	s.AddAssistantMessage("assistant")
	s.AddToolMessage("call-1", "read_file", large)
	s.AddToolMessage("call-2", "read_file", short)

	api := (&agentLoop{}).buildAPIMessages(s)

	gotLarge := ""
	gotShort := ""
	for _, m := range api {
		if m.Content == short {
			gotShort = m.Content
		}
		if strings.HasSuffix(m.Content, "[truncated]") {
			gotLarge = m.Content
		}
	}

	if gotLarge == "" {
		t.Fatalf("expected a truncated tool message, none found")
	}
	if len(gotLarge) >= len(large) {
		t.Errorf("expected large tool output to be truncated, got %d chars (original %d)", len(gotLarge), len(large))
	}
	if len(gotLarge) > compress.TOOL_OUTPUT_MAX_CHARS+100 {
		t.Errorf("truncated content too large: %d chars", len(gotLarge))
	}
	if gotShort != short {
		t.Errorf("short tool output should be unchanged, got %q", gotShort)
	}
}
