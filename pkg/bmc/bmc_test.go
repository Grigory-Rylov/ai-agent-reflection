package bmc

import (
	"strings"
	"testing"
)

const sampleSDR = `CPU_Tctl_Value   | 01h | ok  |  3.1 | 49 degrees C
P0_UMC0_CH_A0    | 10h | ok  |  8.0 | 48 degrees C
P0_UMC1_CH_B0    | 11h | ok  |  8.1 | 51 degrees C
CPU_MOSFET       | 20h | ok  |  7.0 | 43 degrees C
DIMM_MOSFET_1    | 21h | ok  |  7.0 | 47 degrees C
SYS_Air_Inlet    | 30h | ns  | 55.0 | No Reading
SYS_Air_Outlet   | 31h | ok  |  7.0 | 40 degrees C
MB_Air_Inlet     | 32h | ok  |  7.0 | 43 degrees C
PSU0_Temp        | 70h | ns  | 10.1 | Disabled
`

func TestParseSDR(t *testing.T) {
	info, err := ParseSDR(sampleSDR)
	if err != nil {
		t.Fatalf("ParseSDR: %v", err)
	}
	if len(info.Sensors) != 9 {
		t.Errorf("sensors = %d, want 9", len(info.Sensors))
	}
	s := info.Sensor("MB_Air_Inlet")
	if s == nil {
		t.Fatal("MB_Air_Inlet not found")
	}
	if s.Value != "43 degrees C" {
		t.Errorf("MB_Air_Inlet value = %q", s.Value)
	}
	if s.Status != "ok" {
		t.Errorf("MB_Air_Inlet status = %q", s.Status)
	}
	if info.Sensor("Nope") != nil {
		t.Error("expected nil for unknown sensor")
	}
}

func TestParseSDREmpty(t *testing.T) {
	if _, err := ParseSDR("no sensors here"); err == nil {
		t.Error("expected error for empty output")
	}
}

func TestFormat(t *testing.T) {
	info, err := ParseSDR(sampleSDR)
	if err != nil {
		t.Fatalf("ParseSDR: %v", err)
	}
	out := Format(info)
	for _, want := range []string{
		"MB_Air_Inlet: 43C",
		"SYS_Air_Outlet: 40C",
		"CPU_Tctl: 49C",
		"MOSFET: 43–47C",
		"UMC: 48–51C",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Format output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatNil(t *testing.T) {
	if out := Format(nil); out != "" {
		t.Errorf("Format(nil) = %q, want empty", out)
	}
}
