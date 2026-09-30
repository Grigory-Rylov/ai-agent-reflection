package agentloop

import "sync"

type agentActivity struct {
	mu   sync.Mutex
	busy map[string]int
}

var globalAgentActivity = &agentActivity{busy: make(map[string]int)}

func (a *agentActivity) enter(name string) func() {
	if name == "" {
		return func() {}
	}
	a.mu.Lock()
	a.busy[name]++
	a.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.busy[name]--
			if a.busy[name] <= 0 {
				delete(a.busy, name)
			}
		})
	}
}

func (a *agentActivity) active(name string) bool {
	if name == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.busy[name] > 0
}

func (a *agentActivity) snapshot() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]int, len(a.busy))
	for k, v := range a.busy {
		out[k] = v
	}
	return out
}

func MarkAgentActive(name string) func() {
	return globalAgentActivity.enter(name)
}

func AgentCurrentlyActive(name string) bool {
	return globalAgentActivity.active(name)
}
