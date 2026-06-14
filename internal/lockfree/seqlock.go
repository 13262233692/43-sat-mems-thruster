package lockfree

import (
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

type SeqLockRing struct {
	buffer   []types.ThrusterSample
	capacity uint64
	mask     uint64

	seqHead uint64
	seqTail uint64

	writeMu sync.Mutex

	_padding1 [64]byte
	_padding2 [64]byte
}

func NewSeqLockRing(capacity int) *SeqLockRing {
	actualCap := 1
	for actualCap < capacity {
		actualCap <<= 1
	}

	return &SeqLockRing{
		buffer:   make([]types.ThrusterSample, actualCap),
		capacity: uint64(actualCap),
		mask:     uint64(actualCap - 1),
	}
}

func (s *SeqLockRing) Capacity() int {
	return int(s.capacity)
}

func (s *SeqLockRing) Count() int {
	head := atomic.LoadUint64(&s.seqHead)
	tail := atomic.LoadUint64(&s.seqTail)
	return int(head - tail)
}

func (s *SeqLockRing) IsEmpty() bool {
	return s.Count() == 0
}

func (s *SeqLockRing) IsFull() bool {
	return s.Count() >= int(s.capacity)
}

func (s *SeqLockRing) Write(samples []types.ThrusterSample) int {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	head := atomic.LoadUint64(&s.seqHead)
	tail := atomic.LoadUint64(&s.seqTail)
	count := head - tail

	available := s.capacity - count
	n := len(samples)
	if uint64(n) > available {
		n = int(available)
	}

	for i := 0; i < n; i++ {
		idx := (head + uint64(i)) & s.mask
		s.buffer[idx] = samples[i]
	}

	atomic.StoreUint64(&s.seqHead, head+uint64(n))

	return n
}

func (s *SeqLockRing) WriteSingle(sample types.ThrusterSample) bool {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	head := atomic.LoadUint64(&s.seqHead)
	tail := atomic.LoadUint64(&s.seqTail)

	if head-tail >= s.capacity {
		return false
	}

	idx := head & s.mask
	s.buffer[idx] = sample

	atomic.StoreUint64(&s.seqHead, head+1)

	return true
}

func (s *SeqLockRing) Read(out []types.ThrusterSample) int {
	for {
		head := atomic.LoadUint64(&s.seqHead)
		tail := atomic.LoadUint64(&s.seqTail)

		if head == tail {
			return 0
		}

		count := head - tail
		n := len(out)
		if uint64(n) > count {
			n = int(count)
		}

		for i := 0; i < n; i++ {
			idx := (tail + uint64(i)) & s.mask
			out[i] = s.buffer[idx]
		}

		headAfter := atomic.LoadUint64(&s.seqHead)

		if head == headAfter {
			newTail := tail + uint64(n)
			atomic.StoreUint64(&s.seqTail, newTail)
			return n
		}

		runtime.Gosched()
	}
}

func (s *SeqLockRing) Peek(out []types.ThrusterSample) int {
	for {
		head := atomic.LoadUint64(&s.seqHead)
		tail := atomic.LoadUint64(&s.seqTail)

		if head == tail {
			return 0
		}

		count := head - tail
		n := len(out)
		if uint64(n) > count {
			n = int(count)
		}

		for i := 0; i < n; i++ {
			idx := (tail + uint64(i)) & s.mask
			out[i] = s.buffer[idx]
		}

		headAfter := atomic.LoadUint64(&s.seqHead)
		tailAfter := atomic.LoadUint64(&s.seqTail)

		if head == headAfter && tail == tailAfter {
			return n
		}

		runtime.Gosched()
	}
}

func (s *SeqLockRing) PeekLatest(n int) []types.ThrusterSample {
	if n <= 0 {
		return nil
	}

	for {
		head := atomic.LoadUint64(&s.seqHead)
		tail := atomic.LoadUint64(&s.seqTail)

		count := head - tail
		if count == 0 {
			return nil
		}

		if uint64(n) > count {
			n = int(count)
		}

		result := make([]types.ThrusterSample, n)
		startIdx := head - uint64(n)

		for i := 0; i < n; i++ {
			idx := (startIdx + uint64(i)) & s.mask
			result[i] = s.buffer[idx]
		}

		headAfter := atomic.LoadUint64(&s.seqHead)
		if head == headAfter {
			return result
		}

		runtime.Gosched()
	}
}

func (s *SeqLockRing) Clear() {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	atomic.StoreUint64(&s.seqHead, 0)
	atomic.StoreUint64(&s.seqTail, 0)
}

type DoubleSeqLock struct {
	front []types.ThrusterSample
	back  []types.ThrusterSample
	size  int

	seq   uint64
	writeIdx int

	writeMu sync.Mutex

	swapCh chan struct{}
}

func NewDoubleSeqLock(size int) *DoubleSeqLock {
	return &DoubleSeqLock{
		front:  make([]types.ThrusterSample, size),
		back:   make([]types.ThrusterSample, size),
		size:   size,
		swapCh: make(chan struct{}, 1),
	}
}

func (d *DoubleSeqLock) Write(sample types.ThrusterSample) bool {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	if d.writeIdx >= d.size {
		return false
	}

	d.back[d.writeIdx] = sample
	d.writeIdx++

	if d.writeIdx >= d.size {
		atomic.AddUint64(&d.seq, 1)
		d.front, d.back = d.back, d.front
		d.writeIdx = 0

		select {
		case d.swapCh <- struct{}{}:
		default:
		}
	}

	return true
}

func (d *DoubleSeqLock) WriteMany(samples []types.ThrusterSample) int {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	written := 0
	for _, s := range samples {
		if d.writeIdx >= d.size {
			atomic.AddUint64(&d.seq, 1)
			d.front, d.back = d.back, d.front
			d.writeIdx = 0

			select {
			case d.swapCh <- struct{}{}:
			default:
			}
		}
		d.back[d.writeIdx] = s
		d.writeIdx++
		written++
	}

	return written
}

func (d *DoubleSeqLock) ReadFront() []types.ThrusterSample {
	seq := atomic.LoadUint64(&d.seq)

	data := make([]types.ThrusterSample, d.size)
	copy(data, d.front)

	seqAfter := atomic.LoadUint64(&d.seq)

	if seq == seqAfter && seq%2 == 0 {
		return data
	}

	return d.ReadFront()
}

func (d *DoubleSeqLock) SwapNotify() chan struct{} {
	return d.swapCh
}

func (d *DoubleSeqLock) Size() int {
	return d.size
}

type MPSeqRing struct {
	buffer   []types.ThrusterSample
	capacity uint64
	mask     uint64

	head    uint64
	tail    uint64

	writeLock uint32
	readLock  uint32
}

func NewMPSeqRing(capacity int) *MPSeqRing {
	actualCap := 1
	for actualCap < capacity {
		actualCap <<= 1
	}

	return &MPSeqRing{
		buffer:   make([]types.ThrusterSample, actualCap),
		capacity: uint64(actualCap),
		mask:     uint64(actualCap - 1),
	}
}

func (r *MPSeqRing) tryAcquireWrite() bool {
	return atomic.CompareAndSwapUint32(&r.writeLock, 0, 1)
}

func (r *MPSeqRing) releaseWrite() {
	atomic.StoreUint32(&r.writeLock, 0)
}

func (r *MPSeqRing) tryAcquireRead() bool {
	return atomic.CompareAndSwapUint32(&r.readLock, 0, 1)
}

func (r *MPSeqRing) releaseRead() {
	atomic.StoreUint32(&r.readLock, 0)
}

func (r *MPSeqRing) Write(sample types.ThrusterSample) bool {
	for !r.tryAcquireWrite() {
		runtime.Gosched()
	}
	defer r.releaseWrite()

	head := atomic.LoadUint64(&r.head)
	tail := atomic.LoadUint64(&r.tail)

	if head-tail >= r.capacity {
		return false
	}

	idx := head & r.mask
	r.buffer[idx] = sample

	atomic.StoreUint64(&r.head, head+1)
	return true
}

func (r *MPSeqRing) Read() (types.ThrusterSample, bool) {
	for !r.tryAcquireRead() {
		runtime.Gosched()
	}
	defer r.releaseRead()

	head := atomic.LoadUint64(&r.head)
	tail := atomic.LoadUint64(&r.tail)

	if head == tail {
		return types.ThrusterSample{}, false
	}

	idx := tail & r.mask
	sample := r.buffer[idx]

	atomic.StoreUint64(&r.tail, tail+1)
	return sample, true
}

func (r *MPSeqRing) Count() int {
	head := atomic.LoadUint64(&r.head)
	tail := atomic.LoadUint64(&r.tail)
	return int(head - tail)
}

func (r *MPSeqRing) Capacity() int {
	return int(r.capacity)
}
