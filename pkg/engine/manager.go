package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
)

type Source interface {
	CurrentAlias() string
	Entry(alias string) (modelsconfig.ModelEntry, bool)
}

type TransitionPlan struct {
	Alias       string
	StopScript  string
	StartScript string
	ReadyURL    string
	ModelSubstr string
	Deadline    time.Duration
}

func NeedsTransition(oldAlias, newAlias, newStartScript string) bool {
	return newAlias != oldAlias && newStartScript != ""
}

type Manager struct {
	source   Source
	Runner   *Runner
	Prober   *Prober
	Detector StallDetector
	Log      EngineLogger

	Poll           time.Duration
	WaitTick       time.Duration
	NotifyThrottle time.Duration
	Failures       int
	HealthInterval time.Duration

	recoverMu sync.Mutex
}

func NewManager(source Source, run RunFn, probe ProbeFn, det StallDetector) *Manager {
	if det == nil {
		det = NoStallDetector{}
	}
	pr := NewProber(nil)
	if probe != nil {
		pr.Probe = probe
	}
	return &Manager{
		source:         source,
		Runner:         NewRunner(realRunOrDefault(run), nil),
		Prober:         pr,
		Detector:       det,
		Poll:           ProbeInterval,
		NotifyThrottle: NotifyEvery,
		Failures:       FailuresBeforeAlert,
		HealthInterval: HealthProbeInterval,
	}
}

func (m *Manager) WithLog(log EngineLogger) *Manager {
	m.Log = log
	m.Runner.Log = log
	return m
}

func (m *Manager) PlanTransition(newAlias string) (TransitionPlan, bool) {
	plan := m.planFor(m.source.CurrentAlias(), newAlias)
	return plan, NeedsTransition(m.source.CurrentAlias(), newAlias, plan.StartScript)
}

func (m *Manager) ShouldTransition(newAlias string) bool {
	plan, ok := m.PlanTransition(newAlias)
	return ok && (plan.StopScript != "" || plan.StartScript != "")
}

func (m *Manager) Transition(ctx context.Context, newAlias string, notify NotifyFunc) error {
	return m.TransitionBetween(ctx, m.source.CurrentAlias(), newAlias, notify)
}

func (m *Manager) TransitionBetween(ctx context.Context, oldAlias, newAlias string, notify NotifyFunc) error {
	plan, ok := m.plannableBetween(oldAlias, newAlias)
	if !ok {
		return nil
	}
	if err := m.stopOldEngine(plan, notify); err != nil {
		return err
	}
	if err := m.launchNewEngine(ctx, plan, notify); err != nil {
		return err
	}
	return m.WaitForReady(ctx, plan, notify)
}

func (m *Manager) plannableBetween(oldAlias, newAlias string) (TransitionPlan, bool) {
	plan := m.planFor(oldAlias, newAlias)
	return plan, NeedsTransition(oldAlias, newAlias, plan.StartScript)
}

func (m *Manager) planFor(oldAlias, newAlias string) TransitionPlan {
	newEntry, ok := m.source.Entry(newAlias)
	if !ok {
		return TransitionPlan{}
	}
	plan := TransitionPlan{
		Alias:       newAlias,
		StartScript: newEntry.StartScript,
		ReadyURL:    ReadyURLFor(newEntry.Host),
		ModelSubstr: newEntry.Name,
		Deadline:    deadlineForType(newEntry.Type),
	}
	if newAlias != oldAlias {
		if oldEntry, okOld := m.source.Entry(oldAlias); okOld && oldEntry.StopScript != "" {
			plan.StopScript = oldEntry.StopScript
		}
	}
	return plan
}

func (m *Manager) stopOldEngine(plan TransitionPlan, notify NotifyFunc) error {
	if plan.StopScript == "" {
		return nil
	}
	progress(notify, "Stopping previous engine…")
	result := m.Runner.RunScript(plan.StopScript, StopTimeout)
	if result.Err != nil {
		m.warn("previous engine did not stop cleanly (continuing): %v", result.Err)
	}
	return nil
}

func (m *Manager) launchNewEngine(ctx context.Context, plan TransitionPlan, notify NotifyFunc) error {
	if plan.StartScript == "" {
		return nil
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("preparing engine for %s: %w", plan.Alias, ctx.Err())
	default:
	}
	progress(notify, "Starting engine…")
	result := m.Runner.RunScript(plan.StartScript, LaunchTimeout)
	if result.Err != nil {
		return fmt.Errorf("launching engine for %s: %w", plan.Alias, result.Err)
	}
	return nil
}

type waitSettings struct {
	poll     time.Duration
	deadline time.Duration
	throttle time.Duration
	probe    ProbeFn
}

func (m *Manager) waitSettings(plan TransitionPlan) waitSettings {
	return waitSettings{
		poll:     positiveOr(m.Poll, ProbeInterval),
		deadline: positiveOr(plan.Deadline, DefaultReadyDeadline),
		throttle: positiveOr(m.NotifyThrottle, NotifyEvery),
		probe:    m.probeFn(),
	}
}

func (m *Manager) WaitForReady(ctx context.Context, plan TransitionPlan, notify NotifyFunc) error {
	if plan.ReadyURL == "" {
		progress(notify, StatusEngineReady)
		return nil
	}
	s := m.waitSettings(plan)
	deadlineAt := time.Now().Add(s.deadline)
	timer := time.NewTimer(capToRemaining(s.poll, deadlineAt))
	defer timer.Stop()
	start := time.Now()
	lastNotice := start
	lastProbeErr := ""
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", plan.Alias, ctx.Err())
		case <-timer.C:
		}
		if ready := m.performProbe(s, plan, &lastProbeErr, &lastNotice, notify); ready {
			progress(notify, StatusEngineReady)
			return nil
		}
		if time.Now().After(deadlineAt) {
			return m.deadlineError(plan, s, lastProbeErr)
		}
		timer.Reset(capToRemaining(s.poll, deadlineAt))
	}
}

func (m *Manager) performProbe(s waitSettings, plan TransitionPlan, lastProbeErr *string,
	lastNotice *time.Time, notify NotifyFunc) bool {
	ok, err := s.probe(plan.ReadyURL, plan.ModelSubstr)
	if err != nil {
		*lastProbeErr = err.Error()
	} else if ok {
		return true
	}
	if time.Since(*lastNotice) >= s.throttle {
		m.progressWaiting(notify)
		*lastNotice = time.Now()
	}
	return false
}

func (m *Manager) progressWaiting(notify NotifyFunc) {
	msg := "Still waiting for engine…"
	if notify != nil {
		notify(msg)
	}
	if m.Log != nil {
		m.Log.InfoLogf("%s", msg)
	}
}

func (m *Manager) deadlineError(plan TransitionPlan, s waitSettings, lastProbeErr string) error {
	return fmt.Errorf("engine for %s not ready after %s (last probe error: %s)",
		plan.Alias, s.deadline, lastProbeErr)
}

func capToRemaining(delay time.Duration, deadlineAt time.Time) time.Duration {
	remaining := time.Until(deadlineAt)
	if remaining <= 0 {
		return time.Millisecond
	}
	if delay > remaining {
		return remaining
	}
	if delay <= 0 {
		return time.Millisecond
	}
	return delay
}

func (m *Manager) StartWatchdog(ctx context.Context, notify NotifyFunc) context.CancelFunc {
	watchCtx, cancel := context.WithCancel(ctx)
	go m.runWatchdog(watchCtx, notify)
	return cancel
}

func (m *Manager) runWatchdog(ctx context.Context, notify NotifyFunc) {
	interval := positiveOr(m.HealthInterval, HealthProbeInterval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	consecutives := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if stalled, reason := m.detectStallNow(); stalled {
			consecutives = 0
			m.attemptRecovery(ctx, notify, reason)
			continue
		}
		ok, err := m.probeActiveEngine()
		if ok {
			consecutives = 0
			continue
		}
		if err != nil {
			m.debug("watchdog probe degraded: %v", err)
		}
		consecutives++
		if consecutives < m.failureThreshold() {
			continue
		}
		consecutives = 0
		m.attemptRecovery(ctx, notify, "repeated health-probe failures")
	}
}

func (m *Manager) failureThreshold() int {
	if m.Failures > 0 {
		return m.Failures
	}
	return FailuresBeforeAlert
}

func (m *Manager) detectStallNow() (bool, string) {
	if m.Detector == nil {
		return false, ""
	}
	return m.Detector.DetectStall()
}

func (m *Manager) probeActiveEngine() (bool, error) {
	alias := m.source.CurrentAlias()
	entry, ok := m.source.Entry(alias)
	if !ok || entry.Host == "" {
		return true, nil
	}
	return m.probeFn()(ReadyURLFor(entry.Host), entry.Name)
}

func (m *Manager) attemptRecovery(ctx context.Context, notify NotifyFunc, cause string) {
	m.recoverMu.Lock()
	defer m.recoverMu.Unlock()
	alias := m.source.CurrentAlias()
	entry, ok := m.source.Entry(alias)
	if !ok || entry.StartScript == "" {
		m.debug("watchdog: no start script for %s; skipping recovery", alias)
		progress(notify, "Engine watchdog: recovery impossible (no start script); manual intervention required.")
		return
	}
	m.warn("active engine unhealthy (%s); restarting %s", cause, alias)
	progress(notify, "Engine appears stalled — attempting automatic restart…")
	if err := m.TransitionBetween(ctx, "", alias, notify); err != nil {
		m.warn("automatic engine recovery failed: %v", err)
		progress(notify, "Automatic engine recovery failed; please intervene manually.")
		return
	}
	progress(notify, StatusEngineReady)
}

func (m *Manager) probeFn() ProbeFn {
	if m.Prober != nil && m.Prober.Probe != nil {
		return m.Prober.Probe
	}
	return ClientProbe
}

func (m *Manager) warn(format string, args ...any) {
	if m.Log != nil {
		m.Log.WarnLogf(format, args...)
	}
}

func (m *Manager) debug(format string, args ...any) {
	if m.Log != nil {
		m.Log.DebugLogf(format, args...)
	}
}

func progress(notify NotifyFunc, status string) {
	if notify != nil {
		notify(status)
	}
}

func realRunOrDefault(fn RunFn) RunFn {
	if fn == nil {
		return RealRunFn
	}
	return fn
}

func positiveOr(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func ReadyURLFor(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	return host + "/v1/models"
}

func deadlineForType(engineType string) time.Duration {
	if d, ok := ReadyDeadlineByType[engineType]; ok {
		return d
	}
	return DefaultReadyDeadline
}

