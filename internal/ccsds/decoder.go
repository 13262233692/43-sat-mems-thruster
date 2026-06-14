package ccsds

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

const (
	PrimaryHeaderLen   = 6
	SecondaryHeaderLen = 14
	MinPacketLen       = PrimaryHeaderLen + SecondaryHeaderLen
)

var (
	ErrPacketTooShort    = errors.New("ccsds packet too short")
	ErrInvalidVersion    = errors.New("invalid ccsds version")
	ErrChecksumMismatch  = errors.New("checksum mismatch")
	ErrInvalidPacketType = errors.New("invalid packet type")
)

type Decoder struct {
	buffer    []byte
	bitOffset int
	byteLen   int
}

func NewDecoder() *Decoder {
	return &Decoder{
		buffer:    make([]byte, 4096),
		bitOffset: 0,
		byteLen:   0,
	}
}

func (d *Decoder) Reset(data []byte) {
	n := copy(d.buffer, data)
	d.byteLen = n
	d.bitOffset = 0
}

func (d *Decoder) ReadBits(numBits int) (uint64, error) {
	if numBits < 0 || numBits > 64 {
		return 0, errors.New("invalid bit count")
	}

	totalBits := d.byteLen * 8
	if d.bitOffset+numBits > totalBits {
		return 0, errors.New("bit overflow")
	}

	var result uint64
	remaining := numBits
	currentByte := d.bitOffset / 8
	bitInByte := d.bitOffset % 8

	for remaining > 0 {
		bitsFromByte := 8 - bitInByte
		if bitsFromByte > remaining {
			bitsFromByte = remaining
		}

		mask := byte((1 << bitsFromByte) - 1)
		shift := 8 - bitInByte - bitsFromByte
		value := (d.buffer[currentByte] >> shift) & mask

		result = (result << bitsFromByte) | uint64(value)

		remaining -= bitsFromByte
		bitInByte = 0
		currentByte++
	}

	d.bitOffset += numBits
	return result, nil
}

func (d *Decoder) ReadByte() (byte, error) {
	val, err := d.ReadBits(8)
	return byte(val), err
}

func (d *Decoder) ReadUint16() (uint16, error) {
	val, err := d.ReadBits(16)
	return uint16(val), err
}

func (d *Decoder) ReadUint32() (uint32, error) {
	val, err := d.ReadBits(32)
	return uint32(val), err
}

func (d *Decoder) ReadUint64() (uint64, error) {
	return d.ReadBits(64)
}

func (d *Decoder) ReadFloat32() (float32, error) {
	val, err := d.ReadUint32()
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(val), nil
}

func DecodePrimaryHeader(data []byte) (types.CCSDSPrimaryHeader, int, error) {
	if len(data) < PrimaryHeaderLen {
		return types.CCSDSPrimaryHeader{}, 0, ErrPacketTooShort
	}

	d := NewDecoder()
	d.Reset(data)

	version, err := d.ReadBits(3)
	if err != nil {
		return types.CCSDSPrimaryHeader{}, 0, err
	}
	if version != 0 {
		return types.CCSDSPrimaryHeader{}, 0, ErrInvalidVersion
	}

	pktType, err := d.ReadBits(1)
	if err != nil {
		return types.CCSDSPrimaryHeader{}, 0, err
	}

	secHeaderFlag, err := d.ReadBits(1)
	if err != nil {
		return types.CCSDSPrimaryHeader{}, 0, err
	}

	apid, err := d.ReadBits(11)
	if err != nil {
		return types.CCSDSPrimaryHeader{}, 0, err
	}

	seqFlags, err := d.ReadBits(2)
	if err != nil {
		return types.CCSDSPrimaryHeader{}, 0, err
	}

	seqCount, err := d.ReadBits(14)
	if err != nil {
		return types.CCSDSPrimaryHeader{}, 0, err
	}

	pktLen, err := d.ReadUint16()
	if err != nil {
		return types.CCSDSPrimaryHeader{}, 0, err
	}

	hdr := types.CCSDSPrimaryHeader{
		Version:         uint8(version),
		Type:            uint8(pktType),
		SecondaryHeader: secHeaderFlag == 1,
		APID:            uint16(apid),
		SequenceFlags:   uint8(seqFlags),
		SequenceCount:   uint16(seqCount),
		PacketLength:    pktLen,
	}

	return hdr, PrimaryHeaderLen, nil
}

func DecodeSecondaryHeader(data []byte) (types.CCSDSSecondaryHeader, int, error) {
	if len(data) < SecondaryHeaderLen {
		return types.CCSDSSecondaryHeader{}, 0, ErrPacketTooShort
	}

	d := NewDecoder()
	d.Reset(data)

	timestamp, err := d.ReadUint64()
	if err != nil {
		return types.CCSDSSecondaryHeader{}, 0, err
	}

	pktType, err := d.ReadByte()
	if err != nil {
		return types.CCSDSSecondaryHeader{}, 0, err
	}

	subsysID, err := d.ReadByte()
	if err != nil {
		return types.CCSDSSecondaryHeader{}, 0, err
	}

	fmtVer, err := d.ReadByte()
	if err != nil {
		return types.CCSDSSecondaryHeader{}, 0, err
	}

	_, err = d.ReadByte()
	if err != nil {
		return types.CCSDSSecondaryHeader{}, 0, err
	}

	checksum, err := d.ReadUint16()
	if err != nil {
		return types.CCSDSSecondaryHeader{}, 0, err
	}

	hdr := types.CCSDSSecondaryHeader{
		Timestamp:     timestamp,
		PacketType:    pktType,
		SubsystemID:   subsysID,
		FormatVersion: fmtVer,
		Checksum:      checksum,
	}

	return hdr, SecondaryHeaderLen, nil
}

func DecodeThrusterSamples(data []byte, baseTimestamp uint64, seqStart uint64, count int) ([]types.ThrusterSample, error) {
	samples := make([]types.ThrusterSample, count)
	d := NewDecoder()
	d.Reset(data)

	for i := 0; i < count; i++ {
		anodeRaw, err := d.ReadBits(16)
		if err != nil {
			return nil, err
		}
		anodeVolt := float64(anodeRaw) * 0.1

		gridRaw, err := d.ReadBits(16)
		if err != nil {
			return nil, err
		}
		gridCurrent := float64(gridRaw) * 1e-6

		flowRaw, err := d.ReadBits(16)
		if err != nil {
			return nil, err
		}
		xenonFlow := float64(flowRaw) * 1e-12

		statusByte, err := d.ReadByte()
		if err != nil {
			return nil, err
		}

		samples[i] = types.ThrusterSample{
			Timestamp:     baseTimestamp + uint64(i)*uint64(types.SampleIntervalNs),
			AnodeVoltage:  anodeVolt,
			GridCurrent:   gridCurrent,
			XenonMassFlow: xenonFlow,
			Valid:         (statusByte & 0x80) != 0,
			FaultCode:     statusByte & 0x0F,
		}
	}

	return samples, nil
}

func DecodeThrusterSamplesFloat(data []byte, baseTimestamp uint64, seqStart uint64, count int) ([]types.ThrusterSample, error) {
	samples := make([]types.ThrusterSample, count)
	d := NewDecoder()
	d.Reset(data)

	for i := 0; i < count; i++ {
		anodeBytes := make([]byte, 4)
		for j := 0; j < 4; j++ {
			b, err := d.ReadByte()
			if err != nil {
				return nil, err
			}
			anodeBytes[j] = b
		}
		anodeVolt := float64(binary.BigEndian.Uint32(anodeBytes)) / 1e6

		gridBytes := make([]byte, 4)
		for j := 0; j < 4; j++ {
			b, err := d.ReadByte()
			if err != nil {
				return nil, err
			}
			gridBytes[j] = b
		}
		gridCurrent := float64(binary.BigEndian.Uint32(gridBytes)) / 1e9

		flowBytes := make([]byte, 4)
		for j := 0; j < 4; j++ {
			b, err := d.ReadByte()
			if err != nil {
				return nil, err
			}
			flowBytes[j] = b
		}
		xenonFlow := float64(binary.BigEndian.Uint32(flowBytes)) / 1e12

		statusByte, err := d.ReadByte()
		if err != nil {
			return nil, err
		}

		samples[i] = types.ThrusterSample{
			Timestamp:      baseTimestamp + uint64(i)*uint64(types.SampleIntervalNs),
			AnodeVoltage:   anodeVolt,
			GridCurrent:    gridCurrent,
			XenonMassFlow:  xenonFlow,
			SequenceNumber: seqStart + uint64(i),
			Valid:          (statusByte & 0x80) != 0,
			FaultCode:      statusByte & 0x0F,
		}
	}

	return samples, nil
}

func CalculateChecksum(data []byte) uint16 {
	var sum uint32
	for i := 0; i < len(data); i += 2 {
		if i+1 < len(data) {
			sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
		} else {
			sum += uint32(data[i]) << 8
		}
	}
	for sum>>16 > 0 {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}
	return uint16(^sum)
}

func VerifyChecksum(data []byte, checksum uint16) bool {
	calc := CalculateChecksum(data)
	return calc == checksum
}

type PacketDecoder struct {
	scratch []byte
}

func NewPacketDecoder() *PacketDecoder {
	return &PacketDecoder{
		scratch: make([]byte, 65536),
	}
}

type DecodedPacket struct {
	PrimaryHeader   types.CCSDSPrimaryHeader
	SecondaryHeader types.CCSDSSecondaryHeader
	Payload         []byte
}

func (pd *PacketDecoder) Decode(data []byte) (*DecodedPacket, error) {
	priHdr, priOffset, err := DecodePrimaryHeader(data)
	if err != nil {
		return nil, err
	}

	totalLen := PrimaryHeaderLen + int(priHdr.PacketLength) + 1
	if len(data) < totalLen {
		return nil, ErrPacketTooShort
	}

	if !priHdr.SecondaryHeader {
		return &DecodedPacket{
			PrimaryHeader: priHdr,
			Payload:       data[priOffset:totalLen],
		}, nil
	}

	secHdr, secOffset, err := DecodeSecondaryHeader(data[priOffset:])
	if err != nil {
		return nil, err
	}

	payloadStart := priOffset + secOffset
	payloadEnd := totalLen
	if payloadEnd > len(data) {
		payloadEnd = len(data)
	}
	payload := data[payloadStart:payloadEnd]

	pkt := &DecodedPacket{
		PrimaryHeader:   priHdr,
		SecondaryHeader: secHdr,
		Payload:         payload,
	}

	if priHdr.Type == 0 && secHdr.PacketType != 0 {
		checksumData := data[:priOffset+secOffset-2]
		checksumData = append(checksumData, payload...)
		if !VerifyChecksum(checksumData, secHdr.Checksum) {
			return pkt, ErrChecksumMismatch
		}
	}

	return pkt, nil
}
