package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/store"
)

func TestAddAssistantMessageInternalSkipsLoopDetection(t *testing.T) {
	config := DefaultConfig()
	config.MaxLoopHistory = 3
	s := NewSession(config)

	for i := 0; i < 3; i++ {
		s.AddAssistantMessageInternal("[REASONING SUMMARY]\n- decided the same thing again")
	}

	if s.IsLoopDetected() {
		t.Errorf("internal messages must not trigger loop detection")
	}
	if got := s.GetLoopCount(); got != 0 {
		t.Errorf("loop count = %d, want 0", got)
	}
}

func TestGetLastAssistantMessageSkipsInternal(t *testing.T) {
	s := NewSession(DefaultConfig())
	s.AddUserMessage("question")
	s.AddAssistantMessage("visible answer")
	s.AddAssistantMessageInternal("[REASONING SUMMARY]\n- decided X")

	last := s.GetLastAssistantMessage()
	if last == nil {
		t.Fatalf("expected last assistant message, got nil")
	}
	if last.Content != "visible answer" {
		t.Errorf("last assistant content = %q, want %q", last.Content, "visible answer")
	}
	if last.Internal {
		t.Errorf("returned message must not be internal")
	}
}

func TestGetLastAssistantMessageOnlyInternalReturnsNil(t *testing.T) {
	s := NewSession(DefaultConfig())
	s.AddUserMessage("question")
	s.AddAssistantMessageInternal("[REASONING SUMMARY]\n- decided X")

	if last := s.GetLastAssistantMessage(); last != nil {
		t.Errorf("expected nil, got %+v", last)
	}
}

func TestInternalFlagRoundTripJSONFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "session.json")

	config := DefaultConfig()
	config.PeerID = 777001
	config.SessionFile = file
	config.SystemPrompt = ""
	s := NewSession(config)
	s.AddUserMessage("question")
	s.AddAssistantMessage("visible answer")
	s.AddAssistantMessageInternal("[REASONING SUMMARY]\n- decided X")

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	if !strings.Contains(string(data), `"internal": true`) {
		t.Errorf("expected internal flag in session json, got %s", string(data))
	}

	reloaded := NewSession(config)
	hist := reloaded.GetHistory()
	if len(hist) != 3 {
		t.Fatalf("history length = %d, want 3", len(hist))
	}
	if !hist[2].Internal {
		t.Errorf("internal flag lost after reload: %+v", hist[2])
	}
	if hist[1].Internal {
		t.Errorf("normal assistant message became internal: %+v", hist[1])
	}
	if last := reloaded.GetLastAssistantMessage(); last == nil || last.Content != "visible answer" {
		t.Errorf("last assistant after reload = %+v, want visible answer", last)
	}
}

func TestInternalFlagRoundTripStore(t *testing.T) {
	dir := t.TempDir()
	st, err := store.NewStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()

	config := DefaultConfig()
	config.PeerID = 777002
	config.Store = st
	config.SystemPrompt = ""
	s := NewSession(config)
	s.AddUserMessage("question")
	s.AddAssistantMessage("visible answer")
	s.AddAssistantMessageInternal("[REASONING SUMMARY]\n- decided X")

	reloaded := NewSession(config)
	hist := reloaded.GetHistory()
	if len(hist) != 3 {
		t.Fatalf("history length = %d, want 3", len(hist))
	}
	if !hist[2].Internal {
		t.Errorf("internal flag lost after store reload: %+v", hist[2])
	}
	if hist[1].Internal {
		t.Errorf("normal assistant message became internal: %+v", hist[1])
	}
	if last := reloaded.GetLastAssistantMessage(); last == nil || last.Content != "visible answer" {
		t.Errorf("last assistant after reload = %+v, want visible answer", last)
	}
}
