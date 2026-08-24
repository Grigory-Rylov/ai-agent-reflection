package engine

import "context"

type NotifyFunc func(status string)

type EngineLogger interface {
	DebugLog(msg string, args ...any)
	InfoLog(msg string, args ...any)
	WarnLog(msg string, args ...any)
	ErrorLog(msg string, args ...any)
	DebugLogf(format string, args ...any)
	InfoLogf(format string, args ...any)
	WarnLogf(format string, args ...any)
	ErrorLogf(format string, args ...any)
}

type RunFn func(script string, timeoutMs int) (tail string, err error)

type ProbeFn func(url string, modelSubstr string) (bool, error)

type StallDetector interface {
	DetectStall() (stalled bool, reason string)
}

type NoStallDetector struct{}

func (NoStallDetector) DetectStall() (bool, string) { return false, "" }

type Control interface {
	Transition(ctx context.Context, alias string, notify NotifyFunc) error
	ShouldTransition(alias string) bool
	StartWatchdog(ctx context.Context, notify NotifyFunc) context.CancelFunc
}

