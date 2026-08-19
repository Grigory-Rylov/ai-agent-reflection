package session

import "sync"

// PeerInput is a per-peer inbox for user messages that arrive while the agent
// is already mid-turn. It mirrors the opencode "steer" delivery model:
//
//   - The VK handler admits every incoming message here (Admit).
//   - The running agent loop drains it at each LLM turn boundary (Drain) and
//     adds the messages to its session, so the user's message becomes the next
//     thing the agent processes instead of blocking behind a long-held mutex.
//   - After the loop drains a message, its own handler goroutine finds it
//     already claimed (Claim returns false) and drops it — the running turn
//     will answer it.
//   - Messages that arrive too late to be drained are still claimed by their
//     own goroutine and processed as a normal follow-up turn (no message loss).
//
// The inbox is in-memory and transient; it is not part of the persisted
// session history.
type PeerInput struct {
	mu       sync.Mutex
	pending  []string
	promoted []string
}

// Admit appends a message to the inbox.
func (in *PeerInput) Admit(text string) {
	if in == nil {
		return
	}
	in.mu.Lock()
	in.pending = append(in.pending, text)
	in.mu.Unlock()
}

// Claim removes one specific message from the inbox. The handler calls it
// after acquiring the peer lock: if the running loop already drained the
// message, Claim returns false and the handler must drop it (the loop has
// incorporated it into its turn). If it returns true, the handler owns the
// message and must process it as a regular turn.
func (in *PeerInput) Claim(text string) bool {
	if in == nil {
		return false
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	for i, m := range in.pending {
		if m == text {
			in.pending = append(in.pending[:i], in.pending[i+1:]...)
			return true
		}
	}
	return false
}

// Drain removes and returns all pending messages. The running agent loop calls
// this at each turn boundary and adds the result to its session.
func (in *PeerInput) Drain() []string {
	if in == nil {
		return nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if len(in.pending) == 0 {
		return nil
	}
	out := in.pending
	in.pending = nil
	in.promoted = append(in.promoted, out...)
	return out
}

// TakePromoted returns the messages drained since the last call and clears the
// bookkeeping list. The agent-loop layer uses it to mirror promoted messages
// into the durable session history after a run.
func (in *PeerInput) TakePromoted() []string {
	if in == nil {
		return nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	out := in.promoted
	in.promoted = nil
	return out
}

// HasPending reports whether any messages are waiting to be processed.
func (in *PeerInput) HasPending() bool {
	if in == nil {
		return false
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	return len(in.pending) > 0
}

// Clear drops both pending and promoted messages. Called on session reset
// (/clear) so stale messages never leak into a fresh session.
func (in *PeerInput) Clear() {
	if in == nil {
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	in.pending = nil
	in.promoted = nil
}
