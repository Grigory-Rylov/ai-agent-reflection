package engine

import "time"

const (
	StatusEngineReady = "Engine ready."

	ProbeInterval = 10 * time.Second
	HealthProbeInterval = 30 * time.Second
	FailuresBeforeAlert = 3
	StopTimeout = 30 * time.Second
	LaunchTimeout = 60 * time.Second
	NotifyEvery = time.Minute
	DefaultTailBytes = 4 << 10
	DefaultReadyDeadline = 10 * time.Minute

	VLLMActivityHardTimeout = 45 * time.Second
	VLLMActivitySlowThreshold = 120 * time.Second
)

var ReadyDeadlineByType = map[string]time.Duration{
	"vllm":  20 * time.Minute,
	"llama": 10 * time.Minute,
}

