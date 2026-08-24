package engine

import (
	"testing"
	"time"
)

type fakeClock struct {
	t time.Time
}

func (f *fakeClock) Now() time.Time { return f.t }

type recordingLogger struct {
	lines []string
}

func (rl *recordingLogger) DebugLog(string, ...any)         {}
func (rl *recordingLogger) InfoLog(string, ...any)          {}
func (rl *recordingLogger) WarnLog(string, ...any)          {}
func (rl *recordingLogger) ErrorLog(string, ...any)         {}
func (rl *recordingLogger) DebugLogf(f string, args ...any) { rl.lines = append(rl.lines, "D:"+f) }
func (rl *recordingLogger) InfoLogf(f string, args ...any)  { rl.lines = append(rl.lines, "I:"+f) }
func (rl *recordingLogger) WarnLogf(f string, args ...any)  { rl.lines = append(rl.lines, "W:"+f) }
func (rl *recordingLogger) ErrorLogf(f string, args ...any) { rl.lines = append(rl.lines, "E:"+f) }

func newFakeDetector(clock *fakeClock, hard, slow time.Duration) *VLLMDetector {
	tracker := NewActivityTrackerWithClock(clock.Now)
	log := EngineLogger(&recordingLogger{})
	d := NewVLLMDetector(tracker, hard, slow, time.Hour, log)
	d.now = clock.Now
	return d
}

func TestVLLMDetectorIdleNeverTriggers(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	d := newFakeDetector(clock, 45*time.Second, 120*time.Second)
	for i := 0; i < 10; i++ {
		clock.t = clock.t.Add(10 * time.Second)
		stalled, _ := d.DetectStall()
		if stalled {
			t.Fatalf("detector fired while engine idle at +%ds", i*10)
		}
	}
}

func TestVLLMDetectorFiresWhenBusyButSilentPastHardTimeout(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	d := newFakeDetector(clock, 45*time.Second, 120*time.Second)
	d.BeginTurn()
	clock.t = clock.t.Add(44 * time.Second)
	if stalled, _ := d.DetectStall(); stalled {
		t.Fatal("should not fire just under hard timeout")
	}
	clock.t = clock.t.Add(2 * time.Second)
	stalled, reason := d.DetectStall()
	if !stalled {
		t.Fatal("expected stall past hard timeout while busy")
	}
	if reason == "" {
		t.Fatal("expected non-empty reason")
	}
}

func TestVLLMDetectorSoftThresholdDoesNotFireButLogs(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	rec := &recordingLogger{}
	d := NewVLLMDetector(NewActivityTrackerWithClock(clock.Now), 300*time.Second, 120*time.Second, time.Hour, rec)
	d.now = clock.Now
	d.BeginTurn()
	clock.t = clock.t.Add(119 * time.Second)
	if stalled, _ := d.DetectStall(); stalled {
		t.Fatal("must not fire below slow threshold")
	}
	if len(rec.lines) != 0 {
		t.Fatalf("unexpected early warning: %v", rec.lines)
	}
	clock.t = clock.t.Add(1 * time.Second)
	if stalled, _ := d.DetectStall(); stalled {
		t.Fatal("soft threshold must not trigger recovery")
	}
	if len(rec.lines) == 0 {
		t.Fatal("expected a warning log for slow-but-not-yet-hung turn")
	}
}

func TestVLLMDetectorNormalTurnFlow(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	d := newFakeDetector(clock, 45*time.Second, 120*time.Second)
	d.BeginTurn()
	clock.t = clock.t.Add(10 * time.Second)
	d.MarkActivity()
	clock.t = clock.t.Add(10 * time.Second)
	d.EndTurn()
	clock.t = clock.t.Add(60 * time.Second)
	if stalled, _ := d.DetectStall(); stalled {
		t.Fatal("ended turn must not count as stall")
	}
}

func TestVLLMDetectorMultipleWarningsRespectCooldown(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)}
	rec := &recordingLogger{}
	d := NewVLLMDetector(NewActivityTrackerWithClock(clock.Now), 45*time.Second, 120*time.Second, 5*time.Minute, rec)
	d.now = clock.Now
	d.BeginTurn()
	clock.t = clock.t.Add(130 * time.Second)
	stalled, _ := d.DetectStall()
	if !stalled {
		t.Fatal("expected hard-timeout stall")
	}
	firstCount := len(rec.lines)
	if firstCount == 0 {
		t.Fatal("first slow-warning missing")
	}
	clock.t = clock.t.Add(30 * time.Second)
	d.DetectStall()
	if len(rec.lines) != firstCount {
		t.Fatalf("cooldown violated: extra warning within window (%d total)", len(rec.lines))
	}
	clock.t = clock.t.Add(4*time.Minute + 10*time.Second)
	d.DetectStall()
	if len(rec.lines) != firstCount {
		t.Fatalf("cooldown violated near boundary: (%d total)", len(rec.lines))
	}
	clock.t = clock.t.Add(20 * time.Second)
	d.DetectStall()
	if len(rec.lines) != firstCount+1 {
		t.Fatalf("expected exactly one more warning after cooldown expiry, got %d", len(rec.lines))
	}
}
