package buildinfo

import (
	"strings"
	"testing"
)

func TestHumanReadable_RFC3339(t *testing.T) {
	BuildTime = "2026-07-31T09:15:00Z"
	got := HumanReadable()
	if got == "2026-07-31T09:15:00Z" || !strings.Contains(got, "2026-07-31") {
		t.Errorf("expected formatted local time, got %q", got)
	}
}

func TestHumanReadable_DefaultUnknown(t *testing.T) {
	BuildTime = "unknown"
	if got := HumanReadable(); got != "unknown" {
		t.Errorf("expected 'unknown', got %q", got)
	}
}

func TestHumanReadable_Invalid(t *testing.T) {
	BuildTime = "not-a-date"
	if got := HumanReadable(); got != "not-a-date" {
		t.Errorf("expected raw string on parse error, got %q", got)
	}
}
