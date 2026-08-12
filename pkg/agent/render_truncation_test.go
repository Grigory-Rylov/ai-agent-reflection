package agent

import (
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/compress"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

// TestConvertHistoryToAPIMessages_TruncatesLargeToolOutput (п.11) проверяет,
// что при рендере истории в API-сообщения большой tool-вывод обрезается до
// TOOL_OUTPUT_MAX_CHARS (opencode: toModelMessagesEffect с toolOutputMaxChars),
// а короткие tool-выводы и не-tool сообщения не трогаются.
func TestConvertHistoryToAPIMessages_TruncatesLargeToolOutput(t *testing.T) {
	large := strings.Repeat("DATA", 5000)
	short := "ok"

	s := session.NewSession(session.DefaultConfig())
	s.UpdateSystemPrompt("system")
	s.AddUserMessage("user")
	s.AddAssistantMessage("assistant")
	s.AddToolMessage("call-1", "read_file", large)
	s.AddToolMessage("call-2", "read_file", short)

	api := (&agentImpl{}).convertHistoryToAPIMessages(s.GetHistory())

	var gotLarge, gotShort string
	for _, m := range api {
		switch m.ToolCallID {
		case "call-1":
			gotLarge = m.Content
		case "call-2":
			gotShort = m.Content
		}
	}

	if len(gotLarge) >= len(large) {
		t.Errorf("expected large tool output to be truncated, got %d chars (original %d)", len(gotLarge), len(large))
	}
	if !strings.HasSuffix(gotLarge, "[truncated]") {
		t.Errorf("expected truncated suffix, got %q", gotLarge)
	}
	if len(gotLarge) > compress.TOOL_OUTPUT_MAX_CHARS+100 {
		t.Errorf("truncated content too large: %d chars", len(gotLarge))
	}
	if gotShort != short {
		t.Errorf("short tool output should be unchanged, got %q", gotShort)
	}
}
