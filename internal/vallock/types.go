package vallock

import "time"

const (
	ValveStateHealthy   uint8 = 0
	ValveStateWear      uint8 = 1
	ValveStateCritical  uint8 = 2
	ValveStateLocked    uint8 = 3
	ValveStateSeized    uint8 = 4
)

const (
	FaultValveNone           uint8 = 0
	FaultValveCrawl          uint8 = 1
	FaultValveBacklash       uint8 = 2
	FaultValveZeroDrift      uint8 = 3
	FaultValveSeizureRisk    uint8 = 4
	FaultValveLeakage        uint8 = 5
	FaultValveEmergencyLock  uint8 = 6
)

const (
	SampleRateHz      = 20000
	SampleIntervalNs  = int64(time.Second) / SampleRateHz
	SampleIntervalSec = 1.0 / float64(SampleRateHz)
)

type ValveHealthState struct {
	Timestamp          uint64
	State              uint8
	HealthScore        float64
	WearAccumulation   float64
	CrawlVelocity      float64
	BacklashAmount     float64
	PlasticDeformation float64
	StressLevel        float64
	ZeroCrossingCount  uint64
	LastFaultCode      uint8
}

type ZeroCrossingEvent struct {
	Timestamp       uint64
	GridCurrent     float64
	FirstDerivative float64
	SecondDerivative float64
	ThirdDerivative  float64
	Nonlinearity    float64
	Direction       int8
	AnomalyScore    float64
}

type EmergencyStopFrame struct {
	Timestamp       uint64
	ThrusterID      uint8
	CommandID       uint64
	DutyCycle       float64
	LockDurationUs  uint32
	FaultCode       uint8
	HealthScore     float64
	Checksum        uint16
}

type AuditEvent struct {
	Timestamp     uint64
	EventType     uint8
	ThrusterID    uint8
	FaultCode     uint8
	Severity      uint8
	HealthBefore  float64
	HealthAfter   float64
	GridCurrent   float64
	Nonlinearity  float64
	DutyCycleNew  float64
	Description   string
}

const (
	AuditEventStateChange uint8 = 1
	AuditEventZeroCross   uint8 = 2
	AuditEventAnomaly     uint8 = 3
	AuditEmergencyLock    uint8 = 4
	AuditRecovery         uint8 = 5
)
