package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNewStore(t *testing.T) {
	s := newTestStore(t)
	if s == nil {
		t.Fatal("store should not be nil")
	}
}

func TestSessionCRUD(t *testing.T) {
	s := newTestStore(t)

	sd, err := s.GetSession(1)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sd != nil {
		t.Error("expected nil for non-existent session")
	}

	now := time.Now()
	sd = &SessionData{
		PeerID:     1,
		CreatedAt:  now,
		UpdatedAt:  now,
		WorkingDir: "/tmp",
		LoopCount:  2,
		IsLooped:   true,
		LastLooped: "loop detected",
	}

	if err := s.SaveSession(sd); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	loaded, err := s.GetSession(1)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected session after save")
	}
	if loaded.PeerID != 1 {
		t.Errorf("expected peerID 1, got %d", loaded.PeerID)
	}
	if loaded.WorkingDir != "/tmp" {
		t.Errorf("expected /tmp, got %s", loaded.WorkingDir)
	}
	if !loaded.IsLooped {
		t.Error("expected IsLooped=true")
	}
	if loaded.LoopCount != 2 {
		t.Errorf("expected loopCount 2, got %d", loaded.LoopCount)
	}
}

func TestSessionUpdate(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()
	sd := &SessionData{PeerID: 1, CreatedAt: now, UpdatedAt: now}
	s.SaveSession(sd)

	sd.WorkingDir = "/new"
	sd.UpdatedAt = time.Now()
	s.SaveSession(sd)

	loaded, _ := s.GetSession(1)
	if loaded.WorkingDir != "/new" {
		t.Errorf("expected /new, got %s", loaded.WorkingDir)
	}
}

func TestSessionClear(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()
	s.SaveSession(&SessionData{PeerID: 1, CreatedAt: now, UpdatedAt: now})
	s.AddMessage(1, MessageData{
		PeerID: 1, Role: "user", Content: "hi", Timestamp: "now",
	})

	if err := s.ClearSession(1); err != nil {
		t.Fatalf("ClearSession: %v", err)
	}

	sd, _ := s.GetSession(1)
	if sd != nil {
		t.Error("expected nil session after clear")
	}

	msgs, _ := s.GetMessages(1)
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

func TestMessageCRUD(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	s.SaveSession(&SessionData{PeerID: 1, CreatedAt: now, UpdatedAt: now})

	msgs := []MessageData{
		{PeerID: 1, Role: "user", Content: "hello", Timestamp: "t1"},
		{PeerID: 1, Role: "assistant", Content: "hi", ToolCalls: `[{"id":"1"}]`, Timestamp: "t2"},
		{PeerID: 1, Role: "tool", Content: "result", ToolCallID: "1", ToolName: "file_read", Timestamp: "t3"},
	}

	for _, m := range msgs {
		if err := s.AddMessage(1, m); err != nil {
			t.Fatalf("AddMessage: %v", err)
		}
	}

	loaded, err := s.GetMessages(1)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(loaded))
	}

	if loaded[0].Role != "user" || loaded[0].Content != "hello" {
		t.Errorf("unexpected first message")
	}
	if loaded[1].ToolCalls != `[{"id":"1"}]` {
		t.Errorf("unexpected tool_calls: %s", loaded[1].ToolCalls)
	}
	if loaded[2].ToolCallID != "1" || loaded[2].ToolName != "file_read" {
		t.Errorf("unexpected tool message")
	}
}

func TestClearMessages(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	s.SaveSession(&SessionData{PeerID: 1, CreatedAt: now, UpdatedAt: now})
	s.AddMessage(1, MessageData{PeerID: 1, Role: "user", Content: "x", Timestamp: "t"})

	if err := s.ClearMessages(1); err != nil {
		t.Fatalf("ClearMessages: %v", err)
	}

	msgs, _ := s.GetMessages(1)
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after clear")
	}
}

func TestTodoCRUD(t *testing.T) {
	s := newTestStore(t)

	sessionID := "peer_1"
	todos := []TodoItem{
		{ID: "1", SessionID: sessionID, Content: "Task 1", Status: "pending", Priority: "high"},
		{ID: "2", SessionID: sessionID, Content: "Task 2", Status: "completed"},
	}

	if err := s.UpdateTodos(sessionID, todos); err != nil {
		t.Fatalf("UpdateTodos: %v", err)
	}

	loaded, err := s.GetTodos(sessionID)
	if err != nil {
		t.Fatalf("GetTodos: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(loaded))
	}

	if loaded[0].Content != "Task 1" {
		t.Errorf("expected Task 1 at position 0")
	}
	if loaded[0].Position != 0 {
		t.Errorf("expected position 0, got %d", loaded[0].Position)
	}

	todos[0].Status = "in_progress"
	s.UpdateTodos(sessionID, todos)
	loaded, _ = s.GetTodos(sessionID)
	if loaded[0].Status != "in_progress" {
		t.Errorf("expected in_progress, got %s", loaded[0].Status)
	}
}

func TestPermissionCRUD(t *testing.T) {
	s := newTestStore(t)

	sessionID := "peer_1"
	p, err := s.GetPermission(sessionID, "file_write", "/tmp/file.txt")
	if err != nil {
		t.Fatalf("GetPermission: %v", err)
	}
	if p != nil {
		t.Error("expected nil for non-existent permission")
	}

	if err := s.SavePermission(sessionID, "file_write", "/tmp/file.txt", "allow"); err != nil {
		t.Fatalf("SavePermission: %v", err)
	}

	p, err = s.GetPermission(sessionID, "file_write", "/tmp/file.txt")
	if err != nil {
		t.Fatalf("GetPermission: %v", err)
	}
	if p == nil {
		t.Fatal("expected permission after save")
	}
	if p.Decision != "allow" {
		t.Errorf("expected allow, got %s", p.Decision)
	}

	s.SavePermission(sessionID, "file_write", "/tmp/file.txt", "deny")
	p, _ = s.GetPermission(sessionID, "file_write", "/tmp/file.txt")
	if p.Decision != "deny" {
		t.Errorf("expected deny after update, got %s", p.Decision)
	}
}

func TestClearPermissions(t *testing.T) {
	s := newTestStore(t)
	sessionID := "peer_1"

	s.SavePermission(sessionID, "tool1", "res1", "allow")
	s.SavePermission(sessionID, "tool2", "res2", "deny")

	if err := s.ClearPermissions(sessionID); err != nil {
		t.Fatalf("ClearPermissions: %v", err)
	}

	p1, _ := s.GetPermission(sessionID, "tool1", "res1")
	if p1 != nil {
		t.Error("expected nil after clear")
	}
}

func TestConcurrentSession(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()

	s1 := &SessionData{PeerID: 1, CreatedAt: now, UpdatedAt: now, WorkingDir: "/a"}
	s2 := &SessionData{PeerID: 2, CreatedAt: now, UpdatedAt: now, WorkingDir: "/b"}

	s.SaveSession(s1)
	s.SaveSession(s2)

	loaded1, _ := s.GetSession(1)
	loaded2, _ := s.GetSession(2)

	if loaded1.WorkingDir != "/a" {
		t.Errorf("peer 1: expected /a, got %s", loaded1.WorkingDir)
	}
	if loaded2.WorkingDir != "/b" {
		t.Errorf("peer 2: expected /b, got %s", loaded2.WorkingDir)
	}
}

func TestAgentSessionDelete(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()
	if err := s.SaveAgentSession(&AgentSessionData{
		ID: "sess-1", AgentName: "worker", PeerID: 1,
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveAgentSession: %v", err)
	}

	sd, err := s.GetAgentSession("sess-1")
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	if sd == nil {
		t.Fatal("expected session after save")
	}

	if err := s.DeleteAgentSession("sess-1"); err != nil {
		t.Fatalf("DeleteAgentSession: %v", err)
	}

	sd, err = s.GetAgentSession("sess-1")
	if err != nil {
		t.Fatalf("GetAgentSession after delete: %v", err)
	}
	if sd != nil {
		t.Error("expected nil session after delete")
	}
}

func TestAgentSessionMessagesRoundTrip(t *testing.T) {
	s := newTestStore(t)

	now := time.Now()
	sd := &AgentSessionData{
		ID: "sess-msg", AgentName: "reviewer", PeerID: 1,
		Status:     "active",
		LastPrompt: "review the code",
		Messages:   `[{"role":"user","content":"hello"}]`,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.SaveAgentSession(sd); err != nil {
		t.Fatalf("SaveAgentSession: %v", err)
	}

	loaded, err := s.GetAgentSession("sess-msg")
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected session after save")
	}
	if loaded.LastPrompt != "review the code" {
		t.Errorf("expected last_prompt %q, got %q", "review the code", loaded.LastPrompt)
	}
	if loaded.Messages != `[{"role":"user","content":"hello"}]` {
		t.Errorf("unexpected messages: %s", loaded.Messages)
	}
}

func TestDBFileCreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom.db")
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	s.Close()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("db file should exist")
	}
}
