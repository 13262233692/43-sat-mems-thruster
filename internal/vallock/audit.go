package vallock

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

type AuditReport struct {
	mu sync.Mutex

	writer        io.Writer
	format        uint8
	buffer        []AuditEvent
	bufferSize    int
	flushCount    int

	eventCount    uint64
	lockCount     uint64
	recoveryCount uint64
	anomalyCount  uint64

	startTime     time.Time
}

const (
	AuditFormatText   uint8 = 0
	AuditFormatJSON   uint8 = 1
	AuditFormatBinary uint8 = 2
)

func NewAuditReport(writer io.Writer, format uint8, bufferSize int) *AuditReport {
	if bufferSize < 1 {
		bufferSize = 256
	}
	return &AuditReport{
		writer:     writer,
		format:     format,
		buffer:     make([]AuditEvent, 0, bufferSize),
		bufferSize: bufferSize,
		startTime:  time.Now(),
	}
}

func (r *AuditReport) Record(event AuditEvent) {
	r.mu.Lock()

	r.eventCount++

	switch event.EventType {
	case AuditEmergencyLock:
		r.lockCount++
	case AuditRecovery:
		r.recoveryCount++
	case AuditEventAnomaly:
		r.anomalyCount++
	}

	r.buffer = append(r.buffer, event)

	if len(r.buffer) >= r.bufferSize {
		r.flushLocked()
	}

	r.mu.Unlock()
}

func (r *AuditReport) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushLocked()
}

func (r *AuditReport) flushLocked() {
	if len(r.buffer) == 0 {
		return
	}

	for i := range r.buffer {
		r.writeEventLocked(&r.buffer[i])
	}
	r.flushCount += len(r.buffer)
	r.buffer = r.buffer[:0]
}

func (r *AuditReport) writeEventLocked(event *AuditEvent) {
	if r.writer == nil {
		return
	}

	switch r.format {
	case AuditFormatText:
		r.writeTextEvent(event)
	case AuditFormatJSON:
		r.writeJSONEvent(event)
	case AuditFormatBinary:
		r.writeBinaryEvent(event)
	}
}

func (r *AuditReport) writeTextEvent(event *AuditEvent) {
	t := time.Unix(0, int64(event.Timestamp))
	ts := t.Format("2006-01-02 15:04:05.000000")

	severityStr := "INFO"
	switch event.Severity {
	case 1:
		severityStr = "WARNING"
	case 2:
		severityStr = "ERROR"
	case 3:
		severityStr = "CRITICAL"
	}

	line := fmt.Sprintf(
		"[%s] %-8s thruster=%d fault=0x%02x health=%.1f→%.1f grid=%.6f nonlinear=%.3f duty=%.3f - %s\n",
		ts,
		severityStr,
		event.ThrusterID,
		event.FaultCode,
		event.HealthBefore,
		event.HealthAfter,
		event.GridCurrent,
		event.Nonlinearity,
		event.DutyCycleNew,
		event.Description,
	)

	_, _ = r.writer.Write([]byte(line))
}

func (r *AuditReport) writeJSONEvent(event *AuditEvent) {
	line := fmt.Sprintf(
		`{"ts":%d,"type":%d,"thruster":%d,"fault":%d,"sev":%d,"h_before":%.2f,"h_after":%.2f,"grid":%.6f,"nonlinear":%.3f,"duty":%.4f,"desc":"%s"}`+"\n",
		event.Timestamp,
		event.EventType,
		event.ThrusterID,
		event.FaultCode,
		event.Severity,
		event.HealthBefore,
		event.HealthAfter,
		event.GridCurrent,
		event.Nonlinearity,
		event.DutyCycleNew,
		event.Description,
	)
	_, _ = r.writer.Write([]byte(line))
}

func (r *AuditReport) writeBinaryEvent(event *AuditEvent) {
	buf := make([]byte, 48)
	_ = buf
	binary := make([]byte, 0, 48)
	binary = append(binary, byte(event.Timestamp>>56), byte(event.Timestamp>>48), byte(event.Timestamp>>40), byte(event.Timestamp>>32))
	binary = append(binary, byte(event.Timestamp>>24), byte(event.Timestamp>>16), byte(event.Timestamp>>8), byte(event.Timestamp))
	binary = append(binary, event.EventType, event.ThrusterID, event.FaultCode, event.Severity)
	_, _ = r.writer.Write(binary)
}

type AuditSummary struct {
	TotalEvents   uint64
	LockEvents    uint64
	RecoveryEvents uint64
	AnomalyEvents uint64
	FlushedCount  int
	BufferUsed    int
	UptimeSec     float64
}

func (r *AuditReport) Summary() AuditSummary {
	r.mu.Lock()
	defer r.mu.Unlock()

	return AuditSummary{
		TotalEvents:    r.eventCount,
		LockEvents:     r.lockCount,
		RecoveryEvents: r.recoveryCount,
		AnomalyEvents:  r.anomalyCount,
		FlushedCount:   r.flushCount,
		BufferUsed:     len(r.buffer),
		UptimeSec:      time.Since(r.startTime).Seconds(),
	}
}

type ValveSafetyManager struct {
	mu sync.RWMutex

	locks         []*ValveSafetyLock
	thrusterCount int

	trigger       LockTrigger
	auditReport   *AuditReport

	emergencyCh   chan []byte
	emergencyBuf  [][]byte
	emergencyIdx  int

	callbacks     []func([]byte, uint8)
}

func NewValveSafetyManager(thrusterCount int, trigger LockTrigger) *ValveSafetyManager {
	if thrusterCount < 1 {
		thrusterCount = 1
	}
	if thrusterCount > 256 {
		thrusterCount = 256
	}

	m := &ValveSafetyManager{
		thrusterCount: thrusterCount,
		trigger:       trigger,
		locks:         make([]*ValveSafetyLock, thrusterCount),
		emergencyCh:   make(chan []byte, 128),
	}

	for i := 0; i < thrusterCount; i++ {
		lock := NewValveSafetyLock(uint8(i), trigger)
		lock.SetFrameCallback(m.onEmergencyFrame)
		m.locks[i] = lock
	}

	return m
}

func (m *ValveSafetyManager) SetAuditReport(report *AuditReport) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.auditReport = report

	for i := range m.locks {
		m.locks[i].SetAuditCallback(m.onAuditEvent)
	}
}

func (m *ValveSafetyManager) AddFrameCallback(cb func([]byte, uint8)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacks = append(m.callbacks, cb)
}

func (m *ValveSafetyManager) onEmergencyFrame(frame []byte, thrusterID uint8) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	frameCopy := make([]byte, len(frame))
	copy(frameCopy, frame)

	select {
	case m.emergencyCh <- frameCopy:
	default:
	}

	for _, cb := range m.callbacks {
		cb(frameCopy, thrusterID)
	}
}

func (m *ValveSafetyManager) onAuditEvent(event AuditEvent) {
	if m.auditReport != nil {
		m.auditReport.Record(event)
	}
}

func (m *ValveSafetyManager) ProcessSample(thrusterID int, sample *types.ThrusterSample) bool {
	if thrusterID < 0 || thrusterID >= m.thrusterCount {
		return false
	}
	return m.locks[thrusterID].ProcessSample(sample)
}

func (m *ValveSafetyManager) ProcessBatch(thrusterID int, samples []types.ThrusterSample) int {
	if thrusterID < 0 || thrusterID >= m.thrusterCount {
		return 0
	}
	return m.locks[thrusterID].ProcessBatch(samples)
}

func (m *ValveSafetyManager) LockCount(thrusterID int) uint64 {
	if thrusterID < 0 || thrusterID >= m.thrusterCount {
		return 0
	}
	return m.locks[thrusterID].LockCount()
}

func (m *ValveSafetyManager) IsLocked(thrusterID int) bool {
	if thrusterID < 0 || thrusterID >= m.thrusterCount {
		return false
	}
	return m.locks[thrusterID].IsLocked()
}

func (m *ValveSafetyManager) HealthState(thrusterID int) ValveHealthState {
	if thrusterID < 0 || thrusterID >= m.thrusterCount {
		return ValveHealthState{}
	}
	return m.locks[thrusterID].HealthState()
}

func (m *ValveSafetyManager) TotalLocks() uint64 {
	var total uint64
	for i := range m.locks {
		total += m.locks[i].LockCount()
	}
	return total
}

func (m *ValveSafetyManager) EmergencyChannel() <-chan []byte {
	return m.emergencyCh
}

func (m *ValveSafetyManager) Reset() {
	for i := range m.locks {
		m.locks[i].Reset()
	}
}
