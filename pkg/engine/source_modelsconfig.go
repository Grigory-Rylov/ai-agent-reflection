package engine

import (
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/modelsconfig"
)

type HolderSource struct {
	H *modelsconfig.Holder
}

func (hs *HolderSource) CurrentAlias() string {
	if hs.H == nil {
		return ""
	}
	return hs.H.GetDefaultAlias()
}

func (hs *HolderSource) Entry(alias string) (modelsconfig.ModelEntry, bool) {
	if hs.H == nil {
		return modelsconfig.ModelEntry{}, false
	}
	return hs.H.GetModelHost(alias)
}

func NewControlFromHolder(h *modelsconfig.Holder, run RunFn, probe ProbeFn, det StallDetector, log EngineLogger) Control {
	src := &HolderSource{H: h}
	ctrl := NewManager(src, run, probe, det)
	if log != nil {
		ctrl.WithLog(log)
	}
	return ctrl
}
