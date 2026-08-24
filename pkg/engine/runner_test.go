package engine

import (
	"errors"
	"os/user"
	"testing"
	"time"
)

func TestRunScriptSuccess(t *testing.T) {
	log := &recordLogger{}
	run := func(script string, timeoutMs int) (string, error) { return "ok-out", nil }
	r := NewRunner(run, log)

	res := r.RunScript("/opt/start.sh", 5000)
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if res.Tail != "ok-out" {
		t.Errorf("tail = %q, want ok-out", res.Tail)
	}
	if !log.contains("run script: /opt/start.sh") {
		t.Errorf("expected run-script log line, got %v", log.lines)
	}
	if !log.contains("finished script: /opt/start.sh") {
		t.Errorf("expected finished-script log line, got %v", log.lines)
	}
}

func TestRunScriptFailureCapturedInLog(t *testing.T) {
	log := &recordLogger{}
	wantErr := errors.New("boom")
	run := func(script string, timeoutMs int) (string, error) { return "partial-output", wantErr }
	r := NewRunner(run, log)

	res := r.RunScript("/opt/start.sh", 5000)
	if res.Err == nil {
		t.Fatal("expected error, got nil")
	}
	if res.Tail != "partial-output" {
		t.Errorf("tail = %q, want partial-output", res.Tail)
	}
	if !log.contains("[ENGINE] script /opt/start.sh failed") {
		t.Errorf("expected ErrorLogf failure line, got %v", log.lines)
	}
	if !log.contains("partial-output") {
		t.Errorf("expected tail in failure log, got %v", log.lines)
	}
}

func TestRunScriptEmptyScriptIsNoop(t *testing.T) {
	log := &recordLogger{}
	ran := false
	run := func(script string, timeoutMs int) (string, error) { ran = true; return "", nil }
	r := NewRunner(run, log)

	res := r.RunScript("", 5000)
	if ran {
		t.Error("RunFn was invoked for empty script")
	}
	if res.Err != nil || res.Tail != "" {
		t.Errorf("expected empty result, got %+v", res)
	}
	if len(log.lines) != 0 {
		t.Errorf("expected no log lines for noop, got %v", log.lines)
	}
}

func TestRealRunFnHappyPathEcho(t *testing.T) {
	tail, err := RealRunFn("echo hi", 10000)
	if err != nil {
		t.Fatalf("RealRunFn: %v", err)
	}
	requireContains(t, tail, "hi")
}

func TestRealRunFnNonzeroExitSurfacesError(t *testing.T) {
	_, err := RealRunFn("exit 3", 10000)
	if err == nil {
		t.Fatal("expected error for nonzero exit")
	}
}

func TestRealRunHonorsVeryShortTimeout(t *testing.T) {
	u, lookupErr := user.Current()
	if lookupErr == nil && u.Username == "root" {
		t.Skip("skipping: deterministic timeout test unreliable as root")
	}
	start := time.Now()
	_, err := RealRunFn("sleep 5", 50)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error for sleeping script under 50ms timeout")
	}
	if elapsed >= 2*time.Second {
		t.Errorf("timeout not honored quickly: took %v", elapsed)
	}
}
