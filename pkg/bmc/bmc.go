package bmc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	binaryName       = "ipmitool"
	ipmitoolTimeout  = 10 * time.Second
)

var (
	degreesRe = regexp.MustCompile(`(-?\d+)\s+degrees`)
)

type Sensor struct {
	Name   string
	Status string
	Value  string
}

type Info struct {
	Sensors []*Sensor
}

func (i *Info) Sensor(name string) *Sensor {
	for _, s := range i.Sensors {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (i *Info) tempValue(s *Sensor) (int, bool) {
	match := degreesRe.FindStringSubmatch(s.Value)
	if match == nil {
		return 0, false
	}
	temp, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return temp, true
}

func (i *Info) rangeOf(match func(name string) bool) (int, int, bool) {
	min, max, found := 0, 0, false
	for _, s := range i.Sensors {
		if !match(s.Name) {
			continue
		}
		temp, ok := i.tempValue(s)
		if !ok {
			continue
		}
		if !found {
			min, max = temp, temp
			found = true
			continue
		}
		if temp < min {
			min = temp
		}
		if temp > max {
			max = temp
		}
	}
	return min, max, found
}

func Available() bool {
	if _, err := exec.LookPath(binaryName); err != nil {
		return false
	}
	_, err := os.Stat("/dev/ipmi0")
	return err == nil
}

func Fetch(ctx context.Context) (*Info, error) {
	ctx, cancel := context.WithTimeout(ctx, ipmitoolTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "sudo", "-n", binaryName, "sdr", "type", "Temperature").Output()
	if err != nil {
		return nil, fmt.Errorf("running %s: %w", binaryName, err)
	}

	return ParseSDR(string(out))
}

func ParseSDR(out string) (*Info, error) {
	info := &Info{}
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if sensor := parseSensorLine(line); sensor != nil {
			info.Sensors = append(info.Sensors, sensor)
		}
	}
	if len(info.Sensors) == 0 {
		return nil, fmt.Errorf("parsing %s output: no temperature sensors found", binaryName)
	}
	return info, nil
}

func parseSensorLine(line string) *Sensor {
	parts := strings.Split(line, "|")
	if len(parts) < 5 {
		return nil
	}
	name := strings.TrimSpace(parts[0])
	status := strings.TrimSpace(parts[2])
	value := strings.TrimSpace(parts[4])
	if name == "" || status == "" || value == "" {
		return nil
	}
	return &Sensor{Name: name, Status: status, Value: value}
}

func Format(info *Info) string {
	if info == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("🌡 BMC (сервер)\n")
	for _, name := range []string{"MB_Air_Inlet", "SYS_Air_Outlet", "CPU_Tctl_Value"} {
		if line := formatSensorLine(info, name); line != "" {
			b.WriteString(line)
		}
	}
	if line := formatRangeLine(info, "MOSFET", strings.Contains); line != "" {
		b.WriteString(line)
	}
	if line := formatRangeLine(info, "UMC", strings.Contains); line != "" {
		b.WriteString(line)
	}
	return b.String()
}

func formatSensorLine(info *Info, name string) string {
	s := info.Sensor(name)
	if s == nil {
		return ""
	}
	label := strings.TrimSuffix(name, "_Value")
	if temp, ok := info.tempValue(s); ok {
		return fmt.Sprintf("  %s: %dC\n", label, temp)
	}
	return fmt.Sprintf("  %s: %s\n", label, s.Value)
}

func formatRangeLine(info *Info, label string, match func(string, string) bool) string {
	min, max, found := info.rangeOf(func(name string) bool {
		return match(name, label)
	})
	if !found {
		return ""
	}
	if min == max {
		return fmt.Sprintf("  %s: %dC\n", label, min)
	}
	return fmt.Sprintf("  %s: %d–%dC\n", label, min, max)
}
