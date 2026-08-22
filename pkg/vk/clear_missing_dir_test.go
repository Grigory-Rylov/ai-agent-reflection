package vk

import (
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

func TestClearFallsBackWhenWorkingDirMissing(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	mock := newMockAgentLoop()
	handler := NewBotHandlerWithPeerID(nil, mock, log, 0, 0, nil, nil)

	peerID := int64(3333)
	sess := mock.EnsureSession(peerID)
	if sess == nil {
		t.Fatal("expected session")
	}
	sess.SetWorkingDir("/nonexistent/dir/xyz-123")

	result := handler.ProcessMessage("/clear", peerID)

	if !strings.Contains(result, "Сессия сброшена") {
		t.Errorf("BUG: /clear failed instead of falling back, result: %q", result)
	}
}
