package engine

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
)

type recordLogger struct {
	mu    sync.Mutex
	lines []string
}

func (r *recordLogger) line(kind, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, kind+" "+msg)
}

func (r *recordLogger) DebugLog(msg string, args ...any) { r.line("DBG", fmt.Sprintf(msg, args...)) }
func (r *recordLogger) InfoLog(msg string, args ...any)  { r.line("INF", fmt.Sprintf(msg, args...)) }
func (r *recordLogger) WarnLog(msg string, args ...any)  { r.line("WRN", fmt.Sprintf(msg, args...)) }
func (r *recordLogger) ErrorLog(msg string, args ...any) { r.line("ERR", fmt.Sprintf(msg, args...)) }

func (r *recordLogger) DebugLogf(format string, args ...any) { r.line("DBG", fmt.Sprintf(format, args...)) }
func (r *recordLogger) InfoLogf(format string, args ...any)  { r.line("INF", fmt.Sprintf(format, args...)) }
func (r *recordLogger) WarnLogf(format string, args ...any)  { r.line("WRN", fmt.Sprintf(format, args...)) }
func (r *recordLogger) ErrorLogf(format string, args ...any) { r.line("ERR", fmt.Sprintf(format, args...)) }

func (r *recordLogger) contains(sub string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

type notifierRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (n *notifierRecorder) fn() NotifyFunc {
	return func(status string) {
		n.mu.Lock()
		defer n.mu.Unlock()
		n.calls = append(n.calls, status)
	}
}

func (n *notifierRecorder) snapshot() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]string, len(n.calls))
	copy(out, n.calls)
	return out
}

type fakeSource struct {
	current string
	models  map[string]modelsconfig.ModelEntry
}

func (fs *fakeSource) CurrentAlias() string { return fs.current }
func (fs *fakeSource) Entry(alias string) (modelsconfig.ModelEntry, bool) {
	e, ok := fs.models[alias]
	return e, ok
}

type countingRunFn struct {
	mu    sync.Mutex
	calls []string
	tail  string
	err   error
}

func (cr *countingRunFn) fn() RunFn {
	return func(script string, timeoutMs int) (string, error) {
		cr.mu.Lock()
		defer cr.mu.Unlock()
		cr.calls = append(cr.calls, script)
		return cr.tail, cr.err
	}
}

func (cr *countingRunFn) callCount() int {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return len(cr.calls)
}

type flakyProbe struct {
	mu    sync.Mutex
	fails int
	calls int
}

func (fp *flakyProbe) fn() ProbeFn {
	return func(url string, modelSubstr string) (bool, error) {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		fp.calls++
		if fp.calls <= fp.fails {
			return false, fmt.Errorf("connection refused (probe %d)", fp.calls)
		}
		return true, nil
	}
}

func (fp *flakyProbe) callCount() int {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.calls
}

type scriptedDetector struct {
	stall  bool
	reason string
}

func (sd *scriptedDetector) DetectStall() (bool, string) { return sd.stall, sd.reason }

type blockingProbe struct{ released chan struct{} }

func (bp *blockingProbe) fn() ProbeFn {
	return func(url string, modelSubstr string) (bool, error) {
		<-bp.released
		return true, nil
	}
}

func requireContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("needle %q not found in %q", needle, haystack)
	}
}

func newTestManager(fs *fakeSource, run RunFn, probe ProbeFn, det StallDetector) *Manager {
	m := NewManager(fs, run, probe, det)
	m.Poll = 2 * time.Millisecond
	m.WaitTick = 2 * time.Millisecond
	m.NotifyThrottle = 30 * time.Millisecond
	m.Failures = 3
	m.HealthInterval = 10 * time.Millisecond
	return m
}
