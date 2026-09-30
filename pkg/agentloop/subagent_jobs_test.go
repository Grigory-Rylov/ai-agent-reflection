package agentloop

import (
	"context"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/tools"
)

func TestSubJobWaitCompletes(t *testing.T) {
	job := subJobs.create("worker", "owner-wait-"+t.Name(), func() {})
	go func() {
		time.Sleep(20 * time.Millisecond)
		subJobs.complete(job.id, SubagentDone, tools.ToolResult{
			Success: true, Data: map[string]interface{}{"summary": "all done"},
		})
	}()

	res, ok := subJobs.wait(job.id, time.Second)
	if !ok || !res.Success {
		t.Fatalf("wait did not return success result ok=%v res=%v", ok, res)
	}

	var found bool
	for _, j := range subJobs.snapshot() {
		if j.id == job.id {
			found = true
			if j.status != SubagentDone {
				t.Errorf("status = %v, want done", j.status)
			}
		}
	}
	if !found {
		t.Fatalf("completed job missing from snapshot")
	}
}

func TestSubJobWaitTimeout(t *testing.T) {
	job := subJobs.create("worker", "owner-timeout-"+t.Name(), func() {})
	defer subJobs.cancel(job.id)
	if _, ok := subJobs.wait(job.id, 50*time.Millisecond); ok {
		t.Errorf("wait should time out for an uncompleted job")
	}
}

func TestSubJobCancelCancelsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	job := subJobs.create("worker", "owner-cancel-"+t.Name(), cancel)
	if !subJobs.cancel(job.id) {
		t.Fatalf("cancel returned false for active job")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatalf("job context was not cancelled")
	}
	if subJobs.cancel("job-does-not-exist") {
		t.Errorf("cancel of unknown id should return false")
	}
}

func TestSubJobPendingForOwnerIsolates(t *testing.T) {
	owner := "owner-iso-" + t.Name()
	a := subJobs.create("worker", owner, func() {})
	b := subJobs.create("qa", owner, func() {})
	defer subJobs.cancel(a.id)
	defer subJobs.cancel(b.id)

	if got := subJobs.pendingForOwner(owner); got != 2 {
		t.Fatalf("pendingForOwner = %d, want 2", got)
	}
	subJobs.complete(a.id, SubagentDone, tools.ToolResult{Success: true})
	if got := subJobs.pendingForOwner(owner); got != 1 {
		t.Errorf("pendingForOwner after one complete = %d, want 1", got)
	}
	if subJobs.pendingForOwner(owner+"-other") != 0 {
		t.Errorf("unrelated owner should report no pending jobs")
	}
}

func TestSubJobWaitForNextSettleAdvances(t *testing.T) {
	owner := "owner-settle-" + t.Name()
	job := subJobs.create("worker", owner, func() {})
	go func() {
		time.Sleep(20 * time.Millisecond)
		subJobs.complete(job.id, SubagentDone, tools.ToolResult{Success: true})
	}()

	if !subJobs.waitForNextSettle(context.Background(), owner, time.Second) {
		t.Fatalf("waitForNextSettle should return true after the job settles")
	}
	if subJobs.pendingForOwner(owner) != 0 {
		t.Errorf("job should not be pending after settle")
	}
}

func TestSubJobWaitForNextSettleTimesOut(t *testing.T) {
	owner := "owner-settle-timeout-" + t.Name()
	job := subJobs.create("worker", owner, func() {})
	defer subJobs.cancel(job.id)

	if subJobs.waitForNextSettle(context.Background(), owner, 50*time.Millisecond) {
		t.Errorf("waitForNextSettle should time out with nothing settling")
	}
}

func TestFormatJobResult(t *testing.T) {
	okMsg := formatJobResult("job-1", "worker", tools.ToolResult{
		Success: true, Data: map[string]interface{}{"summary": "shipped"},
	})
	if okMsg != "[subagent worker job job-1] done: shipped" {
		t.Errorf("ok format = %q", okMsg)
	}
	errMsg := formatJobResult("job-2", "qa", tools.ToolResult{Success: false, Error: "boom"})
	if errMsg != "[subagent qa job job-2] FAILED: boom" {
		t.Errorf("err format = %q", errMsg)
	}
}

func TestSubJobCreateEnforcesRunningCap(t *testing.T) {
	var created []*subJob
	defer func() {
		for _, j := range created {
			subJobs.cancel(j.id)
		}
	}()
	for {
		j := subJobs.create("worker", "owner-cap", func() {})
		if j == nil {
			break
		}
		created = append(created, j)
		if len(created) > maxRunningJobs+5 {
			t.Fatalf("running cap never engaged after %d creates", len(created))
		}
	}
	if len(created) < 1 {
		t.Fatalf("expected to create at least one job before the cap")
	}
	if got := subJobs.activeRunning(); got != maxRunningJobs {
		t.Errorf("active running = %d, want cap %d", got, maxRunningJobs)
	}
}

func TestSubJobEvictRemovesFromSnapshot(t *testing.T) {
	j := subJobs.create("worker", "owner-evict", func() {})
	subJobs.complete(j.id, SubagentDone, tools.ToolResult{Success: true})
	if !snapshotContains(j.id) {
		t.Fatalf("completed job should be present in snapshot before eviction")
	}
	subJobs.evict(j.id)
	if snapshotContains(j.id) {
		t.Errorf("evicted job should not appear in snapshot")
	}
}

func snapshotContains(id string) bool {
	for _, j := range subJobs.snapshot() {
		if j.id == id {
			return true
		}
	}
	return false
}
