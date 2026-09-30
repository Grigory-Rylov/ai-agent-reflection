package agentloop

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type SubagentStatus string

const (
	SubagentRunning SubagentStatus = "running"
	SubagentAborted SubagentStatus = "aborted"
)

type SubagentSpawn struct {
	ID        string
	AgentName string
	PeerID    int64
	Depth     int
	Status    SubagentStatus
	StartedAt time.Time
	cancel    context.CancelFunc
}

type subagentRegistryType struct {
	mu     sync.Mutex
	seq    int64
	active map[string]*SubagentSpawn
}

var subagentRegistry = &subagentRegistryType{active: make(map[string]*SubagentSpawn)}

func (r *subagentRegistryType) register(agentName string, peerID int64, depth int, cancel context.CancelFunc) string {
	id := fmt.Sprintf("sp-%d", atomic.AddInt64(&r.seq, 1))
	r.mu.Lock()
	r.active[id] = &SubagentSpawn{
		ID:        id,
		AgentName: agentName,
		PeerID:    peerID,
		Depth:     depth,
		Status:    SubagentRunning,
		StartedAt: time.Now(),
		cancel:    cancel,
	}
	r.mu.Unlock()
	return id
}

func (r *subagentRegistryType) finish(id string) {
	r.mu.Lock()
	delete(r.active, id)
	r.mu.Unlock()
}

func (r *subagentRegistryType) cancel(id string) bool {
	r.mu.Lock()
	spawn, ok := r.active[id]
	var cancelFn context.CancelFunc
	if ok {
		spawn.Status = SubagentAborted
		cancelFn = spawn.cancel
	}
	r.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return true
	}
	return false
}

func (r *subagentRegistryType) list() []SubagentSpawn {
	r.mu.Lock()
	out := make([]SubagentSpawn, 0, len(r.active))
	for _, spawn := range r.active {
		out = append(out, SubagentSpawn{
			ID:        spawn.ID,
			AgentName: spawn.AgentName,
			PeerID:    spawn.PeerID,
			Depth:     spawn.Depth,
			Status:    spawn.Status,
			StartedAt: spawn.StartedAt,
		})
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

func ListSubagentSpawns() []SubagentSpawn {
	return subagentRegistry.list()
}

func CancelSubagentSpawn(id string) bool {
	return subagentRegistry.cancel(id)
}
