package vallock

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

type LockTrigger struct {
	AnomalyThreshold     float64
	NonlinearThreshold   float64
	ConsecutiveAnomalies int
	HealthThreshold      float64
	LockDutyCycle        float64
	LockDurationUs       uint32
	MinLockIntervalUs    uint64
}

func DefaultLockTrigger() LockTrigger {
	return LockTrigger{
		AnomalyThreshold:     0.65,
		NonlinearThreshold:   2.5,
		ConsecutiveAnomalies: 2,
		HealthThreshold:      35.0,
		LockDutyCycle:        0.0,
		LockDurationUs:       5000000,
		MinLockIntervalUs:    10000,
	}
}

type ValveSafetyLock struct {
	mu sync.RWMutex

	thrusterID     uint8
	model          *MaxwellMeyerModel
	detector       *ZeroCrossingDetector
	encoder        *CCSDSDownlinkEncoder

	trigger        LockTrigger
	state          ValveHealthState
	isLocked       atomic.Bool
	lockStartTime  uint64
	lastLockTime   uint64
	anomalyCount   int

	consecAnomalies int
	lastEvent      ZeroCrossingEvent

	emergencyBuf   []byte
	frameCallback  func([]byte, uint8)
	auditCallback  func(AuditEvent)

	auditBuffer    []AuditEvent
	auditIdx       int
	auditCount     int

	lockCount      uint64
	recoveryCount  uint64
	totalAnomalies uint64
}

func NewValveSafetyLock(thrusterID uint8, trigger LockTrigger) *ValveSafetyLock {
	return &ValveSafetyLock{
		thrusterID:   thrusterID,
		model:        NewMaxwellMeyerModel(),
		detector:     NewZeroCrossingDetector(32),
		encoder:      NewCCSDSDownlinkEncoder(APIDEmergencyStop),
		trigger:      trigger,
		emergencyBuf: make([]byte, 256),
		auditBuffer:  make([]AuditEvent, 512),
	}
}

func (v *ValveSafetyLock) Configure(trigger LockTrigger) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.trigger = trigger
	v.detector.SetThresholds(trigger.NonlinearThreshold, trigger.AnomalyThreshold)
}

func (v *ValveSafetyLock) SetFrameCallback(cb func([]byte, uint8)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.frameCallback = cb
}

func (v *ValveSafetyLock) SetAuditCallback(cb func(AuditEvent)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.auditCallback = cb
}

func (v *ValveSafetyLock) ProcessSample(sample *types.ThrusterSample) bool {
	if !sample.Valid {
		return false
	}

	v.mu.Lock()

	v.model.Update(sample.GridCurrent, sample.Thrust, sample.Timestamp)

	event := v.detector.Update(sample)

	state := v.model.GetState()
	v.state = state

	triggered := false
	faultCode := uint8(0)

	if event != nil {
		v.lastEvent = *event
		v.totalAnomalies++

		if v.detector.IsAnomalous(event) {
			v.consecAnomalies++

			if v.consecAnomalies >= v.trigger.ConsecutiveAnomalies &&
				state.HealthScore < v.trigger.HealthThreshold {
				triggered = true
				faultCode = FaultValveSeizureRisk
			}
		} else {
			if v.consecAnomalies > 0 {
				v.consecAnomalies--
			}
		}
	}

	if state.HealthScore < 20.0 && !v.isLocked.Load() {
		triggered = true
		faultCode = FaultValveEmergencyLock
	}

	if triggered && !v.isLocked.Load() {
		now := sample.Timestamp
		if v.lastLockTime == 0 || now > v.lastLockTime+v.trigger.MinLockIntervalUs*1000 {
			v.engageLockLocked(now, faultCode)
		}
	}

	v.mu.Unlock()

	return v.isLocked.Load()
}

func (v *ValveSafetyLock) ProcessBatch(samples []types.ThrusterSample) int {
	lockTriggered := 0
	for i := range samples {
		if v.ProcessSample(&samples[i]) {
			lockTriggered++
		}
	}
	return lockTriggered
}

func (v *ValveSafetyLock) engageLockLocked(timestamp uint64, faultCode uint8) {
	v.isLocked.Store(true)
	v.lockStartTime = timestamp
	v.lastLockTime = timestamp
	v.lockCount++
	v.state.State = ValveStateLocked
	v.state.LastFaultCode = faultCode

	frameLen := v.encoder.EncodeEmergencyStopFast(
		timestamp,
		v.thrusterID,
		v.trigger.LockDutyCycle,
		v.trigger.LockDurationUs,
		faultCode,
		v.state.HealthScore,
		v.emergencyBuf,
	)

	if v.frameCallback != nil && frameLen > 0 {
		v.frameCallback(v.emergencyBuf[:frameLen], v.thrusterID)
	}

	audit := AuditEvent{
		Timestamp:    timestamp,
		EventType:    AuditEmergencyLock,
		ThrusterID:   v.thrusterID,
		FaultCode:    faultCode,
		Severity:     3,
		HealthBefore: v.state.HealthScore,
		HealthAfter:  v.state.HealthScore,
		GridCurrent:  v.lastEvent.GridCurrent,
		Nonlinearity: v.lastEvent.Nonlinearity,
		DutyCycleNew: v.trigger.LockDutyCycle,
		Description:  "Emergency safety lock engaged - valve seizure risk detected",
	}
	v.recordAuditLocked(audit)

	if v.auditCallback != nil {
		v.auditCallback(audit)
	}
}

func (v *ValveSafetyLock) DisengageLock(timestamp uint64) bool {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.isLocked.Load() {
		return false
	}

	if timestamp < v.lockStartTime+uint64(v.trigger.LockDurationUs)*1000 {
		return false
	}

	healthBefore := v.state.HealthScore
	v.isLocked.Store(false)
	v.consecAnomalies = 0
	v.recoveryCount++

	if v.state.HealthScore > 60 {
		v.state.State = ValveStateHealthy
	} else {
		v.state.State = ValveStateWear
	}

	audit := AuditEvent{
		Timestamp:    timestamp,
		EventType:    AuditRecovery,
		ThrusterID:   v.thrusterID,
		FaultCode:    FaultValveNone,
		Severity:     1,
		HealthBefore: healthBefore,
		HealthAfter:  v.state.HealthScore,
		Description:  "Safety lock disengaged - valve health recovered",
	}
	v.recordAuditLocked(audit)

	if v.auditCallback != nil {
		v.auditCallback(audit)
	}

	return true
}

func (v *ValveSafetyLock) IsLocked() bool {
	return v.isLocked.Load()
}

func (v *ValveSafetyLock) HealthState() ValveHealthState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	state := v.state
	state.State = v.currentStateLocked()
	return state
}

func (v *ValveSafetyLock) currentStateLocked() uint8 {
	if v.isLocked.Load() {
		return ValveStateLocked
	}
	return v.state.State
}

func (v *ValveSafetyLock) LockCount() uint64 {
	return atomic.LoadUint64(&v.lockCount)
}

func (v *ValveSafetyLock) RecoveryCount() uint64 {
	return atomic.LoadUint64(&v.recoveryCount)
}

func (v *ValveSafetyLock) TotalAnomalies() uint64 {
	return atomic.LoadUint64(&v.totalAnomalies)
}

func (v *ValveSafetyLock) LastEvent() ZeroCrossingEvent {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.lastEvent
}

func (v *ValveSafetyLock) RecentAuditEvents(maxCount int) []AuditEvent {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if maxCount > v.auditCount {
		maxCount = v.auditCount
	}
	if maxCount <= 0 {
		return nil
	}

	result := make([]AuditEvent, maxCount)
	for i := 0; i < maxCount; i++ {
		offset := (v.auditIdx - maxCount + i + len(v.auditBuffer)) % len(v.auditBuffer)
		result[i] = v.auditBuffer[offset]
	}
	return result
}

func (v *ValveSafetyLock) recordAuditLocked(event AuditEvent) {
	v.auditBuffer[v.auditIdx] = event
	v.auditIdx = (v.auditIdx + 1) % len(v.auditBuffer)
	if v.auditCount < len(v.auditBuffer) {
		v.auditCount++
	}
}

func (v *ValveSafetyLock) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.model.Reset()
	v.detector.Reset()
	v.isLocked.Store(false)
	v.lockStartTime = 0
	v.lastLockTime = 0
	v.anomalyCount = 0
	v.consecAnomalies = 0
	v.state = ValveHealthState{}
	v.lockCount = 0
	v.recoveryCount = 0
	v.totalAnomalies = 0
	v.auditIdx = 0
	v.auditCount = 0
}

func (v *ValveSafetyLock) MonitorLoop(stopCh <-chan struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if v.IsLocked() {
				now := uint64(time.Now().UnixNano())
				v.DisengageLock(now)
			}
		case <-stopCh:
			return
		}
	}
}
