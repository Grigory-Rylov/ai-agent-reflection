package agentloop

import (
	"sync"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/internalmsg"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tokenizers"
	"github.com/Grigory-Rylov/ai-agent-reflection/session"
)

// turnMirror copies messages produced inside the ephemeral per-turn agent
// session into the persistent peer session while the turn is still running, so
// a crash, restart or cancellation cannot wipe out work already done.
type turnMirror struct {
	target   *session.Session
	source   *session.Session
	mu       sync.Mutex
	cursor   int
	mirrored map[string]bool
}

func newTurnMirror(target, source *session.Session, from int) *turnMirror {
	return &turnMirror{
		target:   target,
		source:   source,
		cursor:   from,
		mirrored: make(map[string]bool),
	}
}

func (m *turnMirror) sync() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	history := m.source.GetHistory()
	for m.cursor < len(history) {
		m.copyMessage(history[m.cursor], m.cursor == len(history)-1)
		m.cursor++
	}
}

func (m *turnMirror) copyMessage(msg session.Message, last bool) {
	switch msg.Role {
	case session.UserRole:
		if isInnerTurnScaffolding(msg.Content) {
			return
		}
		m.mirrored[msg.Content] = true
		m.target.AddUserMessage(msg.Content)
	case session.AssistantRole:
		m.copyAssistantMessage(msg, last)
	case session.ToolRole:
		m.target.AddToolMessage(msg.ToolCallID, msg.Name, msg.Content)
	}
}

func isInnerTurnScaffolding(content string) bool {
	switch content {
	case tokenizers.CompactionUserMessage,
		tokenizers.CompactionAutoContinueText,
		tokenizers.CompactionOverflowContinueText:
		return true
	}
	return false
}

func (m *turnMirror) copyAssistantMessage(msg session.Message, last bool) {
	switch {
	case msg.Summary:
		return
	case msg.Internal || internalmsg.IsInternal(msg.Content):
		m.target.AddAssistantMessageInternal(msg.Content)
	case len(msg.ToolCalls) > 0:
		m.target.AddAssistantMessageWithToolCalls(msg.Content, msg.ToolCalls)
	case msg.Content != "" || last:
		m.target.AddAssistantMessage(msg.Content)
	}
}

func (m *turnMirror) alreadyMirrored(content string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mirrored[content]
}
