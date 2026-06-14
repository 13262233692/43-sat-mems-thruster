package types

import "time"

const (
	SamplingRateHz   = 20000
	SampleIntervalNs = int64(time.Second) / SamplingRateHz
)

type CCSDSPrimaryHeader struct {
	Version         uint8
	Type            uint8
	SecondaryHeader bool
	APID            uint16
	SequenceFlags   uint8
	SequenceCount   uint16
	PacketLength    uint16
}

type CCSDSSecondaryHeader struct {
	Timestamp     uint64
	PacketType    uint8
	SubsystemID   uint8
	FormatVersion uint8
	Checksum      uint16
}

type ThrusterSample struct {
	Timestamp        uint64
	AnodeVoltage     float64
	GridCurrent      float64
	XenonMassFlow    float64
	Thrust           float64
	AxialTorque      float64
	SequenceNumber   uint64
	Valid            bool
	FaultCode        uint8
}

type AttitudeState struct {
	Timestamp      uint64
	RollAngle      float64
	PitchAngle     float64
	YawAngle       float64
	RollRate       float64
	PitchRate      float64
	YawRate        float64
	Quaternion     [4]float64
}

type ThrustCommand struct {
	Timestamp     uint64
	ThrusterID    uint8
	DurationUs    uint32
	ThrustLevel   float64
	Direction     [3]float64
	CommandID     uint64
}

type SafetyStatus struct {
	Timestamp       uint64
	SystemHealthy   bool
	AnodeOverVolt   bool
	GridOverCurrent bool
	FlowOutOfRange  bool
	ThrustMismatch  bool
	FaultCount      uint32
	LastFaultTime   uint64
	LastFaultCode   uint8
}

const (
	FaultNone            uint8 = 0
	FaultAnodeOverVolt   uint8 = 1
	FaultGridOverCurrent uint8 = 2
	FaultFlowLow         uint8 = 3
	FaultFlowHigh        uint8 = 4
	FaultThrustMismatch  uint8 = 5
	FaultChecksum        uint8 = 6
	FaultTimeout         uint8 = 7
)
