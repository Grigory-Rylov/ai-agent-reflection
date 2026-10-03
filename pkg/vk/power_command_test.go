package vk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

const ownerPeerID = int64(2000000001)

func jsonMarshalKeyboard(kb map[string]interface{}) (string, error) {
	b, err := json.Marshal(kb)
	return string(b), err
}

// The agent bot must ignore /shutdown and /reboot: they are executed by the
// restarter (which stays alive even when the agent is stopped), so the agent
// must neither execute them nor reply "unknown command".
func TestPowerCommandsIgnoredByAgent(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	h := NewBotHandler(nil, mock, log)
	h.mainPeerID = ownerPeerID

	for _, cmd := range []string{"/shutdown", "/reboot"} {
		got := h.ProcessMessage(cmd, ownerPeerID)
		if got != "" {
			t.Errorf("%s from owner: agent must stay silent (restarter executes it), got %q", cmd, got)
		}
	}
	if mock.lastMessage != "" {
		t.Errorf("power commands must not reach the model, got %q", mock.lastMessage)
	}
}

func TestPowerCommandsIgnoredByAgentForStranger(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	h := NewBotHandler(nil, mock, log)
	h.mainPeerID = ownerPeerID

	for _, cmd := range []string{"/shutdown", "/reboot"} {
		got := h.ProcessMessage(cmd, 999)
		if got != "" {
			t.Errorf("%s from stranger: agent must stay silent, got %q", cmd, got)
		}
	}
}

func TestPowerCommandsListedInHelp(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	h := NewBotHandler(nil, newMockAgentLoop(), log)
	h.mainPeerID = ownerPeerID

	help := h.ProcessMessage("/help", ownerPeerID)
	if !strings.Contains(help, "/shutdown") || !strings.Contains(help, "/reboot") {
		t.Errorf("/help must list /shutdown and /reboot:\n%s", help)
	}
}

func TestPowerCommandsDeniedForOtherPeer(t *testing.T) {
	var sent []string
	p := &PowerCommands{
		OwnerPeerID: ownerPeerID,
		ShutdownCmd: "echo should-not-run",
		RebootCmd:   "echo should-not-run",
		Send:        func(peerID int64, text string) { sent = append(sent, text) },
	}

	p.Handle("shutdown", 999)
	p.Handle("reboot", 999)

	if len(sent) != 2 {
		t.Fatalf("expected 2 replies, got %d: %v", len(sent), sent)
	}
	for _, s := range sent {
		if !strings.Contains(s, "владельцу") {
			t.Errorf("expected owner-only refusal, got %q", s)
		}
	}
}

func TestPowerCommandsDeniedWhenNoOwnerConfigured(t *testing.T) {
	var sent []string
	p := &PowerCommands{
		ShutdownCmd: "echo should-not-run",
		RebootCmd:   "echo should-not-run",
		Send:        func(peerID int64, text string) { sent = append(sent, text) },
	}

	p.Handle("shutdown", ownerPeerID)
	if len(sent) != 1 || !strings.Contains(sent[0], "владельцу") {
		t.Errorf("expected owner-only refusal when peer_id is 0, got %v", sent)
	}
}

func TestPowerCommandsNotConfigured(t *testing.T) {
	var sent []string
	p := &PowerCommands{
		OwnerPeerID: ownerPeerID,
		Send:        func(peerID int64, text string) { sent = append(sent, text) },
	}

	p.Handle("shutdown", ownerPeerID)
	p.Handle("reboot", ownerPeerID)

	if len(sent) != 2 {
		t.Fatalf("expected 2 replies, got %d", len(sent))
	}
	if !strings.Contains(sent[0], "shutdown не настроен") {
		t.Errorf("expected 'shutdown not configured', got %q", sent[0])
	}
	if !strings.Contains(sent[1], "reboot не настроен") {
		t.Errorf("expected 'reboot not configured', got %q", sent[1])
	}
}

func TestPowerCommandsExecutesConfiguredCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "marker")
	var sent []string
	p := &PowerCommands{
		OwnerPeerID: ownerPeerID,
		ShutdownCmd: "echo dry-run > " + marker,
		RebootCmd:   "exit 3",
		Send:        func(peerID int64, text string) { sent = append(sent, text) },
	}

	p.Handle("shutdown", ownerPeerID)
	if len(sent) != 1 || !strings.Contains(sent[0], "shutdown") {
		t.Fatalf("expected shutdown confirmation, got %v", sent)
	}

	// The command runs in a detached goroutine; wait for the marker file.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, statErr := os.Stat(marker)
		if statErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("marker file was not created within 5s: %v", statErr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	p.Handle("reboot", ownerPeerID)
	if len(sent) != 2 || !strings.Contains(sent[1], "reboot") {
		t.Fatalf("expected reboot confirmation, got %v", sent)
	}
}

func TestCommandKeyboardShowsPowerButtonsOnlyWhenConfigured(t *testing.T) {
	kb := CreateCommandKeyboard("", "")
	kbJSON, err := jsonMarshalKeyboard(kb)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(kbJSON, "/shutdown") || strings.Contains(kbJSON, "/reboot") {
		t.Errorf("power buttons must be hidden when unconfigured: %s", kbJSON)
	}

	kb = CreateCommandKeyboard("echo s", "echo r")
	kbJSON, _ = jsonMarshalKeyboard(kb)
	if !strings.Contains(kbJSON, "/shutdown") || !strings.Contains(kbJSON, "/reboot") {
		t.Errorf("power buttons must appear when configured: %s", kbJSON)
	}
}
