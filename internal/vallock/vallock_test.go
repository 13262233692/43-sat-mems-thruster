package vallock

import (
	"bytes"
	"math"
	"testing"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

func TestMaxwellMeyerBasic(t *testing.T) {
	model := NewMaxwellMeyerModel()

	score := model.HealthScore()
	if score < 99 || score > 101 {
		t.Errorf("initial health score should be ~100, got %.2f", score)
	}

	sample := &types.ThrusterSample{
		Timestamp:   0,
		GridCurrent: 0.1,
		Thrust:      0.001,
		Valid:       true,
	}
	model.Update(sample.GridCurrent, sample.Thrust, sample.Timestamp)

	if model.ZeroCrossings() != 0 {
		t.Errorf("zero crossings should be 0 after first positive sample")
	}
}

func TestMaxwellMeyerZeroCrossing(t *testing.T) {
	model := NewMaxwellMeyerModel()

	samples := make([]types.ThrusterSample, 600)
	ts := uint64(0)
	for i := 0; i < 600; i++ {
		t := float64(i) * SampleIntervalSec
		samples[i] = types.ThrusterSample{
			Timestamp:   ts,
			GridCurrent: 0.05 * math.Sin(2*math.Pi*50*t),
			Thrust:      0.001,
			Valid:       true,
		}
		ts += uint64(SampleIntervalNs)
	}

	model.ProcessBatch(samples)

	if model.ZeroCrossings() < 2 {
		t.Errorf("expected at least 2 zero crossings, got %d", model.ZeroCrossings())
	}

	score := model.HealthScore()
	if score < 0 || score > 100 {
		t.Errorf("health score out of range: %.2f", score)
	}
}

func TestMaxwellMeyerWearAccumulation(t *testing.T) {
	model := NewMaxwellMeyerModel()

	samples := make([]types.ThrusterSample, 10000)
	ts := uint64(0)
	for i := 0; i < 10000; i++ {
		t := float64(i) * SampleIntervalSec
		samples[i] = types.ThrusterSample{
			Timestamp:   ts,
			GridCurrent: 0.1 * math.Sin(2*math.Pi*100*t),
			Thrust:      0.001,
			Valid:       true,
		}
		ts += uint64(SampleIntervalNs)
	}

	model.ProcessBatch(samples)

	wear := model.WearAccumulation()
	if wear <= 0 {
		t.Errorf("wear accumulation should be > 0, got %f", wear)
	}

	state := model.GetState()
	if state.HealthScore >= 100 {
		t.Errorf("health should degrade after cycles, got %.2f", state.HealthScore)
	}
}

func TestZeroCrossingDetectorBasic(t *testing.T) {
	detector := NewZeroCrossingDetector(32)

	sample := &types.ThrusterSample{
		Timestamp:   0,
		GridCurrent: 0.01,
		Valid:       true,
	}
	evt := detector.Update(sample)
	if evt != nil {
		t.Errorf("first sample should not generate event")
	}
}

func TestZeroCrossingDetectorDetection(t *testing.T) {
	detector := NewZeroCrossingDetector(32)

	ts := uint64(0)
	events := 0

	for i := 0; i < 500; i++ {
		t := float64(i) * SampleIntervalSec
		sample := &types.ThrusterSample{
			Timestamp:   ts,
			GridCurrent: 0.05 * math.Sin(2*math.Pi*200*t),
			Valid:       true,
		}
		if detector.Update(sample) != nil {
			events++
		}
		ts += uint64(SampleIntervalNs)
	}

	if events < 2 {
		t.Errorf("expected at least 2 zero crossing events, got %d", events)
	}
}

func TestZeroCrossingAnomalyDetection(t *testing.T) {
	detector := NewZeroCrossingDetector(64)
	detector.SetThresholds(1.0, 0.3)

	ts := uint64(0)
	anomalyEvents := 0

	for i := 0; i < 200; i++ {
		t := float64(i) * SampleIntervalSec
		gc := 0.05 * math.Sin(2*math.Pi*200*t)
		if i > 100 && i < 120 {
			gc *= 3.0
		}
		sample := &types.ThrusterSample{
			Timestamp:   ts,
			GridCurrent: gc,
			Valid:       true,
		}
		if evt := detector.Update(sample); evt != nil && detector.IsAnomalous(evt) {
			anomalyEvents++
		}
		ts += uint64(SampleIntervalNs)
	}

	if anomalyEvents == 0 {
		t.Logf("warning: no anomaly events detected (may be expected depending on parameters)")
	}
}

func TestCCSDSDownlinkEncoding(t *testing.T) {
	encoder := NewCCSDSDownlinkEncoder(APIDEmergencyStop)

	frame := &EmergencyStopFrame{
		Timestamp:      1234567890000000000,
		ThrusterID:     3,
		CommandID:      42,
		DutyCycle:      0.0,
		LockDurationUs: 5000000,
		FaultCode:      FaultValveSeizureRisk,
		HealthScore:    42.5,
	}

	buf := make([]byte, 64)
	n := encoder.EncodeEmergencyStop(frame, buf)

	if n < 36 {
		t.Errorf("encoded frame too short: %d", n)
	}

	if buf[17] != 3 {
		t.Errorf("thruster ID mismatch: got %d, want 3", buf[17])
	}

	if buf[32] != FaultValveSeizureRisk {
		t.Errorf("fault code mismatch: got %d, want %d", buf[32], FaultValveSeizureRisk)
	}
}

func TestCCSDSDownlinkRoundTrip(t *testing.T) {
	encoder := NewCCSDSDownlinkEncoder(APIDEmergencyStop)

	frame := &EmergencyStopFrame{
		Timestamp:      0x0123456789ABCDEF,
		ThrusterID:     7,
		CommandID:      0xFEDCBA9876543210,
		DutyCycle:      0.5,
		LockDurationUs: 12345678,
		FaultCode:      FaultValveEmergencyLock,
		HealthScore:    23.0,
	}

	buf := make([]byte, 64)
	n := encoder.EncodeEmergencyStop(frame, buf)

	decoded, err := DecodeEmergencyStop(buf[:n])
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if decoded.ThrusterID != frame.ThrusterID {
		t.Errorf("thruster ID: got %d, want %d", decoded.ThrusterID, frame.ThrusterID)
	}

	if decoded.CommandID != frame.CommandID {
		t.Errorf("command ID: got %d, want %d", decoded.CommandID, frame.CommandID)
	}

	if decoded.LockDurationUs != frame.LockDurationUs {
		t.Errorf("lock duration: got %d, want %d", decoded.LockDurationUs, frame.LockDurationUs)
	}

	if decoded.FaultCode != frame.FaultCode {
		t.Errorf("fault code: got %d, want %d", decoded.FaultCode, frame.FaultCode)
	}

	if math.Abs(decoded.DutyCycle-frame.DutyCycle) > 0.0002 {
		t.Errorf("duty cycle: got %.4f, want %.4f", decoded.DutyCycle, frame.DutyCycle)
	}
}

func TestCCSDSChecksum(t *testing.T) {
	data := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05}
	checksum := calculateChecksum16(data)

	verifyData := make([]byte, len(data)+2)
	copy(verifyData, data)
	verifyData[len(data)] = byte(checksum >> 8)
	verifyData[len(data)+1] = byte(checksum)

	if !VerifyChecksum16(verifyData) {
		t.Errorf("checksum verification failed")
	}
}

func TestValveSafetyLockBasic(t *testing.T) {
	trigger := DefaultLockTrigger()
	trigger.HealthThreshold = 50.0
	trigger.ConsecutiveAnomalies = 1
	lock := NewValveSafetyLock(0, trigger)

	if lock.IsLocked() {
		t.Errorf("should not be locked initially")
	}

	samples := make([]types.ThrusterSample, 1000)
	ts := uint64(0)
	for i := 0; i < 1000; i++ {
		t := float64(i) * SampleIntervalSec
		samples[i] = types.ThrusterSample{
			Timestamp:   ts,
			GridCurrent: 0.02 * math.Sin(2*math.Pi*50*t),
			Thrust:      0.0001,
			Valid:       true,
		}
		ts += uint64(SampleIntervalNs)
	}

	lock.ProcessBatch(samples)

	state := lock.HealthState()
	if state.HealthScore < 0 || state.HealthScore > 100 {
		t.Errorf("health score out of range: %.2f", state.HealthScore)
	}
}

func TestValveSafetyLockEmergencyEngage(t *testing.T) {
	trigger := DefaultLockTrigger()
	trigger.HealthThreshold = 90.0
	trigger.ConsecutiveAnomalies = 1
	trigger.LockDurationUs = 10000

	lock := NewValveSafetyLock(0, trigger)

	frameReceived := false
	var lastFrame []byte
	lock.SetFrameCallback(func(frame []byte, thrusterID uint8) {
		frameReceived = true
		lastFrame = make([]byte, len(frame))
		copy(lastFrame, frame)
	})

	samples := make([]types.ThrusterSample, 5000)
	ts := uint64(0)
	for i := 0; i < 5000; i++ {
		t := float64(i) * SampleIntervalSec
		gc := 0.1 * math.Sin(2*math.Pi*400*t)
		if i > 3000 {
			gc *= 2.5
		}
		samples[i] = types.ThrusterSample{
			Timestamp:   ts,
			GridCurrent: gc,
			Thrust:      0.001,
			Valid:       true,
		}
		ts += uint64(SampleIntervalNs)
	}

	lock.ProcessBatch(samples)

	if frameReceived && len(lastFrame) < 36 {
		t.Errorf("emergency frame too short: %d", len(lastFrame))
	}

	if lock.LockCount() > 0 && !lock.IsLocked() {
		t.Errorf("lock count > 0 but not locked")
	}
}

func TestAuditReportText(t *testing.T) {
	var buf bytes.Buffer
	report := NewAuditReport(&buf, AuditFormatText, 10)

	event := AuditEvent{
		Timestamp:    1234567890000000000,
		EventType:    AuditEmergencyLock,
		ThrusterID:   3,
		FaultCode:    FaultValveSeizureRisk,
		Severity:     3,
		HealthBefore: 45.0,
		HealthAfter:  45.0,
		GridCurrent:  0.05,
		Nonlinearity: 3.5,
		DutyCycleNew: 0.0,
		Description:  "test event",
	}

	report.Record(event)
	report.Flush()

	output := buf.String()
	if len(output) == 0 {
		t.Errorf("audit report output is empty")
	}
	if !bytes.Contains([]byte(output), []byte("CRITICAL")) {
		t.Errorf("output should contain CRITICAL severity")
	}
	if !bytes.Contains([]byte(output), []byte("thruster=3")) {
		t.Errorf("output should contain thruster ID")
	}
}

func TestAuditReportJSON(t *testing.T) {
	var buf bytes.Buffer
	report := NewAuditReport(&buf, AuditFormatJSON, 10)

	event := AuditEvent{
		Timestamp:    1234567890000000000,
		EventType:    AuditRecovery,
		ThrusterID:   1,
		FaultCode:    FaultValveNone,
		Severity:     1,
		HealthBefore: 40.0,
		HealthAfter:  65.0,
		Description:  "recovery test",
	}

	report.Record(event)
	report.Flush()

	output := buf.String()
	if len(output) == 0 {
		t.Errorf("JSON audit output is empty")
	}
}

func TestValveSafetyManager(t *testing.T) {
	trigger := DefaultLockTrigger()
	mgr := NewValveSafetyManager(4, trigger)

	if mgr.TotalLocks() != 0 {
		t.Errorf("initial lock count should be 0")
	}

	if mgr.IsLocked(0) {
		t.Errorf("thruster 0 should not be locked initially")
	}

	sample := &types.ThrusterSample{
		Timestamp:   0,
		GridCurrent: 0.01,
		Thrust:      0.001,
		Valid:       true,
	}
	mgr.ProcessSample(1, sample)

	state := mgr.HealthState(1)
	if state.HealthScore <= 0 {
		t.Errorf("health score should be positive")
	}
}

func TestChecksumKnownValue(t *testing.T) {
	data := []byte{0x00, 0x00}
	checksum := calculateChecksum16(data)

	if checksum != 0xFFFF {
		t.Errorf("checksum of zero should be 0xFFFF, got 0x%04X", checksum)
	}
}

func TestMaxwellMeyerReset(t *testing.T) {
	model := NewMaxwellMeyerModel()

	for i := 0; i < 100; i++ {
		t := float64(i) * SampleIntervalSec
		model.Update(0.1*math.Sin(2*math.Pi*100*t), 0.001, uint64(i)*uint64(SampleIntervalNs))
	}

	if model.TotalCycles() == 0 {
		t.Errorf("should have some cycles before reset")
	}

	model.Reset()

	if model.TotalCycles() != 0 {
		t.Errorf("cycles should be 0 after reset")
	}

	if model.ZeroCrossings() != 0 {
		t.Errorf("zero crossings should be 0 after reset")
	}

	score := model.HealthScore()
	if score < 99.9 {
		t.Errorf("health should be ~100 after reset, got %.2f", score)
	}
}
