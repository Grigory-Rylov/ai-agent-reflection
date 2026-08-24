package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
)

func twoModelSource() *fakeSource {
	return &fakeSource{
		current: "alpha",
		models: map[string]modelsconfig.ModelEntry{
			"alpha": {Name: "alpha-model", Host: "127.0.0.1:8081", Type: "llama", StartScript: "/opt/a.sh", StopScript: "/opt/as.sh"},
			"beta":  {Name: "beta-model", Host: "127.0.0.1:8020", Type: "vllm", StartScript: "/opt/b.sh", StopScript: "/opt/bs.sh"},
		},
	}
}

func TestPlanningTable(t *testing.T) {
	tests := []struct {
		name           string
		fs             *fakeSource
		target         string
		wantNeeds      bool
		wantStop       string
		wantStart      string
		wantDeadlineMS int64
	}{
		{
			name: "fresh_alias_start_only_keeps_stop_empty_when_old_has_none",
			fs: &fakeSource{
				current: "bare",
				models: map[string]modelsconfig.ModelEntry{
					"bare":  {Name: "bm", Host: "h:1"},
					"fresh": {Name: "fm", Host: "h:2", StartScript: "/opt/f.sh", Type: "llama"},
				},
			},
			target:         "fresh",
			wantNeeds:      true,
			wantStop:       "",
			wantStart:      "/opt/f.sh",
			wantDeadlineMS: int64((10 * time.Minute) / time.Millisecond),
		},
		{
			name:           "swap_between_two_full_entries_takes_old_stop_and_new_start",
			fs:             twoModelSource(),
			target:         "beta",
			wantNeeds:      true,
			wantStop:       "/opt/as.sh",
			wantStart:      "/opt/b.sh",
			wantDeadlineMS: int64((20 * time.Minute) / time.Millisecond),
		},
		{
			name:        "same_alias_never_transitions_even_with_scripts",
			fs:          twoModelSource(),
			target:      "alpha",
			wantNeeds:   false,
			wantStop:    "",
			wantStart:   "/opt/a.sh",
		},
		{
			name: "different_alias_target_without_start_script_does_not_transition",
			fs: &fakeSource{
				current: "alpha",
				models: map[string]modelsconfig.ModelEntry{
					"alpha": {Name: "am", Host: "h:1", StartScript: "/opt/a.sh"},
					"quiet": {Name: "qm", Host: "h:2"},
				},
			},
			target:    "quiet",
			wantNeeds: false,
		},
		{
			name:           "vllm_deadline_is_twenty_minutes",
			fs:             twoModelSource(),
			target:         "beta",
			wantNeeds:      true,
			wantStop:       "/opt/as.sh",
			wantStart:      "/opt/b.sh",
			wantDeadlineMS: int64((20 * time.Minute) / time.Millisecond),
		},
		{
			name:           "llama_deadline_is_ten_minutes",
			fs:             twoModelSource(),
			target:         "alpha",
			wantNeeds:      false,
			wantStop:       "",
			wantStart:      "/opt/a.sh",
			wantDeadlineMS: int64((10 * time.Minute) / time.Millisecond),
		},
		{
			name: "unknown_type_uses_default_deadline",
			fs: &fakeSource{
				current: "old",
				models: map[string]modelsconfig.ModelEntry{
					"old": {Name: "om", Host: "h:1"},
					"x":   {Name: "xm", Host: "h:2", Type: "weird-engine", StartScript: "/opt/x.sh"},
				},
			},
			target:         "x",
			wantNeeds:      true,
			wantStop:       "",
			wantStart:      "/opt/x.sh",
			wantDeadlineMS: int64(DefaultReadyDeadline / time.Millisecond),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(tc.fs, nil, nil, nil)
			plan, needs := m.PlanTransition(tc.target)
			if needs != tc.wantNeeds {
				t.Fatalf("needs = %v, want %v", needs, tc.wantNeeds)
			}
			if plan.StopScript != tc.wantStop {
				t.Errorf("stop script = %q, want %q", plan.StopScript, tc.wantStop)
			}
			if plan.StartScript != tc.wantStart {
				t.Errorf("start script = %q, want %q", plan.StartScript, tc.wantStart)
			}
			if tc.wantDeadlineMS > 0 && plan.Deadline != time.Duration(tc.wantDeadlineMS)*time.Millisecond {
				t.Errorf("deadline = %v, want %dms", plan.Deadline, tc.wantDeadlineMS)
			}
			if plan.ReadyURL != ReadyURLFor(tc.fs.models[tc.target].Host) {
				t.Errorf("ready url = %q, want %q", plan.ReadyURL, ReadyURLFor(tc.fs.models[tc.target].Host))
			}
			if plan.ModelSubstr != tc.fs.models[tc.target].Name {
				t.Errorf("model substr = %q, want %q", plan.ModelSubstr, tc.fs.models[tc.target].Name)
			}
		})
	}
}

func TestWaitForReadyImmediate(t *testing.T) {
	fs := twoModelSource()
	rec := &notifierRecorder{}
	m := newTestManager(fs, nil, func(url, sub string) (bool, error) { return true, nil }, nil)
	plan := TransitionPlan{Alias: "beta", ReadyURL: "http://h:8020/v1/models", ModelSubstr: "beta-model", Deadline: time.Hour}

	if err := m.WaitForReady(context.Background(), plan, rec.fn()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one notify, got %v", calls)
	}
	if calls[0] != StatusEngineReady {
		t.Errorf("first notify = %q, want %q", calls[0], StatusEngineReady)
	}
}

func TestWaitForReadyFlipsAfterFailedPolls(t *testing.T) {
	fs := twoModelSource()
	fp := &flakyProbe{fails: 3}
	rec := &notifierRecorder{}
	m := newTestManager(fs, nil, fp.fn(), nil)
	plan := TransitionPlan{Alias: "beta", ReadyURL: "http://h:8020/v1/models", ModelSubstr: "beta-model", Deadline: time.Hour}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.WaitForReady(ctx, plan, rec.fn()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := rec.snapshot()
	if fp.callCount() < 4 {
		t.Errorf("probe called %d times, want >= 4 (3 fails + success)", fp.callCount())
	}
	if len(calls) > 3 {
		t.Errorf("too many notifies for %vms throttle: %v", m.NotifyThrottle, calls)
	}
	if calls[len(calls)-1] != StatusEngineReady {
		t.Errorf("final notify = %q, want %q", calls[len(calls)-1], StatusEngineReady)
	}
	waitNotices := 0
	for _, c := range calls {
		if c != StatusEngineReady {
			waitNotices++
		}
	}
	if waitNotices > 2 {
		t.Errorf("expected at most 2 interim 'still waiting' notices, got %d: %v", waitNotices, calls)
	}
}

func TestWaitForReadyDeadlineExceeded(t *testing.T) {
	fs := twoModelSource()
	m := newTestManager(fs, nil, func(url, sub string) (bool, error) {
		return false, context.DeadlineExceeded
	}, nil)
	plan := TransitionPlan{Alias: "beta", ReadyURL: "http://h:8020/v1/models", ModelSubstr: "beta-model", Deadline: 500 * time.Millisecond}

	start := time.Now()
	err := m.WaitForReady(context.Background(), plan, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected deadline error, got nil")
	}
	requireContains(t, err.Error(), "beta")
	requireContains(t, err.Error(), "not ready")
	requireContains(t, err.Error(), "context deadline exceeded")
	if elapsed < 450*time.Millisecond || elapsed > 3*time.Second {
		t.Errorf("elapsed %v outside sanity band [450ms, 3s]", elapsed)
	}
}

func TestWaitForReadyCanceledMidWait(t *testing.T) {
	fs := twoModelSource()
	fp := &flakyProbe{fails: 1 << 30}
	m := newTestManager(fs, nil, fp.fn(), nil)
	plan := TransitionPlan{Alias: "beta", ReadyURL: "http://h:8020/v1/models", ModelSubstr: "beta-model", Deadline: time.Hour}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.WaitForReady(ctx, plan, nil) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error on cancel")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error does not wrap context.Canceled: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForReady did not honor cancellation")
	}
}

func TestTransitionEndToEndSequence(t *testing.T) {
	fs := twoModelSource()
	run := &countingRunFn{tail: "launched"}
	probeCalls := 0
	rec := &notifierRecorder{}
	m := newTestManager(fs, run.fn(), func(url, sub string) (bool, error) {
		probeCalls++
		return true, nil
	}, nil)

	err := m.Transition(context.Background(), "beta", rec.fn())
	if err != nil {
		t.Fatalf("transition: %v", err)
	}
	if run.callCount() != 2 {
		t.Fatalf("RunFn called %d times, want 2 (stop+start)", run.callCount())
	}
	calls := rec.snapshot()
	wantSeq := []string{"Stopping previous engine…", "Starting engine…", StatusEngineReady}
	if len(calls) != len(wantSeq) {
		t.Fatalf("notify sequence = %v, want %v", calls, wantSeq)
	}
	for i, w := range wantSeq {
		if calls[i] != w {
			t.Errorf("notify[%d] = %q, want %q", i, calls[i], w)
		}
	}
	if probeCalls < 1 {
		t.Error("readiness probe never ran")
	}
}

func TestTransitionFailingStartPropagatesWithoutWaiting(t *testing.T) {
	fs := twoModelSource()
	run := &countingRunFn{err: errors.New("launcher exploded")}
	probeCalled := false
	m := newTestManager(fs, run.fn(), func(url, sub string) (bool, error) {
		probeCalled = true
		return true, nil
	}, nil)

	err := m.Transition(context.Background(), "beta", nil)
	if err == nil {
		t.Fatal("expected error from failing start script")
	}
	requireContains(t, err.Error(), "beta")
	requireContains(t, err.Error(), "launcher exploded")
	if probeCalled {
		t.Error("readiness probe ran although start failed")
	}
}

type watchdogProbe struct {
	mu       sync.Mutex
	unstable int
	forever  bool
	total    int
}

func (wp *watchdogProbe) fn() ProbeFn {
	return func(url string, modelSubstr string) (bool, error) {
		wp.mu.Lock()
		defer wp.mu.Unlock()
		wp.total++
		if wp.forever || wp.total <= wp.unstable {
			return false, fmt.Errorf("health probe miss #%d", wp.total)
		}
		return true, nil
	}
}

func (wp *watchdogProbe) totalCount() int {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	return wp.total
}

func TestWatchdogSkipsRecoveryWithoutStartScript(t *testing.T) {
	fs := &fakeSource{
		current: "silent",
		models: map[string]modelsconfig.ModelEntry{
			"silent": {Name: "sm"},
		},
	}
	sd := &scriptedDetector{stall: true, reason: "forced-stall"}
	run := &countingRunFn{}
	rec := &notifierRecorder{}
	log := &recordLogger{}
	m := newTestManager(fs, run.fn(), nil, sd).WithLog(log)

	cancel := m.StartWatchdog(context.Background(), rec.fn())
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if countSubstring(rec.snapshot(), "manual intervention required") >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	jointDone := make(chan struct{})
	go func() {
		cancel()
		close(jointDone)
	}()
	select {
	case <-jointDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("watchdog did not stop within grace period")
	}
	calls := rec.snapshot()
	if got := countSubstring(calls, "manual intervention required"); got < 1 {
		t.Errorf("skip-path notice missing, got %v", calls)
	}
	if got := countSubstring(calls, StatusEngineReady); got != 0 {
		t.Errorf("ready notices = %d, want 0 (no start script -> skipped) (%v)", got, calls)
	}
	if !log.contains("skipping recovery") {
		t.Errorf("expected skip-recovery debug line, got %v", log.lines)
	}
	if run.callCount() != 0 {
		t.Errorf("RunFn invoked %d times, want 0 (skip path must not launch anything)", run.callCount())
	}
}

func TestWatchdogAutoRestartsAfterRepeatedMisses(t *testing.T) {
	fs := twoModelSource()
	run := &countingRunFn{tail: "relaunch"}
	wp := &watchdogProbe{unstable: 3}
	rec := &notifierRecorder{}
	m := newTestManager(fs, run.fn(), wp.fn(), nil)

	cancel := m.StartWatchdog(context.Background(), rec.fn())
	defer cancel()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if run.callCount() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls := rec.snapshot()
	if got := countSubstring(calls, "attempting automatic restart"); got < 1 {
		t.Errorf("recovery attempts = %d, want >= 1 (%v)", got, calls)
	}
	if run.callCount() < 1 {
		t.Error("recovery did not invoke RunFn")
	}
}

func TestNoStallDetectorNeverStalls(t *testing.T) {
	d := NoStallDetector{}
	stalled, reason := d.DetectStall()
	if stalled || reason != "" {
		t.Errorf("NoStallDetector = (%v, %q), want (false, \"\")", stalled, reason)
	}
}

func countSubstring(list []string, sub string) int {
	n := 0
	for _, s := range list {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}
