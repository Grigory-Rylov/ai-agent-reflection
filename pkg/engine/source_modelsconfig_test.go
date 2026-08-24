package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
)

func TestHolderSourceReflectsHolder(t *testing.T) {
	h := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "a",
		Models: map[string]modelsconfig.ModelEntry{
			"a": {Name: "na", Host: "h:1"},
			"b": {Name: "nb", Host: "h:2"},
		},
	})
	src := &HolderSource{H: h}
	if src.CurrentAlias() != "a" {
		t.Errorf("CurrentAlias = %q, want a", src.CurrentAlias())
	}
	e, ok := src.Entry("b")
	if !ok || e.Name != "nb" {
		t.Errorf("Entry(b) = %+v, %v", e, ok)
	}
	if _, ok := src.Entry("none"); ok {
		t.Error("Entry(none) reported present for missing alias")
	}

	nilSrc := &HolderSource{}
	if nilSrc.CurrentAlias() != "" {
		t.Errorf("nil-holder CurrentAlias = %q, want empty", nilSrc.CurrentAlias())
	}
	if _, ok := nilSrc.Entry("a"); ok {
		t.Error("nil-holder Entry reported present")
	}
}

func TestNewControlFromHolderWiresDefaultsAndLog(t *testing.T) {
	h := modelsconfig.NewTestHolder(&modelsconfig.ModelsConfig{
		Default: "a",
		Models: map[string]modelsconfig.ModelEntry{
			"a": {Name: "na", Host: "h:1", StartScript: "/opt/a.sh"},
			"b": {Name: "nb", Host: "h:2", StartScript: "/opt/b.sh"},
		},
	})
	log := &recordLogger{}
	var ctl Control = NewControlFromHolder(h, nil, nil, nil, log)
	mgr, ok := ctl.(*Manager)
	if !ok {
		t.Fatalf("NewControlFromHolder returned %T, want *Manager", ctl)
	}
	if mgr.Runner == nil || mgr.Prober == nil || mgr.Detector == nil {
		t.Error("production defaults not applied (Runner/Prober/Detector)")
	}
	if mgr.Log == nil {
		t.Error("log not wired onto manager")
	}

	rec := &notifierRecorder{}
	mgr.Poll = 2 * time.Millisecond
	mgr.NotifyThrottle = 30 * time.Millisecond
	mgr.Prober.Probe = func(url, sub string) (bool, error) { return true, nil }
	mgr.Runner.Run = func(script string, timeoutMs int) (string, error) { return "ok", nil }
	if err := ctl.Transition(context.Background(), "b", rec.fn()); err != nil {
		t.Fatalf("transition through holder-backed control: %v", err)
	}
	if !log.contains("run script: /opt/b.sh") {
		t.Errorf("holder-backed transition did not flow through wired log: %v", log.lines)
	}
}
