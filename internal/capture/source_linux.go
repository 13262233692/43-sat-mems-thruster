//go:build linux

package capture

import (
	"encoding/binary"
	"syscall"
	"time"
	"unsafe"
)

type rawSocketSource struct {
	fd      int
	ifIndex int
	iface   string
	stats   CaptureStats
}

func newPlatformPacketSource() PacketSource {
	return &rawSocketSource{
		fd: -1,
	}
}

func (r *rawSocketSource) Open(iface string) error {
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, syscall.ETH_P_ALL)
	if err != nil {
		return err
	}
	r.fd = fd
	r.iface = iface

	if err := r.bindToInterface(iface); err != nil {
		syscall.Close(fd)
		r.fd = -1
		return err
	}

	return nil
}

func (r *rawSocketSource) bindToInterface(iface string) error {
	ifaceInfo, err := net.InterfaceByName(iface)
	if err != nil {
		return err
	}
	r.ifIndex = ifaceInfo.Index

	addr := syscall.SockaddrLinklayer{
		Protocol: syscall.ETH_P_ALL,
		Ifindex:  r.ifIndex,
	}

	return syscall.Bind(r.fd, &addr)
}

func (r *rawSocketSource) Close() error {
	if r.fd >= 0 {
		err := syscall.Close(r.fd)
		r.fd = -1
		return err
	}
	return nil
}

func (r *rawSocketSource) ReadPacket(timeout time.Duration) (*Packet, error) {
	buf := make([]byte, 65536)

	tv := syscall.Timeval{}
	if timeout > 0 {
		tv.Sec = int64(timeout.Seconds())
		tv.Usec = int64(timeout.Nanoseconds() / 1000 % 1000000)
	}

	for {
		rfds := &syscall.FdSet{}
		rfds.Bits[r.fd/64] |= 1 << (r.fd % 64)

		n, err := syscall.Select(r.fd+1, rfds, nil, nil, &tv)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return nil, err
		}
		if n == 0 {
			return nil, ErrTimeout
		}

		break
	}

	n, _, err := syscall.Recvfrom(r.fd, buf, 0)
	if err != nil {
		r.stats.Errors++
		return nil, err
	}

	pkt := &Packet{
		Data:      buf[:n],
		Timestamp: time.Now(),
		Length:    n,
	}

	r.stats.PacketsReceived++
	r.stats.BytesReceived += uint64(n)

	return pkt, nil
}

func (r *rawSocketSource) SetBPFFilter(filter string) error {
	if filter == "" {
		return nil
	}
	return ErrNotSupported
}

func (r *rawSocketSource) Stats() CaptureStats {
	return r.stats
}

type tpacketHdr struct {
	TpStatus  uint32
	TpLen     uint32
	TpSnaplen uint32
	TpMac     uint16
	TpNet     uint16
	TpSec     uint32
	TpUsec    uint32
}

func htons(val uint16) uint16 {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, val)
	return *(*uint16)(unsafe.Pointer(&b[0]))
}
