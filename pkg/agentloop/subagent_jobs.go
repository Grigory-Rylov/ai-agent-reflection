package agentloop

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/agent"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

const (
	SubagentDone    SubagentStatus = "done"
	SubagentErrored SubagentStatus = "error"

	maxRunningJobs = 15
	jobRetention   = 5 * time.Minute
)

type subJob struct {
	id         string
	name       string
	owner      string
	status     SubagentStatus
	result     tools.ToolResult
	done       chan struct{}
	cancel     context.CancelFunc
	createdAt  time.Time
	finishedAt time.Time
}

type subJobStore struct {
	mu       sync.Mutex
	seq      int64
	jobs     map[string]*subJob
	settled  map[string]int
	settleCh chan struct{}
}

var subJobs = &subJobStore{
	jobs:     make(map[string]*subJob),
	settled:  make(map[string]int),
	settleCh: make(chan struct{}, 1),
}

func ownerToken(bgOwner string, peerID int64) string {
	if bgOwner != "" {
		return bgOwner
	}
	return "main:" + strconv.FormatInt(peerID, 10)
}

func NewSubagentWatcher() agent.SubagentWatcher {
	return jobWatcher{}
}

type jobWatcher struct{}

func (jobWatcher) PendingSubagents(owner string) int {
	return subJobs.pendingForOwner(owner)
}

func (jobWatcher) WaitForSubagent(ctx context.Context, owner string, timeout time.Duration) bool {
	return subJobs.waitForNextSettle(ctx, owner, timeout)
}

func (s *subJobStore) create(name, owner string, cancel context.CancelFunc) *subJob {
	s.mu.Lock()
	if s.activeRunningLocked() >= maxRunningJobs {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	id := fmt.Sprintf("job-%d", atomic.AddInt64(&s.seq, 1))
	job := &subJob{
		id:        id,
		name:      name,
		owner:     owner,
		status:    SubagentRunning,
		done:      make(chan struct{}),
		cancel:    cancel,
		createdAt: time.Now(),
	}
	s.mu.Lock()
	s.jobs[id] = job
	s.mu.Unlock()
	return job
}

func (s *subJobStore) complete(id string, status SubagentStatus, result tools.ToolResult) {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if ok {
		job.status = status
		job.result = result
		job.finishedAt = time.Now()
		s.settled[job.owner]++
	}
	s.mu.Unlock()
	if ok {
		close(job.done)
		s.signalSettle()
		time.AfterFunc(jobRetention, func() { s.evict(id) })
	}
}

func (s *subJobStore) signalSettle() {
	select {
	case s.settleCh <- struct{}{}:
	default:
	}
}

func (s *subJobStore) pendingForOwner(owner string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingForOwnerLocked(owner)
}

func (s *subJobStore) pendingForOwnerLocked(owner string) int {
	n := 0
	for _, j := range s.jobs {
		if j.owner == owner && j.status == SubagentRunning {
			n++
		}
	}
	return n
}

func (s *subJobStore) waitForNextSettle(ctx context.Context, owner string, timeout time.Duration) bool {
	s.mu.Lock()
	before := s.settled[owner]
	s.mu.Unlock()

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		s.mu.Lock()
		advanced := s.settled[owner] > before
		pending := s.pendingForOwnerLocked(owner)
		s.mu.Unlock()

		if advanced || pending == 0 {
			return true
		}

		select {
		case <-s.settleCh:
		case <-deadline.C:
			s.mu.Lock()
			res := s.settled[owner] > before
			s.mu.Unlock()
			return res
		case <-ctx.Done():
			return false
		}
	}
}

func (s *subJobStore) evict(id string) {
	s.mu.Lock()
	delete(s.jobs, id)
	s.mu.Unlock()
}

func (s *subJobStore) activeRunning() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeRunningLocked()
}

func (s *subJobStore) activeRunningLocked() int {
	n := 0
	for _, j := range s.jobs {
		if j.status == SubagentRunning {
			n++
		}
	}
	return n
}

func (s *subJobStore) cancel(id string) bool {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if ok && job.status == SubagentRunning {
		job.status = SubagentAborted
	}
	var cancelFn context.CancelFunc
	if ok {
		cancelFn = job.cancel
	}
	s.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		return true
	}
	return false
}

func (s *subJobStore) wait(id string, timeout time.Duration) (tools.ToolResult, bool) {
	s.mu.Lock()
	job, ok := s.jobs[id]
	s.mu.Unlock()
	if !ok {
		return tools.ToolResult{Success: false, Error: fmt.Sprintf("no job with id %q", id)}, false
	}
	if timeout > 0 {
		select {
		case <-job.done:
			return job.result, true
		case <-time.After(timeout):
			return tools.ToolResult{}, false
		}
	}
	<-job.done
	return job.result, true
}

func (s *subJobStore) snapshot() []subJob {
	s.mu.Lock()
	out := make([]subJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, subJob{
			id:         j.id,
			name:       j.name,
			owner:      j.owner,
			status:     j.status,
			result:     j.result,
			createdAt:  j.createdAt,
			finishedAt: j.finishedAt,
		})
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].createdAt.Before(out[j].createdAt) })
	return out
}

func (t *SubAgentTool) cloneForAsync(jobID string) *SubAgentTool {
	sub := t.cloneForSibling(0)
	sub.spawnNonce = jobID
	return sub
}

func (t *SubAgentTool) startAsyncJob(ctx context.Context, item batchTask, globalContext string) string {
	jobCtx, cancel := context.WithCancel(ctx)
	job := subJobs.create(item.agentName(), ownerToken(t.BGOwner, t.PeerID), cancel)
	if job == nil {
		cancel()
		return ""
	}
	go func() {
		spawn := t.cloneForAsync(job.id)
		res := spawn.runSpawn(jobCtx, item.agentName(), item.promptWithContext(globalContext))
		status := SubagentDone
		if !res.Success {
			status = SubagentErrored
		}
		subJobs.complete(job.id, status, res)
		t.autoDeliverJobResult(job.id, item.agentName(), res)
	}()
	return job.id
}

func (t *SubAgentTool) autoDeliverJobResult(jobID, name string, res tools.ToolResult) {
	hub := tools.GetBackgroundHub()
	if hub == nil {
		return
	}
	text := formatJobResult(jobID, name, res)
	if fn := hub.DeliveryFor(t.BGOwner); fn != nil {
		fn(t.PeerID, text)
		return
	}
	if t.BGOwner == "" {
		if fn := hub.DeliveryFor("main"); fn != nil {
			fn(t.PeerID, text)
			return
		}
	}
	if t.Log != nil {
		t.Log.WarnLogf("[BG] subagent job %s (%s) result dead-lettered: no delivery sink for owner %q", jobID, name, t.BGOwner)
	}
}

func formatJobResult(jobID, name string, res tools.ToolResult) string {
	if !res.Success {
		return fmt.Sprintf("[subagent %s job %s] FAILED: %s", name, jobID, res.Error)
	}
	summary := ""
	if m, ok := res.Data.(map[string]interface{}); ok {
		if s, ok := m["summary"].(string); ok {
			summary = s
		}
	}
	return fmt.Sprintf("[subagent %s job %s] done: %s", name, jobID, summary)
}
