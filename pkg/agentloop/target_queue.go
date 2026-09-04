package agentloop

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/util/stringutil"
)

type TargetRunner func(ctx context.Context, agentName, prompt string, peerID int64) (string, error)
type TargetDeliver func(agentName string, peerID int64, response string, err error)

type targetJob struct {
	agentName string
	prompt    string
	peerID    int64
}

type targetLane struct {
	busy   bool
	active int
	jobs   []*targetJob
}

type TargetQueue struct {
	mu    sync.Mutex
	lanes map[string]*targetLane
	run   TargetRunner
	deliv TargetDeliver
}

func NewTargetQueue(run TargetRunner, deliv TargetDeliver) *TargetQueue {
	return &TargetQueue{
		lanes: make(map[string]*targetLane),
		run:   run,
		deliv: deliv,
	}
}

func (q *TargetQueue) Submit(agentName, prompt string, peerID int64) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	lane := q.laneForLocked(agentName)
	pos := len(lane.jobs) + lane.active
	lane.jobs = append(lane.jobs, &targetJob{agentName: agentName, prompt: prompt, peerID: peerID})
	if !lane.busy {
		lane.busy = true
		go q.pumpLane(agentName)
	}
	return pos
}

func (q *TargetQueue) laneForLocked(name string) *targetLane {
	lane, ok := q.lanes[name]
	if !ok {
		lane = &targetLane{}
		q.lanes[name] = lane
	}
	return lane
}

func (q *TargetQueue) pumpLane(name string) {
	for {
		job := q.takeNext(name)
		if job == nil {
			return
		}
		logger.DebugToFile("[TARGET] lane #%s: starting job for peer %d: %s", name, job.peerID, logLine(job.prompt))
		resp, err := q.runAgentSafely(job)
		if err != nil {
			logger.DebugToFile("[TARGET] lane #%s: job FAILED for peer %d: %v", name, job.peerID, err)
		} else {
			logger.DebugToFile("[TARGET] lane #%s: job DONE for peer %d, delivering answer (%d chars)", name, job.peerID, len(resp))
		}
		q.finishJob(name, job, resp, err)
	}
}

func logLine(s string) string {
	return stringutil.Truncate(strings.Join(strings.Fields(s), " "), 120, "...")
}

func (q *TargetQueue) runAgentSafely(job *targetJob) (resp string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("agent #%s panicked: %v", job.agentName, r)
		}
	}()
	return q.runAgent(job)
}

func (q *TargetQueue) finishJob(name string, job *targetJob, resp string, err error) {
	q.deliver(job, resp, err)
	q.noteFinished(name)
}

func (q *TargetQueue) noteFinished(name string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.laneForLocked(name).active--
}

func (q *TargetQueue) takeNext(name string) *targetJob {
	q.mu.Lock()
	defer q.mu.Unlock()

	lane := q.laneForLocked(name)
	if len(lane.jobs) == 0 {
		lane.busy = false
		return nil
	}
	lane.active++
	job := lane.jobs[0]
	lane.jobs = lane.jobs[1:]
	return job
}

func (q *TargetQueue) runAgent(job *targetJob) (string, error) {
	if q.run == nil {
		return "", nil
	}
	return q.run(context.Background(), job.agentName, job.prompt, job.peerID)
}

func (q *TargetQueue) deliver(job *targetJob, resp string, err error) {
	if q.deliv == nil {
		return
	}
	q.deliv(job.agentName, job.peerID, resp, err)
}
