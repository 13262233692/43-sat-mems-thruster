package ccsds

import (
	"encoding/binary"
	"testing"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

func TestDecodePrimaryHeader(t *testing.T) {
	data := make([]byte, 6)

	version := uint16(0)
	pktType := uint16(0)
	secHeader := uint16(1)
	apid := uint16(0x042)
	seqFlags := uint16(3)
	seqCount := uint16(1234)
	pktLen := uint16(100)

	firstWord := (version << 13) | (pktType << 12) | (secHeader << 11) | apid
	secondWord := (seqFlags << 14) | (seqCount & 0x3FFF)

	binary.BigEndian.PutUint16(data[0:2], firstWord)
	binary.BigEndian.PutUint16(data[2:4], secondWord)
	binary.BigEndian.PutUint16(data[4:6], pktLen)

	hdr, offset, err := DecodePrimaryHeader(data)
	if err != nil {
		t.Fatalf("DecodePrimaryHeader failed: %v", err)
	}

	if offset != PrimaryHeaderLen {
		t.Errorf("Expected offset %d, got %d", PrimaryHeaderLen, offset)
	}

	if hdr.Version != 0 {
		t.Errorf("Expected Version 0, got %d", hdr.Version)
	}

	if hdr.Type != 0 {
		t.Errorf("Expected Type 0, got %d", hdr.Type)
	}

	if !hdr.SecondaryHeader {
		t.Error("Expected SecondaryHeader true")
	}

	if hdr.APID != apid {
		t.Errorf("Expected APID %d, got %d", apid, hdr.APID)
	}

	if hdr.SequenceFlags != 3 {
		t.Errorf("Expected SequenceFlags 3, got %d", hdr.SequenceFlags)
	}

	if hdr.SequenceCount != seqCount {
		t.Errorf("Expected SequenceCount %d, got %d", seqCount, hdr.SequenceCount)
	}

	if hdr.PacketLength != pktLen {
		t.Errorf("Expected PacketLength %d, got %d", pktLen, hdr.PacketLength)
	}
}

func TestDecodeSecondaryHeader(t *testing.T) {
	data := make([]byte, SecondaryHeaderLen)

	timestamp := uint64(1234567890123456789)
	pktType := byte(1)
	subsysID := byte(2)
	fmtVer := byte(3)
	checksum := uint16(0xABCD)

	binary.BigEndian.PutUint64(data[0:8], timestamp)
	data[8] = pktType
	data[9] = subsysID
	data[10] = fmtVer
	data[11] = 0
	binary.BigEndian.PutUint16(data[12:14], checksum)

	hdr, offset, err := DecodeSecondaryHeader(data)
	if err != nil {
		t.Fatalf("DecodeSecondaryHeader failed: %v", err)
	}

	if offset != SecondaryHeaderLen {
		t.Errorf("Expected offset %d, got %d", SecondaryHeaderLen, offset)
	}

	if hdr.Timestamp != timestamp {
		t.Errorf("Expected Timestamp %d, got %d", timestamp, hdr.Timestamp)
	}

	if hdr.PacketType != pktType {
		t.Errorf("Expected PacketType %d, got %d", pktType, hdr.PacketType)
	}

	if hdr.SubsystemID != subsysID {
		t.Errorf("Expected SubsystemID %d, got %d", subsysID, hdr.SubsystemID)
	}
}

func TestDecoderReadBits(t *testing.T) {
	d := NewDecoder()
	data := []byte{0xAB, 0xCD, 0xEF}
	d.Reset(data)

	val, err := d.ReadBits(4)
	if err != nil {
		t.Fatal(err)
	}
	if val != 0xA {
		t.Errorf("Expected 0xA, got 0x%X", val)
	}

	val, err = d.ReadBits(8)
	if err != nil {
		t.Fatal(err)
	}
	if val != 0xBC {
		t.Errorf("Expected 0xBC, got 0x%X", val)
	}

	val, err = d.ReadBits(12)
	if err != nil {
		t.Fatal(err)
	}
	if val != 0xDEF {
		t.Errorf("Expected 0xDEF, got 0x%X", val)
	}
}

func TestDecodeThrusterSamplesFloat(t *testing.T) {
	sampleCount := 5
	payload := make([]byte, sampleCount*13)

	for i := 0; i < sampleCount; i++ {
		off := i * 13

		anodeRaw := uint32(1500.5 * 1e6)
		binary.BigEndian.PutUint32(payload[off:off+4], anodeRaw)

		gridRaw := uint32(0.15 * 1e9)
		binary.BigEndian.PutUint32(payload[off+4:off+8], gridRaw)

		flowRaw := uint32(1.5e-6 * 1e12)
		binary.BigEndian.PutUint32(payload[off+8:off+12], flowRaw)

		payload[off+12] = 0x80
	}

	samples, err := DecodeThrusterSamplesFloat(payload, 1000, 0, sampleCount)
	if err != nil {
		t.Fatalf("DecodeThrusterSamplesFloat failed: %v", err)
	}

	if len(samples) != sampleCount {
		t.Errorf("Expected %d samples, got %d", sampleCount, len(samples))
	}

	for i, s := range samples {
		if !s.Valid {
			t.Errorf("Sample %d should be valid", i)
		}
		if s.Timestamp != 1000+uint64(i)*uint64(types.SampleIntervalNs) {
			t.Errorf("Sample %d timestamp mismatch", i)
		}
	}
}

func TestCalculateChecksum(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04}
	checksum := CalculateChecksum(data)

	verifyData := []byte{0x01, 0x02, 0x03, 0x04, 0x00, 0x00}
	binary.BigEndian.PutUint16(verifyData[4:6], checksum)

	result := CalculateChecksum(verifyData)
	if result != 0 {
		t.Errorf("Checksum verification failed: expected 0, got 0x%04X", result)
	}
}

func TestPacketDecoder(t *testing.T) {
	pd := NewPacketDecoder()

	const primaryHeaderLen = 6
	const secondaryHeaderLen = 14
	const sampleCount = 10
	const sampleSize = 13
	payloadLen := sampleCount * sampleSize
	totalLen := primaryHeaderLen + secondaryHeaderLen + payloadLen

	data := make([]byte, totalLen)

	version := uint16(0)
	pktType := uint16(0)
	secHeaderFlag := uint16(1)
	apid := uint16(0x042)
	seqFlags := uint16(3)
	seqCount := uint16(1)
	pktLen := uint16(secondaryHeaderLen + payloadLen - 1)

	firstWord := (version << 13) | (pktType << 12) | (secHeaderFlag << 11) | apid
	secondWord := (seqFlags << 14) | (seqCount & 0x3FFF)

	binary.BigEndian.PutUint16(data[0:2], firstWord)
	binary.BigEndian.PutUint16(data[2:4], secondWord)
	binary.BigEndian.PutUint16(data[4:6], pktLen)

	secOffset := primaryHeaderLen
	binary.BigEndian.PutUint64(data[secOffset:secOffset+8], 123456789)
	data[secOffset+8] = 1
	data[secOffset+9] = 2
	data[secOffset+10] = 1
	data[secOffset+11] = 0
	binary.BigEndian.PutUint16(data[secOffset+12:secOffset+14], 0)

	payloadOffset := primaryHeaderLen + secondaryHeaderLen
	for i := 0; i < sampleCount; i++ {
		off := payloadOffset + i*sampleSize
		binary.BigEndian.PutUint32(data[off:off+4], 1500000000)
		binary.BigEndian.PutUint32(data[off+4:off+8], 150000000)
		binary.BigEndian.PutUint32(data[off+8:off+12], 1500)
		data[off+12] = 0x80
	}

	checksumData := make([]byte, 0, primaryHeaderLen+secondaryHeaderLen-2+payloadLen)
	checksumData = append(checksumData, data[:primaryHeaderLen+secondaryHeaderLen-2]...)
	checksumData = append(checksumData, data[payloadOffset:payloadOffset+payloadLen]...)
	checksum := CalculateChecksum(checksumData)
	binary.BigEndian.PutUint16(data[secOffset+12:secOffset+14], checksum)

	pkt, err := pd.Decode(data)
	if err != nil {
		t.Fatalf("Packet decode failed: %v", err)
	}

	if pkt.PrimaryHeader.APID != apid {
		t.Errorf("Expected APID %d, got %d", apid, pkt.PrimaryHeader.APID)
	}

	if !pkt.PrimaryHeader.SecondaryHeader {
		t.Error("Expected secondary header")
	}

	if len(pkt.Payload) != payloadLen {
		t.Errorf("Expected payload length %d, got %d", payloadLen, len(pkt.Payload))
	}
}

func BenchmarkDecodePrimaryHeader(b *testing.B) {
	data := make([]byte, 6)
	firstWord := uint16(0x042 | (1 << 11))
	secondWord := uint16(3 << 14)
	binary.BigEndian.PutUint16(data[0:2], firstWord)
	binary.BigEndian.PutUint16(data[2:4], secondWord)
	binary.BigEndian.PutUint16(data[4:6], 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DecodePrimaryHeader(data)
	}
}

func BenchmarkDecodeThrusterSamplesFloat(b *testing.B) {
	sampleCount := 100
	payload := make([]byte, sampleCount*13)

	for i := 0; i < sampleCount; i++ {
		off := i * 13
		binary.BigEndian.PutUint32(payload[off:off+4], 1500000000)
		binary.BigEndian.PutUint32(payload[off+4:off+8], 150000000)
		binary.BigEndian.PutUint32(payload[off+8:off+12], 1500)
		payload[off+12] = 0x80
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DecodeThrusterSamplesFloat(payload, 0, 0, sampleCount)
	}
}

func BenchmarkDecoderReadBits(b *testing.B) {
	d := NewDecoder()
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.Reset(data)
		for j := 0; j < 256; j++ {
			d.ReadBits(32)
		}
	}
}

func BenchmarkPacketDecoder(b *testing.B) {
	pd := NewPacketDecoder()
	const sampleCount = 10
	const sampleSize = 13
	payloadLen := sampleCount * sampleSize
	totalLen := 6 + 14 + payloadLen
	data := make([]byte, totalLen)

	firstWord := uint16(0x042 | (1 << 11))
	secondWord := uint16(3 << 14)
	pktLen := uint16(14 + payloadLen - 1)

	binary.BigEndian.PutUint16(data[0:2], firstWord)
	binary.BigEndian.PutUint16(data[2:4], secondWord)
	binary.BigEndian.PutUint16(data[4:6], pktLen)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pd.Decode(data)
	}
}
