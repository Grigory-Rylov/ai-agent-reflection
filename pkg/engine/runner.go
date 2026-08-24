package engine

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

type ScriptResult struct {
	Tail string
	Err  error
}

type Runner struct {
	Run      RunFn
	Log      EngineLogger
	TailSize int
}

func NewRunner(run RunFn, log EngineLogger) *Runner {
	return &Runner{Run: run, Log: log, TailSize: DefaultTailBytes}
}

func RealRunFn(script string, timeoutMs int) (string, error) {
	timeout := time.Duration(timeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = LaunchTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-lc", script)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	tail, trimErr := tailOf(buf.Bytes(), DefaultTailBytes)
	if trimErr != nil {
		tail = buf.String()
	}
	if ctx.Err() == context.DeadlineExceeded {
		return tail, fmt.Errorf("running %s: timed out after %s", script, timeout)
	}
	return tail, runErr
}

func (r *Runner) RunScript(script string, timeout time.Duration) ScriptResult {
	if script == "" {
		return ScriptResult{}
	}
	ms := int(timeout / time.Millisecond)
	if ms <= 0 {
		ms = 1
	}
	logAction(r.Log, "run script", script, timeout)
	tail, err := r.Run(script, ms)
	if err != nil {
		logScriptFailure(r.Log, script, tail, err)
		return ScriptResult{Tail: tail, Err: err}
	}
	logAction(r.Log, "finished script", script, timeout)
	return ScriptResult{Tail: tail}
}

func logAction(log EngineLogger, kind, script string, timeout time.Duration) {
	if log != nil {
		log.InfoLogf("[ENGINE] %s: %s (timeout=%s)", kind, script, timeout)
	}
}

func logScriptFailure(log EngineLogger, script, tail string, err error) {
	if log == nil {
		return
	}
	log.ErrorLogf("[ENGINE] script %s failed: %v", script, err)
	if tail != "" {
		log.ErrorLogf("[ENGINE] script %s output tail:\n%s", script, tail)
	}
}

func tailOf(b []byte, max int) (string, error) {
	limit := max
	if limit <= 0 {
		limit = DefaultTailBytes
	}
	if len(b) > limit {
		b = b[len(b)-limit:]
		nl := bytes.LastIndexByte(b, '\n')
		if nl >= 0 && nl < len(b)-1 {
			b = b[nl+1:]
		}
	}
	out := string(b)
	if out != string([]byte(out)) {
		return "", fmt.Errorf("captured output is not valid utf-8")
	}
	return out, nil
}

