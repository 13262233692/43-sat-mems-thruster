package capture

import (
	"errors"
	"time"
)

type Packet struct {
	Data      []byte
	Timestamp time.Time
	Length    int
}

type PacketSource interface {
	Open(iface string) error
	Close() error
	ReadPacket(timeout time.Duration) (*Packet, error)
	SetBPFFilter(filter string) error
	Stats() CaptureStats
}

type CaptureStats struct {
	PacketsReceived uint64
	PacketsDropped  uint64
	BytesReceived   uint64
	Errors          uint64
}

var (
	ErrNotSupported = errors.New("operation not supported on this platform")
	ErrTimeout      = errors.New("read timeout")
	ErrClosed       = errors.New("capture source closed")
)

func NewPacketSource() PacketSource {
	return newPlatformPacketSource()
}
