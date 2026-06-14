package lockfree

import (
	"sync/atomic"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

type SPSCRing struct {
	_         [0]uint64
	capacity  uint64
	mask      uint64
	_         [0]uint64

	writeCache uint64
	readPos    uint64
	_pad1      [7]uint64

	readCache  uint64
	writePos   uint64
	_pad2      [7]uint64

	buffer []types.ThrusterSample
}

func NewSPSCRing(capacity int) *SPSCRing {
	actualCap := 1
	for actualCap < capacity {
		actualCap <<= 1
	}

	return &SPSCRing{
		capacity:   uint64(actualCap),
		mask:       uint64(actualCap - 1),
		buffer:     make([]types.ThrusterSample, actualCap),
		writeCache: 0,
		readCache:  0,
	}
}

func (r *SPSCRing) Capacity() int {
	return int(r.capacity)
}

func (r *SPSCRing) Write(sample types.ThrusterSample) bool {
	writePos := atomic.LoadUint64(&r.writePos)
	nextPos := writePos + 1

	if nextPos-r.readCache > r.capacity {
		r.readCache = atomic.LoadUint64(&r.readPos)
		if nextPos-r.readCache > r.capacity {
			return false
		}
	}

	r.buffer[writePos&r.mask] = sample

	atomic.StoreUint64(&r.writePos, nextPos)
	return true
}

func (r *SPSCRing) WriteBatch(samples []types.ThrusterSample) int {
	writePos := atomic.LoadUint64(&r.writePos)
	readPos := atomic.LoadUint64(&r.readPos)

	available := r.capacity - (writePos - readPos)
	n := len(samples)
	if uint64(n) > available {
		n = int(available)
	}

	for i := 0; i < n; i++ {
		idx := (writePos + uint64(i)) & r.mask
		r.buffer[idx] = samples[i]
	}

	atomic.StoreUint64(&r.writePos, writePos+uint64(n))
	return n
}

func (r *SPSCRing) Read() (types.ThrusterSample, bool) {
	readPos := atomic.LoadUint64(&r.readPos)
	writePos := atomic.LoadUint64(&r.writePos)

	if readPos >= writePos {
		return types.ThrusterSample{}, false
	}

	idx := readPos & r.mask
	sample := r.buffer[idx]

	atomic.StoreUint64(&r.readPos, readPos+1)
	return sample, true
}

func (r *SPSCRing) ReadBatch(out []types.ThrusterSample) int {
	readPos := atomic.LoadUint64(&r.readPos)
	writePos := atomic.LoadUint64(&r.writePos)

	count := writePos - readPos
	if count == 0 {
		return 0
	}

	n := len(out)
	if uint64(n) > count {
		n = int(count)
	}

	for i := 0; i < n; i++ {
		idx := (readPos + uint64(i)) & r.mask
		out[i] = r.buffer[idx]
	}

	atomic.StoreUint64(&r.readPos, readPos+uint64(n))
	return n
}

func (r *SPSCRing) Peek(out []types.ThrusterSample) int {
	readPos := atomic.LoadUint64(&r.readPos)
	writePos := atomic.LoadUint64(&r.writePos)

	count := writePos - readPos
	if count == 0 {
		return 0
	}

	n := len(out)
	if uint64(n) > count {
		n = int(count)
	}

	for i := 0; i < n; i++ {
		idx := (readPos + uint64(i)) & r.mask
		out[i] = r.buffer[idx]
	}

	return n
}

func (r *SPSCRing) PeekLatest(n int) []types.ThrusterSample {
	if n <= 0 {
		return nil
	}

	writePos := atomic.LoadUint64(&r.writePos)
	readPos := atomic.LoadUint64(&r.readPos)

	count := writePos - readPos
	if count == 0 {
		return nil
	}

	if uint64(n) > count {
		n = int(count)
	}

	result := make([]types.ThrusterSample, n)
	start := writePos - uint64(n)

	for i := 0; i < n; i++ {
		idx := (start + uint64(i)) & r.mask
		result[i] = r.buffer[idx]
	}

	return result
}

func (r *SPSCRing) Count() int {
	writePos := atomic.LoadUint64(&r.writePos)
	readPos := atomic.LoadUint64(&r.readPos)
	return int(writePos - readPos)
}

func (r *SPSCRing) IsEmpty() bool {
	return r.Count() == 0
}

func (r *SPSCRing) IsFull() bool {
	return r.Count() >= int(r.capacity)
}

func (r *SPSCRing) Clear() {
	atomic.StoreUint64(&r.writePos, 0)
	atomic.StoreUint64(&r.readPos, 0)
	r.writeCache = 0
	r.readCache = 0
}

type SPSCByteRing struct {
	_         [0]uint64
	capacity  uint64
	mask      uint64
	_         [0]uint64

	writeCache uint64
	readPos    uint64
	_pad1      [7]uint64

	readCache  uint64
	writePos   uint64
	_pad2      [7]uint64

	buffer []byte
}

func NewSPSCByteRing(capacity int) *SPSCByteRing {
	actualCap := 1
	for actualCap < capacity {
		actualCap <<= 1
	}

	return &SPSCByteRing{
		capacity:   uint64(actualCap),
		mask:       uint64(actualCap - 1),
		buffer:     make([]byte, actualCap),
		writeCache: 0,
		readCache:  0,
	}
}

func (r *SPSCByteRing) Write(data []byte) int {
	writePos := atomic.LoadUint64(&r.writePos)

	if writePos-r.readCache >= r.capacity {
		r.readCache = atomic.LoadUint64(&r.readPos)
		if writePos-r.readCache >= r.capacity {
			return 0
		}
	}

	available := r.capacity - (writePos - r.readCache)
	n := len(data)
	if uint64(n) > available {
		n = int(available)
	}

	for i := 0; i < n; i++ {
		idx := (writePos + uint64(i)) & r.mask
		r.buffer[idx] = data[i]
	}

	atomic.StoreUint64(&r.writePos, writePos+uint64(n))
	return n
}

func (r *SPSCByteRing) Read(out []byte) int {
	readPos := atomic.LoadUint64(&r.readPos)
	writePos := atomic.LoadUint64(&r.writePos)

	count := writePos - readPos
	if count == 0 {
		return 0
	}

	n := len(out)
	if uint64(n) > count {
		n = int(count)
	}

	for i := 0; i < n; i++ {
		idx := (readPos + uint64(i)) & r.mask
		out[i] = r.buffer[idx]
	}

	atomic.StoreUint64(&r.readPos, readPos+uint64(n))
	return n
}

func (r *SPSCByteRing) Count() int {
	writePos := atomic.LoadUint64(&r.writePos)
	readPos := atomic.LoadUint64(&r.readPos)
	return int(writePos - readPos)
}

func (r *SPSCByteRing) Capacity() int {
	return int(r.capacity)
}

func (r *SPSCByteRing) Clear() {
	atomic.StoreUint64(&r.writePos, 0)
	atomic.StoreUint64(&r.readPos, 0)
	r.writeCache = 0
	r.readCache = 0
}

type SlotRing struct {
	capacity uint64
	mask     uint64

	_pad0 [8]uint64
	write  uint64
	_pad1  [8]uint64
	read   uint64
	_pad2  [8]uint64

	slots []slotEntry
}

type slotEntry struct {
	seq  uint64
	data types.ThrusterSample
	_pad [56]byte
}

func NewSlotRing(capacity int) *SlotRing {
	actualCap := 1
	for actualCap < capacity {
		actualCap <<= 1
	}

	ring := &SlotRing{
		capacity: uint64(actualCap),
		mask:     uint64(actualCap - 1),
		slots:    make([]slotEntry, actualCap),
	}

	for i := range ring.slots {
		ring.slots[i].seq = uint64(i)
	}

	return ring
}

func (r *SlotRing) Capacity() int {
	return int(r.capacity)
}

func (r *SlotRing) Write(sample types.ThrusterSample) bool {
	writePos := atomic.LoadUint64(&r.write)
	idx := writePos & r.mask
	seq := atomic.LoadUint64(&r.slots[idx].seq)

	if seq != writePos {
		return false
	}

	r.slots[idx].data = sample
	atomic.StoreUint64(&r.slots[idx].seq, writePos+1)

	atomic.StoreUint64(&r.write, writePos+1)
	return true
}

func (r *SlotRing) Read() (types.ThrusterSample, bool) {
	readPos := atomic.LoadUint64(&r.read)
	idx := readPos & r.mask
	seq := atomic.LoadUint64(&r.slots[idx].seq)

	if seq != readPos+1 {
		return types.ThrusterSample{}, false
	}

	data := r.slots[idx].data
	atomic.StoreUint64(&r.slots[idx].seq, readPos+r.capacity)

	atomic.StoreUint64(&r.read, readPos+1)
	return data, true
}

func (r *SlotRing) Count() int {
	writePos := atomic.LoadUint64(&r.write)
	readPos := atomic.LoadUint64(&r.read)
	return int(writePos - readPos)
}

func (r *SlotRing) IsEmpty() bool {
	return r.Count() == 0
}

func (r *SlotRing) IsFull() bool {
	return r.Count() >= int(r.capacity)
}
