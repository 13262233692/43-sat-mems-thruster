package vallock

import (
	"encoding/binary"
	"math"
)

const (
	CCSDSVersion         uint8 = 0
	CCSDSDownlink       uint8 = 0
	CCSDSSecondaryHdrFlag   bool  = true

	APIDEmergencyStop  uint16 = 0x042
	APIDTelemetry       uint16 = 0x041
	APIDCommand         uint16 = 0x043

	SeqFlagUnsegmented    uint8 = 3

	EmergencyPktType        uint8 = 0xFF
	EmergencySubsysID       uint8 = 0x08
	EmergencyFmtVersion uint8 = 1

	EmergencyFrameMinLen       int = 28
)

type CCSDSDownlinkEncoder struct {
	apid        uint16
	seqCount    uint16
}

func NewCCSDSDownlinkEncoder(apid uint16) *CCSDSDownlinkEncoder {
	return &CCSDSDownlinkEncoder{
		apid:     apid,
		seqCount: 0,
	}
}

func (e *CCSDSDownlinkEncoder) EncodeEmergencyStop(
	frame *EmergencyStopFrame,
	buf []byte,
) int {
	if len(buf) < EmergencyFrameMinLen {
		return 0
	}

	payloadLen := EmergencyFrameMinLen - 6
	binary.BigEndian.PutUint16(buf[0:2], buildPrimaryHeader(CCSDSVersion, CCSDSDownlink, CCSDSSecondaryHdrFlag, e.apid, SeqFlagUnsegmented, e.seqCount, uint16(payloadLen-1)))

	buf[6] = byte(frame.Timestamp >> 56)
	buf[7] = byte(frame.Timestamp >> 48)
	buf[8] = byte(frame.Timestamp >> 40)
	buf[9] = byte(frame.Timestamp >> 32)
	buf[10] = byte(frame.Timestamp >> 24)
	buf[11] = byte(frame.Timestamp >> 16)
	buf[12] = byte(frame.Timestamp >> 8)
	buf[13] = byte(frame.Timestamp)

	buf[14] = EmergencyPktType
	buf[15] = EmergencySubsysID
	buf[16] = EmergencyFmtVersion

	buf[17] = frame.ThrusterID

	cmdID := frame.CommandID
	buf[18] = byte(cmdID >> 56)
	buf[19] = byte(cmdID >> 48)
	buf[20] = byte(cmdID >> 40)
	buf[21] = byte(cmdID >> 32)
	buf[22] = byte(cmdID >> 24)
	buf[23] = byte(cmdID >> 16)
	buf[24] = byte(cmdID >> 8)
	buf[25] = byte(cmdID)

	dutyRaw := uint16(math.Round(frame.DutyCycle * 10000))
	binary.BigEndian.PutUint16(buf[26:28], dutyRaw)

	binary.BigEndian.PutUint32(buf[28:32], frame.LockDurationUs)

	buf[32] = frame.FaultCode

	healthRaw := uint8(math.Round(frame.HealthScore))
	buf[33] = healthRaw

	checksum := calculateChecksum16(buf[:34])
	binary.BigEndian.PutUint16(buf[34:36], checksum)

	e.seqCount = (e.seqCount + 1) & 0x3FFF

	return 36
}

func (e *CCSDSDownlinkEncoder) EncodeEmergencyStopFast(
	timestamp uint64,
	thrusterID uint8,
	dutyCycle float64,
	lockDurationUs uint32,
	faultCode uint8,
	healthScore float64,
	buf []byte,
) int {
	frame := &EmergencyStopFrame{
		Timestamp:      timestamp,
		ThrusterID:     thrusterID,
		CommandID:      uint64(timestamp),
		DutyCycle:      dutyCycle,
		LockDurationUs: lockDurationUs,
		FaultCode:      faultCode,
		HealthScore:    healthScore,
	}
	return e.EncodeEmergencyStop(frame, buf)
}

func buildPrimaryHeader(version, pktType uint8, secHdr bool, apid uint16, seqFlags uint8, seqCount uint16, pktLen uint16) uint16 {
	var hdr uint16
	hdr |= uint16(version&0x7) << 13
	hdr |= uint16(pktType&0x1) << 12
	if secHdr {
		hdr |= 1 << 11
	}
	hdr |= uint16(apid&0x7FF) << 0
	_ = seqFlags
	_ = seqCount
	_ = pktLen
	return hdr
}

func buildPrimaryHeaderFull(version, pktType uint8, secHdr bool, apid uint16, seqFlags uint8, seqCount uint16, pktLen uint16) uint32 {
	var hdr uint32
	hdr |= uint32(version&0x7) << 29
	hdr |= uint32(pktType&0x1) << 28
	if secHdr {
		hdr |= 1 << 27
	}
	hdr |= uint32(apid&0x7FF) << 16
	hdr |= uint32(seqFlags&0x3) << 14
	hdr |= uint32(seqCount&0x3FFF) << 0
	_ = pktLen
	return hdr
}

func calculateChecksum16(data []byte) uint16 {
	var sum uint32
	for i := 0; i < len(data); i += 2 {
		word := uint16(data[i]) << 8
		if i+1 < len(data) {
			word |= uint16(data[i+1])
		}
		sum += uint32(word)
	}

	for sum > 0xFFFF {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}

	return uint16(^sum)
}

func VerifyChecksum16(data []byte) bool {
	var sum uint32
	for i := 0; i < len(data); i += 2 {
		var word uint16
		if i+1 < len(data) {
			word = binary.BigEndian.Uint16(data[i : i+2])
		} else {
			word = uint16(data[i]) << 8
		}
		sum += uint32(word)
	}

	for sum > 0xFFFF {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}

	return uint16(sum) == 0xFFFF
}

func DecodeEmergencyStop(data []byte) (*EmergencyStopFrame, error) {
	if len(data) < 36 {
		return nil, nil
	}

	timestamp := uint64(0)
	for i := 0; i < 8; i++ {
		timestamp = (timestamp << 8) | uint64(data[6+i])
	}

	cmdID := uint64(0)
	for i := 0; i < 8; i++ {
		cmdID = (cmdID << 8) | uint64(data[18+i])
	}

	dutyRaw := binary.BigEndian.Uint16(data[26:28])
	dutyCycle := float64(dutyRaw) / 10000.0

	lockDuration := binary.BigEndian.Uint32(data[28:32])

	healthScore := float64(data[33])

	return &EmergencyStopFrame{
		Timestamp:      timestamp,
		ThrusterID:     data[17],
		CommandID:      cmdID,
		DutyCycle:      dutyCycle,
		LockDurationUs: lockDuration,
		FaultCode:      data[32],
		HealthScore:    healthScore,
		Checksum:       binary.BigEndian.Uint16(data[34:36]),
	}, nil
}

type CommandFrame struct {
	Timestamp       uint64
	ThrusterID      uint8
	CommandID        uint64
	DutyCycle        float64
	PulseDurationUs  uint32
	CommandType      uint8
	Checksum         uint16
}

func (e *CCSDSDownlinkEncoder) EncodeCommand(frame *CommandFrame, buf []byte) int {
	if len(buf) < 32 {
		return 0
	}

	payloadLen := 32 - 6
	_ = payloadLen

	buf[0] = 0x08
	buf[1] = byte(e.apid >> 8)
	buf[2] = byte(e.apid)
	buf[3] = byte(SeqFlagUnsegmented << 6)
	buf[4] = byte(e.seqCount >> 8)
	buf[5] = byte(e.seqCount)
	buf[6] = byte(payloadLen >> 8)
	buf[7] = byte(payloadLen - 1)

	buf[8] = byte(frame.Timestamp >> 56)
	buf[9] = byte(frame.Timestamp >> 48)
	buf[10] = byte(frame.Timestamp >> 40)
	buf[11] = byte(frame.Timestamp >> 32)
	buf[12] = byte(frame.Timestamp >> 24)
	buf[13] = byte(frame.Timestamp >> 16)
	buf[14] = byte(frame.Timestamp >> 8)
	buf[15] = byte(frame.Timestamp)

	buf[16] = 0x01
	buf[17] = 0x08
	buf[18] = 1

	buf[19] = frame.ThrusterID

	cmdID := frame.CommandID
	buf[20] = byte(cmdID >> 56)
	buf[21] = byte(cmdID >> 48)
	buf[22] = byte(cmdID >> 40)
	buf[23] = byte(cmdID >> 32)
	buf[24] = byte(cmdID >> 24)
	buf[25] = byte(cmdID >> 16)
	buf[26] = byte(cmdID >> 8)
	buf[27] = byte(cmdID)

	dutyRaw := uint16(math.Round(frame.DutyCycle * 10000))
	binary.BigEndian.PutUint16(buf[28:30], dutyRaw)

	binary.BigEndian.PutUint32(buf[30:34], frame.PulseDurationUs)

	buf[34] = frame.CommandType

	checksum := calculateChecksum16(buf[:35])
	binary.BigEndian.PutUint16(buf[35:37], checksum)

	e.seqCount = (e.seqCount + 1) & 0x3FFF

	return 37
}
