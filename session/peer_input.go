package session

import "sync"

type PeerInput struct {
	mu       sync.Mutex
	pending  []string
	promoted []string
}

func (in *PeerInput) Admit(text string) {
	if in == nil {
		return
	}
	in.mu.Lock()
	in.pending = append(in.pending, text)
	in.mu.Unlock()
}

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

func (in *PeerInput) HasPending() bool {
	if in == nil {
		return false
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	return len(in.pending) > 0
}

func (in *PeerInput) Clear() {
	if in == nil {
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	in.pending = nil
	in.promoted = nil
}
