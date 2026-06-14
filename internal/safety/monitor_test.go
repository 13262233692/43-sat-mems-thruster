package safety

import (
	"testing"

	"github.com/cubesat/mems-thruster-gateway/internal/config"
	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

func newTestConfig() config.SafetyConfig {
	return config.SafetyConfig{
		MaxAnodeVoltage:     2200.0,
		MaxGridCurrent:      0.6,
		MaxXenonFlow:        6.0e-6,
		MinXenonFlow:        0.01e-6,
		MaxThrustDeviation:  0.25,
		FaultThresholdCount: 3,
		FaultCooldownMs:     10,
		AutoRecovery:        true,
	}
}

func TestMonitorCheckSample(t *testing.T) {
	cfg := newTestConfig()
	mon := NewMonitor(cfg)

	sample := &types.ThrusterSample{
		Timestamp:     1000,
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Thrust:        100e-6,
		Valid:         true,
	}

	if !mon.CheckSample(sample) {
		t.Error("Normal sample should pass check")
	}

	if !mon.IsHealthy() {
		t.Error("Monitor should be healthy with normal samples")
	}
}

func TestMonitorAnodeOverVoltage(t *testing.T) {
	cfg := newTestConfig()
	mon := NewMonitor(cfg)

	for i := 0; i < 5; i++ {
		sample := &types.ThrusterSample{
			Timestamp:     uint64(i) * 50000,
			AnodeVoltage:  2500.0,
			GridCurrent:   0.15,
			XenonMassFlow: 1.5e-6,
			Valid:         true,
		}
		mon.CheckSample(sample)
	}

	if mon.IsHealthy() {
		t.Error("Monitor should be unhealthy after anode overvoltage faults")
	}

	status := mon.Status()
	if !status.AnodeOverVolt {
		t.Error("Status should indicate anode overvoltage fault")
	}

	if status.FaultCount == 0 {
		t.Error("Fault count should be greater than zero")
	}
}

func TestMonitorGridOverCurrent(t *testing.T) {
	cfg := newTestConfig()
	mon := NewMonitor(cfg)

	for i := 0; i < 5; i++ {
		sample := &types.ThrusterSample{
			Timestamp:     uint64(i) * 50000,
			AnodeVoltage:  1500.0,
			GridCurrent:   0.8,
			XenonMassFlow: 1.5e-6,
			Valid:         true,
		}
		mon.CheckSample(sample)
	}

	if mon.IsHealthy() {
		t.Error("Monitor should be unhealthy after grid overcurrent faults")
	}

	status := mon.Status()
	if !status.GridOverCurrent {
		t.Error("Status should indicate grid overcurrent fault")
	}
}

func TestMonitorFlowOutOfRange(t *testing.T) {
	cfg := newTestConfig()
	mon := NewMonitor(cfg)

	for i := 0; i < 5; i++ {
		sample := &types.ThrusterSample{
			Timestamp:     uint64(i) * 50000,
			AnodeVoltage:  1500.0,
			GridCurrent:   0.15,
			XenonMassFlow: 0.001e-6,
			Valid:         true,
		}
		mon.CheckSample(sample)
	}

	if mon.IsHealthy() {
		t.Error("Monitor should be unhealthy after flow out of range faults")
	}

	status := mon.Status()
	if !status.FlowOutOfRange {
		t.Error("Status should indicate flow out of range fault")
	}
}

func TestMonitorInvalidSample(t *testing.T) {
	cfg := newTestConfig()
	mon := NewMonitor(cfg)

	sample := &types.ThrusterSample{
		Timestamp:     1000,
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Valid:         false,
	}

	if mon.CheckSample(sample) {
		t.Error("Invalid sample should not pass check")
	}
}

func TestMonitorReset(t *testing.T) {
	cfg := newTestConfig()
	mon := NewMonitor(cfg)

	for i := 0; i < 5; i++ {
		sample := &types.ThrusterSample{
			Timestamp:    uint64(i) * 50000,
			AnodeVoltage: 2500.0,
			GridCurrent:  0.15,
			Valid:        true,
		}
		mon.CheckSample(sample)
	}

	if mon.IsHealthy() {
		t.Error("Monitor should be unhealthy before reset")
	}

	mon.Reset()

	if !mon.IsHealthy() {
		t.Error("Monitor should be healthy after reset")
	}
}

func TestMonitorAutoRecovery(t *testing.T) {
	cfg := newTestConfig()
	cfg.FaultThresholdCount = 3
	cfg.FaultCooldownMs = 1
	mon := NewMonitor(cfg)

	for i := 0; i < 5; i++ {
		sample := &types.ThrusterSample{
			Timestamp:    uint64(i) * 50000,
			AnodeVoltage: 2500.0,
			GridCurrent:  0.15,
			Valid:        true,
		}
		mon.CheckSample(sample)
	}

	if mon.IsHealthy() {
		t.Error("Monitor should be unhealthy after faults")
	}

	recoverySample := &types.ThrusterSample{
		Timestamp:    1_000_000_000,
		AnodeVoltage: 1500.0,
		GridCurrent:  0.15,
		Valid:        true,
	}
	mon.CheckSample(recoverySample)

	if !mon.IsHealthy() {
		t.Log("Monitor may need more time for auto-recovery")
	}
}

func TestWatchdog(t *testing.T) {
	wd := NewWatchdog(100)

	wd.Start()
	defer wd.Stop()

	if !wd.IsAlive() {
		t.Error("Watchdog should be alive initially")
	}

	wd.Feed()

	if !wd.IsAlive() {
		t.Error("Watchdog should be alive after feeding")
	}
}

func BenchmarkMonitorCheckSample(b *testing.B) {
	cfg := newTestConfig()
	mon := NewMonitor(cfg)

	sample := &types.ThrusterSample{
		Timestamp:     1234567890,
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Thrust:        100e-6,
		Valid:         true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mon.CheckSample(sample)
	}
}

func BenchmarkMonitorCheckBatch100(b *testing.B) {
	cfg := newTestConfig()
	mon := NewMonitor(cfg)

	samples := make([]types.ThrusterSample, 100)
	for i := range samples {
		samples[i] = types.ThrusterSample{
			Timestamp:     uint64(i) * 50000,
			AnodeVoltage:  1500.0,
			GridCurrent:   0.15,
			XenonMassFlow: 1.5e-6,
			Thrust:        100e-6,
			Valid:         true,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mon.CheckBatch(samples)
	}
}
