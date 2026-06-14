package capture

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/cubesat/mems-thruster-gateway/internal/ccsds"
	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

type Gateway struct {
	source   PacketSource
	decoder  *ccsds.PacketDecoder
	running  atomic.Bool
	stats    GatewayStats
	mu       sync.Mutex

	sampleCallback   func([]types.ThrusterSample)
	packetCallback   func(*ccsds.DecodedPacket)
	errorCallback    func(error)

	ifaceName       string
	bpfFilter       string
	pollInterval    time.Duration

	decodedSamples  uint64
	droppedPackets  uint64
	seqErrors       uint64
}

type GatewayStats struct {
	PacketsReceived  uint64
	PacketsDecoded   uint64
	PacketsDropped   uint64
	SamplesDecoded   uint64
	ChecksumErrors   uint64
	SequenceErrors   uint64
	BytesProcessed   uint64
}

func NewGateway(iface string, bpfFilter string) *Gateway {
	return &Gateway{
		source:       NewPacketSource(),
		decoder:      ccsds.NewPacketDecoder(),
		ifaceName:    iface,
		bpfFilter:    bpfFilter,
		pollInterval: 10 * time.Microsecond,
	}
}

func (g *Gateway) SetSampleCallback(cb func([]types.ThrusterSample)) {
	g.sampleCallback = cb
}

func (g *Gateway) SetPacketCallback(cb func(*ccsds.DecodedPacket)) {
	g.packetCallback = cb
}

func (g *Gateway) SetErrorCallback(cb func(error)) {
	g.errorCallback = cb
}

func (g *Gateway) Start() error {
	if err := g.source.Open(g.ifaceName); err != nil {
		return err
	}

	if g.bpfFilter != "" {
		_ = g.source.SetBPFFilter(g.bpfFilter)
	}

	g.running.Store(true)
	go g.pollLoop()

	return nil
}

func (g *Gateway) Stop() {
	g.running.Store(false)
	_ = g.source.Close()
}

func (g *Gateway) pollLoop() {
	var lastSeq uint16
	var seqInitialized bool

	buf := make([]byte, 65536)
	_ = buf

	for g.running.Load() {
		pkt, err := g.source.ReadPacket(100 * time.Microsecond)
		if err != nil {
			if err == ErrTimeout {
				continue
			}
			if err == ErrClosed {
				break
			}
			if g.errorCallback != nil {
				g.errorCallback(err)
			}
			continue
		}

		atomic.AddUint64(&g.stats.PacketsReceived, 1)
		atomic.AddUint64(&g.stats.BytesProcessed, uint64(pkt.Length))

		decoded, err := g.decoder.Decode(pkt.Data)
		if err != nil {
			atomic.AddUint64(&g.stats.PacketsDropped, 1)
			if err == ccsds.ErrChecksumMismatch {
				atomic.AddUint64(&g.stats.ChecksumErrors, 1)
			}
			continue
		}

		atomic.AddUint64(&g.stats.PacketsDecoded, 1)

		if !seqInitialized {
			lastSeq = decoded.PrimaryHeader.SequenceCount
			seqInitialized = true
		} else {
			expectedSeq := (lastSeq + 1) & 0x3FFF
			if decoded.PrimaryHeader.SequenceCount != expectedSeq {
				atomic.AddUint64(&g.stats.SequenceErrors, 1)
			}
			lastSeq = decoded.PrimaryHeader.SequenceCount
		}

		if g.packetCallback != nil {
			g.packetCallback(decoded)
		}

		if decoded.PrimaryHeader.SecondaryHeader && decoded.SecondaryHeader.PacketType == 1 {
			samples := g.decodeThrusterPayload(decoded)
			if len(samples) > 0 {
				atomic.AddUint64(&g.stats.SamplesDecoded, uint64(len(samples)))
				if g.sampleCallback != nil {
					g.sampleCallback(samples)
				}
			}
		}
	}
}

func (g *Gateway) decodeThrusterPayload(pkt *ccsds.DecodedPacket) []types.ThrusterSample {
	payload := pkt.Payload
	if len(payload) < 13 {
		return nil
	}

	sampleSize := 13
	sampleCount := len(payload) / sampleSize
	if sampleCount == 0 {
		return nil
	}

	baseTimestamp := pkt.SecondaryHeader.Timestamp
	seqStart := uint64(pkt.PrimaryHeader.SequenceCount) * uint64(sampleCount)

	samples, err := ccsds.DecodeThrusterSamplesFloat(
		payload[:sampleCount*sampleSize],
		baseTimestamp,
		seqStart,
		sampleCount,
	)
	if err != nil {
		return nil
	}

	return samples
}

func (g *Gateway) Stats() GatewayStats {
	srcStats := g.source.Stats()
	return GatewayStats{
		PacketsReceived:  atomic.LoadUint64(&g.stats.PacketsReceived),
		PacketsDecoded:   atomic.LoadUint64(&g.stats.PacketsDecoded),
		PacketsDropped:   atomic.LoadUint64(&g.stats.PacketsDropped),
		SamplesDecoded:   atomic.LoadUint64(&g.stats.SamplesDecoded),
		ChecksumErrors:   atomic.LoadUint64(&g.stats.ChecksumErrors),
		SequenceErrors:   atomic.LoadUint64(&g.stats.SequenceErrors),
		BytesProcessed:   srcStats.BytesReceived,
	}
}
