package vk

import (
	"strings"
	"testing"

	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/bmc"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/gpu"
	"github.com/Grigory-Rylov/ai-agent-reflection/pkg/logger"
)

func TestStatusGPUBlockShape(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	handler := NewBotHandler(nil, newMockAgentLoop(), log)

	block := handler.statusGPU()
	if block == "" {
		t.Skip("no GPU data available on this host")
	}

	if !strings.HasPrefix(block, "🎮 GPU ") {
		t.Errorf("GPU block should start with header, got %q", block)
	}
	if !strings.Contains(block, "Driver: ") || !strings.Contains(block, "CUDA: ") {
		t.Errorf("GPU block missing driver/cuda line: %q", block)
	}
	if !strings.Contains(block, "MiB (") {
		t.Errorf("GPU block missing memory usage line: %q", block)
	}
}

func TestStatusGPUBlockEmptyWithoutNvidiaSMI(t *testing.T) {
	if gpu.Available() {
		t.Skip("nvidia-smi present on this host")
	}

	log, _ := logger.New(logger.DefaultConfig())
	handler := NewBotHandler(nil, newMockAgentLoop(), log)

	if block := handler.statusGPU(); block != "" {
		t.Errorf("expected empty GPU block without nvidia-smi, got %q", block)
	}
}

func TestStatusAppendsGPUBlockLast(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	handler := NewBotHandler(nil, newMockAgentLoop(), log)

	status := handler.ProcessMessage("/status", 12345)
	index := strings.Index(status, "🎮 GPU")
	if index < 0 {
		t.Skip("no GPU data appended on this host")
	}

	if strings.Count(status, "🎮 GPU") != 1 {
		t.Errorf("status should contain exactly one GPU header:\n%s", status)
	}
	if strings.Contains(status[index:], "Режим:") {
		t.Errorf("GPU block must be after mode section:\n%s", status)
	}
}

func TestStatusBMCBlockShape(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	handler := NewBotHandler(nil, newMockAgentLoop(), log)

	block := handler.statusBMC()
	if block == "" {
		t.Skip("no BMC data available on this host")
	}

	if !strings.HasPrefix(block, "🌡 BMC") {
		t.Errorf("BMC block should start with header, got %q", block)
	}
}

func TestStatusBMCBlockEmptyWithoutIPMITool(t *testing.T) {
	if bmc.Available() {
		t.Skip("ipmitool present on this host")
	}

	log, _ := logger.New(logger.DefaultConfig())
	handler := NewBotHandler(nil, newMockAgentLoop(), log)

	if block := handler.statusBMC(); block != "" {
		t.Errorf("expected empty BMC block without ipmitool, got %q", block)
	}
}

func TestStatusBMCBlockLast(t *testing.T) {
	log, _ := logger.New(logger.DefaultConfig())
	handler := NewBotHandler(nil, newMockAgentLoop(), log)

	status := handler.ProcessMessage("/status", 12345)
	index := strings.Index(status, "🌡 BMC")
	if index < 0 {
		t.Skip("no BMC data appended on this host")
	}

	if strings.Count(status, "🌡 BMC") != 1 {
		t.Errorf("status should contain exactly one BMC header:\n%s", status)
	}
	if strings.Contains(status[index:], "Режим:") {
		t.Errorf("BMC block must be the last section of /status:\n%s", status)
	}
}
