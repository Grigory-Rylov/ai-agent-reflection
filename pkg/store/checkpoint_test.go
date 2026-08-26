package store

import (
	"testing"
	"time"
)

func seedCheckpointSession(t *testing.T, s Store, id, lastPrompt, messages string) {
	t.Helper()
	now := time.Now()
	if err := s.SaveAgentSession(&AgentSessionData{
		ID:           id,
		AgentName:    "worker",
		PeerID:       1,
		SystemPrompt: "sp",
		LastPrompt:   lastPrompt,
		Status:       "active",
		CreatedAt:    now,
		UpdatedAt:    now,
		Messages:     messages,
	}); err != nil {
		t.Fatalf("seed SaveAgentSession: %v", err)
	}
}

func assertCheckpointFields(t *testing.T, s Store, id, wantLTC, wantMessages, wantLastPrompt string) {
	t.Helper()
	sd, err := s.GetAgentSession(id)
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	if sd == nil {
		t.Fatalf("expected session %s to exist", id)
	}
	if sd.LastToolCall != wantLTC {
		t.Errorf("LastToolCall = %q, want %q", sd.LastToolCall, wantLTC)
	}
	if sd.Messages != wantMessages {
		t.Errorf("Messages = %q, want %q", sd.Messages, wantMessages)
	}
	if sd.LastPrompt != wantLastPrompt {
		t.Errorf("LastPrompt = %q, want %q", sd.LastPrompt, wantLastPrompt)
	}
}

func TestSaveAgentCheckpoint(t *testing.T) {
	tests := []struct {
		name         string
		id           string
		existing     bool
		lastPrompt   string
		checkpoints  [][2]string
		wantLTC      string
		wantMessages string
	}{
		{
			name:         "overwrites last tool call and messages",
			id:           "ckpt-a",
			existing:     true,
			lastPrompt:   "orig-task",
			checkpoints:  [][2]string{{"shell_execute", "[1]"}, {"file_read,glob", "[2]"}},
			wantLTC:      "file_read,glob",
			wantMessages: "[2]",
		},
		{
			name:         "single checkpoint persists",
			id:           "ckpt-b",
			existing:     true,
			lastPrompt:   "solo-task",
			checkpoints:  [][2]string{{"calc", "[]"}},
			wantLTC:      "calc",
			wantMessages: "[]",
		},
		{
			name:         "missing session ignored gracefully",
			id:           "ckpt-missing",
			existing:     false,
			lastPrompt:   "",
			checkpoints:  [][2]string{{"any_tool", "ignored"}},
			wantLTC:      "",
			wantMessages: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			if tt.existing {
				seedCheckpointSession(t, s, tt.id, tt.lastPrompt, "seed-messages")
			}
			for _, cp := range tt.checkpoints {
				if err := s.SaveAgentCheckpoint(tt.id, cp[0], cp[1]); err != nil {
					t.Fatalf("SaveAgentCheckpoint(%q): unexpected error: %v", tt.id, err)
				}
			}
			if !tt.existing {
				sd, err := s.GetAgentSession(tt.id)
				if err != nil {
					t.Fatalf("GetAgentSession: %v", err)
				}
				if sd != nil {
					t.Fatalf("expected no session created by checkpoint, got %+v", sd)
				}
				return
			}
			assertCheckpointFields(t, s, tt.id, tt.wantLTC, tt.wantMessages, tt.lastPrompt)
		})
	}
}

func TestSaveAgentCheckpointAdvancesUpdatedAt(t *testing.T) {
	s := newTestStore(t)
	seedCheckpointSession(t, s, "adv-upt", "task", "before")
	before, err := s.GetAgentSession("adv-upt")
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := s.SaveAgentCheckpoint("adv-upt", "time_get", "after"); err != nil {
		t.Fatalf("SaveAgentCheckpoint: %v", err)
	}
	after, err := s.GetAgentSession("adv-upt")
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
	}
}

func TestSaveAgentCheckpointKeepsOtherColumns(t *testing.T) {
	s := newTestStore(t)
	seedCheckpointSession(t, s, "keep-cols", "orig-task", "seed")

	if err := s.SaveAgentCheckpoint("keep-cols", "time_get", "[new]"); err != nil {
		t.Fatalf("SaveAgentCheckpoint: %v", err)
	}

	sd, err := s.GetAgentSession("keep-cols")
	if err != nil {
		t.Fatalf("GetAgentSession: %v", err)
	}
	if sd.LastPrompt != "orig-task" {
		t.Errorf("LastPrompt changed: got %q, want %q", sd.LastPrompt, "orig-task")
	}
	if sd.AgentName != "worker" || sd.Status != "active" {
		t.Errorf("other columns changed: %+v", sd)
	}
}
