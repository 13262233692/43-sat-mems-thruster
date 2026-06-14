//go:build windows

package capture

import (
	"encoding/binary"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

type simulatedPacketSource struct {
	iface     string
	filter    string
	running   atomic.Bool
	stats     CaptureStats
	mu        sync.Mutex
	seqCount  uint16
	timestamp uint64
	randGen   *rand.Rand
	packetCh  chan *Packet
	closeCh   chan struct{}
}

func newPlatformPacketSource() PacketSource {
	return &simulatedPacketSource{
		randGen: rand.New(rand.NewSource(time.Now().UnixNano())),
		packetCh: make(chan *Packet, 1024),
		closeCh:  make(chan struct{}),
	}
}

func (s *simulatedPacketSource) Open(iface string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.iface = iface
	s.running.Store(true)
	s.timestamp = uint64(time.Now().UnixNano())

	go s.simulateTraffic()

	return nil
}

func (s *simulatedPacketSource) Close() error {
	s.running.Store(false)
	select {
	case <-s.closeCh:
	default:
		close(s.closeCh)
	}
	return nil
}

func (s *simulatedPacketSource) ReadPacket(timeout time.Duration) (*Packet, error) {
	if !s.running.Load() {
		return nil, ErrClosed
	}

	select {
	case pkt, ok := <-s.packetCh:
		if !ok {
			return nil, ErrClosed
		}
		atomic.AddUint64(&s.stats.PacketsReceived, 1)
		atomic.AddUint64(&s.stats.BytesReceived, uint64(pkt.Length))
		return pkt, nil
	case <-time.After(timeout):
		return nil, ErrTimeout
	}
}

func (s *simulatedPacketSource) SetBPFFilter(filter string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filter = filter
	return nil
}

func (s *simulatedPacketSource) Stats() CaptureStats {
	return CaptureStats{
		PacketsReceived: atomic.LoadUint64(&s.stats.PacketsReceived),
		PacketsDropped:  atomic.LoadUint64(&s.stats.PacketsDropped),
		BytesReceived:   atomic.LoadUint64(&s.stats.BytesReceived),
		Errors:          atomic.LoadUint64(&s.stats.Errors),
	}
}

func (s *simulatedPacketSource) simulateTraffic() {
	ticker := time.NewTicker(50 * time.Microsecond)
	defer ticker.Stop()

	sampleInterval := int64(50 * time.Microsecond)
	samplesPerPacket := 10

	for {
		select {
		case <-s.closeCh:
			close(s.packetCh)
			return
		case <-ticker.C:
			pkt := s.generateCCSDSPacket(samplesPerPacket)
			s.seqCount++
			s.timestamp += uint64(sampleInterval * int64(samplesPerPacket))

			select {
			case s.packetCh <- pkt:
			default:
				atomic.AddUint64(&s.stats.PacketsDropped, 1)
			}
		}
	}
}

func (s *simulatedPacketSource) generateCCSDSPacket(sampleCount int) *Packet {
	const primaryHeaderLen = 6
	const secondaryHeaderLen = 14
	const sampleSize = 13
	payloadLen := sampleCount * sampleSize
	totalLen := primaryHeaderLen + secondaryHeaderLen + payloadLen

	data := make([]byte, totalLen)

	version := uint16(0)
	pktType := uint16(0)
	secHeaderFlag := uint16(1)
	apid := uint16(0x042)
	seqFlags := uint16(3)
	seqCount := uint16(s.seqCount)

	firstWord := (version << 13) | (pktType << 12) | (secHeaderFlag << 11) | apid
	secondWord := (seqFlags << 14) | (seqCount & 0x3FFF)
	pktLen := uint16(secondaryHeaderLen + payloadLen - 1)

	binary.BigEndian.PutUint16(data[0:2], firstWord)
	binary.BigEndian.PutUint16(data[2:4], secondWord)
	binary.BigEndian.PutUint16(data[4:6], pktLen)

	secOffset := primaryHeaderLen
	binary.BigEndian.PutUint64(data[secOffset:secOffset+8], s.timestamp)
	data[secOffset+8] = 1
	data[secOffset+9] = 2
	data[secOffset+10] = 1
	data[secOffset+11] = 0
	binary.BigEndian.PutUint16(data[secOffset+12:secOffset+14], 0)

	payloadOffset := primaryHeaderLen + secondaryHeaderLen
	baseAnode := 1500.0 + s.randGen.Float64()*200
	baseGrid := 0.15 + s.randGen.Float64()*0.05
	baseFlow := 1.5e-6 + s.randGen.Float64()*0.3e-6

	for i := 0; i < sampleCount; i++ {
		off := payloadOffset + i*sampleSize

		anodeRaw := uint32((baseAnode + s.randGen.Float64()*10) * 1e6)
		binary.BigEndian.PutUint32(data[off:off+4], anodeRaw)

		gridRaw := uint32((baseGrid + s.randGen.Float64()*0.005) * 1e9)
		binary.BigEndian.PutUint32(data[off+4:off+8], gridRaw)

		flowRaw := uint32((baseFlow + s.randGen.Float64()*0.1e-6) * 1e12)
		binary.BigEndian.PutUint32(data[off+8:off+12], flowRaw)

		data[off+12] = 0x80
	}

	checksumStart := primaryHeaderLen + secondaryHeaderLen - 2
	calcChecksum := calculateChecksum(data[:checksumStart])
	calcChecksum = addChecksum(calcChecksum, data[payloadOffset:payloadOffset+payloadLen])
	binary.BigEndian.PutUint16(data[checksumStart:checksumStart+2], ^uint16(calcChecksum))

	return &Packet{
		Data:      data,
		Timestamp: time.Now(),
		Length:    totalLen,
	}
}

func calculateChecksum(data []byte) uint32 {
	var sum uint32
	for i := 0; i < len(data); i += 2 {
		if i+1 < len(data) {
			sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
		} else {
			sum += uint32(data[i]) << 8
		}
	}
	return sum
}

func addChecksum(base uint32, data []byte) uint32 {
	sum := base
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
	return sum
}
