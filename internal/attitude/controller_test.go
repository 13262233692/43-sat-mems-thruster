package attitude

import (
	"testing"

	"github.com/cubesat/mems-thruster-gateway/internal/config"
	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

func TestPIDController(t *testing.T) {
	pid := NewPIDController(1.0, 0.1, 0.01, -10, 10)

	setpoint := 1.0
	measured := 0.0
	timestamp := uint64(1e9)

	output := pid.Update(setpoint, measured, timestamp)

	if output <= 0 {
		t.Error("Expected positive output when measured is below setpoint")
	}

	measured = 0.5
	output = pid.Update(setpoint, measured, 2*timestamp)
	t.Logf("PID output at t=2s: %f", output)

	measured = 1.0
	output = pid.Update(setpoint, measured, 3*timestamp)
	t.Logf("PID output at t=3s (setpoint reached): %f", output)
}

func TestPIDControllerOutputLimits(t *testing.T) {
	pid := NewPIDController(100.0, 0.0, 0.0, -5, 5)

	output := pid.Update(10.0, 0.0, 1e9)

	if output > 5 {
		t.Errorf("Output should be limited to 5, got %f", output)
	}

	output = pid.Update(-10.0, 0.0, 2e9)
	if output < -5 {
		t.Errorf("Output should be limited to -5, got %f", output)
	}
}

func TestPIDControllerReset(t *testing.T) {
	pid := NewPIDController(1.0, 0.5, 0.1, -10, 10)

	pid.Update(1.0, 0.0, 1e9)
	pid.Update(1.0, 0.5, 2e9)

	pid.Reset()

	expectedAfterReset := 1.0 * 1.0
	output := pid.Update(1.0, 0.0, 3e9)

	if output != expectedAfterReset {
		t.Errorf("After reset, first update should be proportional only: expected %f, got %f",
			expectedAfterReset, output)
	}
}

func TestController(t *testing.T) {
	cfg := config.AttitudeControlConfig{
		KpRoll:             0.15,
		KpPitch:            0.15,
		KpYaw:              0.12,
		KiRoll:             0.01,
		KiPitch:            0.01,
		KiYaw:              0.008,
		KdRoll:             0.05,
		KdPitch:            0.05,
		KdYaw:              0.04,
		MaxThrustPerAxis:   200e-6,
		MinPulseDurationUs: 100,
		DeadBandAngle:      0.0001,
	}

	ctrl := NewController(cfg, 4)

	target := types.AttitudeState{
		RollAngle:  0.0,
		PitchAngle: 0.0,
		YawAngle:   0.0,
	}
	ctrl.SetTarget(target)

	current := types.AttitudeState{
		RollAngle:  0.05,
		PitchAngle: -0.03,
		YawAngle:   0.02,
	}
	ctrl.UpdateCurrent(current)

	commands := ctrl.Compute(1e9)

	if len(commands) == 0 {
		t.Log("No commands generated (may be below minimum pulse duration)")
	} else {
		t.Logf("Generated %d thrust commands", len(commands))
		for _, cmd := range commands {
			t.Logf("  Thruster %d: %.2f uN, %d us",
				cmd.ThrusterID, cmd.ThrustLevel*1e6, cmd.DurationUs)
		}
	}
}

func TestControllerDeadBand(t *testing.T) {
	cfg := config.AttitudeControlConfig{
		KpRoll:             0.15,
		KpPitch:            0.15,
		KpYaw:              0.12,
		KiRoll:             0.0,
		KiPitch:            0.0,
		KiYaw:              0.0,
		KdRoll:             0.0,
		KdPitch:            0.0,
		KdYaw:              0.0,
		MaxThrustPerAxis:   200e-6,
		MinPulseDurationUs: 100,
		DeadBandAngle:      0.01,
	}

	ctrl := NewController(cfg, 4)

	target := types.AttitudeState{}
	ctrl.SetTarget(target)

	current := types.AttitudeState{
		RollAngle: 0.001,
	}
	ctrl.UpdateCurrent(current)

	commands := ctrl.Compute(1e9)

	if len(commands) > 0 {
		t.Error("Should not generate commands within dead band")
	}
}

type mockSafety struct{}

func (m *mockSafety) IsHealthy() bool             { return true }
func (m *mockSafety) Status() types.SafetyStatus  { return types.SafetyStatus{SystemHealthy: true} }

func TestDecisionEngine(t *testing.T) {
	cfg := config.AttitudeControlConfig{
		KpRoll:             0.15,
		KpPitch:            0.15,
		KpYaw:              0.12,
		KiRoll:             0.0,
		KiPitch:            0.0,
		KiYaw:              0.0,
		KdRoll:             0.0,
		KdPitch:            0.0,
		KdYaw:              0.0,
		MaxThrustPerAxis:   200e-6,
		MinPulseDurationUs: 100,
		DeadBandAngle:      0.001,
	}

	ctrl := NewController(cfg, 4)
	safety := &mockSafety{}
	de := NewDecisionEngine(ctrl, safety)

	if de.GetMode() != ModeStandby {
		t.Error("Initial mode should be standby")
	}

	de.SetMode(ModeHold)
	de.SetTarget(types.AttitudeState{})

	de.UpdateState(types.AttitudeState{
		RollAngle: 0.1,
	})

	de.Enable()
	commands := de.Process(1e9)
	t.Logf("Hold mode commands: %d", len(commands))

	de.Disable()
	commands = de.Process(2e9)
	if len(commands) > 0 {
		t.Error("Should not generate commands when disabled")
	}
}

func TestDecisionEngineManualMode(t *testing.T) {
	cfg := config.AttitudeControlConfig{
		MaxThrustPerAxis:   200e-6,
		MinPulseDurationUs: 100,
	}

	ctrl := NewController(cfg, 4)
	safety := &mockSafety{}
	de := NewDecisionEngine(ctrl, safety)

	de.SetMode(ModeManual)
	de.SetManualThrust(50e-6)
	de.Enable()

	commands := de.Process(1e9)

	if len(commands) == 0 {
		t.Error("Manual mode should generate commands")
	}
}

type unhealthySafety struct{}

func (u *unhealthySafety) IsHealthy() bool            { return false }
func (u *unhealthySafety) Status() types.SafetyStatus { return types.SafetyStatus{SystemHealthy: false} }

func TestDecisionEngineSafetyLockout(t *testing.T) {
	ctrl := NewController(config.AttitudeControlConfig{
		MaxThrustPerAxis:   200e-6,
		MinPulseDurationUs: 100,
	}, 4)

	safety := &unhealthySafety{}
	de := NewDecisionEngine(ctrl, safety)

	de.SetMode(ModeHold)
	de.Enable()

	commands := de.Process(1e9)
	if len(commands) > 0 {
		t.Error("Should not generate commands when system is unhealthy")
	}
}

func BenchmarkPIDControllerUpdate(b *testing.B) {
	pid := NewPIDController(0.15, 0.01, 0.05, -200e-6, 200e-6)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pid.Update(0.0, 0.05, uint64(i)*50000)
	}
}

func BenchmarkControllerCompute(b *testing.B) {
	ctrl := NewController(config.AttitudeControlConfig{
		KpRoll:             0.15,
		KpPitch:            0.15,
		KpYaw:              0.12,
		KiRoll:             0.01,
		KiPitch:            0.01,
		KiYaw:              0.008,
		KdRoll:             0.05,
		KdPitch:            0.05,
		KdYaw:              0.04,
		MaxThrustPerAxis:   200e-6,
		MinPulseDurationUs: 100,
		DeadBandAngle:      0.001,
	}, 4)

	ctrl.SetTarget(types.AttitudeState{})
	ctrl.UpdateCurrent(types.AttitudeState{
		RollAngle:  0.05,
		PitchAngle: -0.03,
		YawAngle:   0.02,
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctrl.Compute(uint64(i) * 50000)
	}
}

func BenchmarkDecisionEngineProcess(b *testing.B) {
	ctrl := NewController(config.AttitudeControlConfig{
		KpRoll:    0.15,
		KpPitch:   0.15,
		KpYaw:     0.12,
		KiRoll:    0.01,
		KiPitch:   0.01,
		KiYaw:     0.008,
		KdRoll:    0.05,
		KdPitch:   0.05,
		KdYaw:     0.04,
		MaxThrustPerAxis:   200e-6,
		MinPulseDurationUs: 100,
		DeadBandAngle:      0.001,
	}, 4)

	safety := &mockSafety{}
	de := NewDecisionEngine(ctrl, safety)
	de.SetMode(ModeHold)
	de.SetTarget(types.AttitudeState{})
	de.UpdateState(types.AttitudeState{RollAngle: 0.05})
	de.Enable()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		de.Process(uint64(i) * 50000)
	}
}
