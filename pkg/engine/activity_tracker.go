package engine

import (
	"fmt"
	"sync"
	"time"
)

type ActivityTracker struct {
	mu             sync.Mutex
	lastTouch      time.Time
	turnInProgress bool
	turnStartedAt  time.Time
	now            func() time.Time
}

func NewActivityTracker() *ActivityTracker {
	return &ActivityTracker{lastTouch: time.Now(), now: time.Now}
}

func NewActivityTrackerWithClock(now func() time.Time) *ActivityTracker {
	if now == nil {
		now = time.Now
	}
	return &ActivityTracker{lastTouch: now(), now: now}
}

func (at *ActivityTracker) BeginTurn() {
	at.mu.Lock()
	defer at.mu.Unlock()
	now := at.clockNow()
	at.turnInProgress = true
	at.turnStartedAt = now
	at.lastTouch = now
}

func (at *ActivityTracker) EndTurn() {
	at.mu.Lock()
	defer at.mu.Unlock()
	at.turnInProgress = false
	at.lastTouch = at.clockNow()
}

func (at *ActivityTracker) Touch() {
	at.mu.Lock()
	defer at.mu.Unlock()
	at.lastTouch = at.clockNow()
}

func (at *ActivityTracker) Snapshot() (bool, time.Duration, time.Duration) {
	at.mu.Lock()
	defer at.mu.Unlock()
	now := at.clockNow()
	idleSinceLastTouch := now.Sub(at.lastTouch)
	var turnAge time.Duration
	if at.turnInProgress {
		turnAge = now.Sub(at.turnStartedAt)
	}
	return at.turnInProgress, idleSinceLastTouch, turnAge
}

func (at *ActivityTracker) clockNow() time.Time {
	if at.now != nil {
		return at.now()
	}
	return time.Now()
}

type VLLMDetector struct {
	tracker       *ActivityTracker
	hardTimeout   time.Duration
	slowThreshold time.Duration
	warnCooldown  time.Duration
	log           EngineLogger
	now           func() time.Time

	mu           sync.Mutex
	lastSlowWarn time.Time
}

func NewVLLMDetector(tracker *ActivityTracker, hardTimeout, slowThreshold, warnCooldown time.Duration, log EngineLogger) *VLLMDetector {
	if tracker == nil {
		tracker = NewActivityTracker()
	}
	if hardTimeout <= 0 {
		hardTimeout = VLLMActivityHardTimeout
	}
	if slowThreshold <= 0 {
		slowThreshold = VLLMActivitySlowThreshold
	}
	if warnCooldown <= 0 {
		warnCooldown = NotifyEvery
	}
	return &VLLMDetector{
		tracker:       tracker,
		hardTimeout:   hardTimeout,
		slowThreshold: slowThreshold,
		warnCooldown:  warnCooldown,
		log:           log,
		now:           time.Now,
	}
}

func (d *VLLMDetector) MarkActivity() {
	d.tracker.Touch()
}

func (d *VLLMDetector) BeginTurn() {
	d.tracker.BeginTurn()
}

func (d *VLLMDetector) EndTurn() {
	d.tracker.EndTurn()
}

func (d *VLLMDetector) DetectStall() (bool, string) {
	busy, idle, turnAge := d.tracker.Snapshot()
	if !busy {
		return false, ""
	}
	if idle >= d.hardTimeout {
		d.noteSlowWarning(idle, turnAge)
		return true, fmt.Sprintf("vllm turn active %s, no engine activity for %s (hard threshold %s)",
			shortDur(turnAge), shortDur(idle), shortDur(d.hardTimeout))
	}
	if idle >= d.slowThreshold {
		d.noteSlowWarning(idle, turnAge)
	}
	return false, ""
}

func (d *VLLMDetector) noteSlowWarning(idle, turnAge time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.detectorNow()
	if !d.lastSlowWarn.IsZero() && now.Sub(d.lastSlowWarn) < d.warnCooldown {
		return
	}
	d.lastSlowWarn = now
	if d.log != nil {
		d.log.WarnLogf("[ENGINE][WATCHDOG] vllm: no activity for %s mid-turn (age %s, hard threshold %s) — probable stall",
			shortDur(idle), shortDur(turnAge), shortDur(d.hardTimeout))
	}
}

func (d *VLLMDetector) detectorNow() time.Time {
	if d.now != nil {
		return d.now()
	}
	return time.Now()
}

func shortDur(d time.Duration) string {
	r := d.Round(time.Second)
	if r < time.Second {
		return "<1s"
	}
	return r.String()
}
