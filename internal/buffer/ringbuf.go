package buffer

import (
	"sync"
	"sync/atomic"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

type RingBuffer struct {
	buffer   []types.ThrusterSample
	capacity int
	mask     int
	writeIdx uint64
	readIdx  uint64
	count    uint64

	mu     sync.RWMutex
	waitCh chan struct{}
	closed atomic.Bool
}

func NewRingBuffer(capacity int) *RingBuffer {
	actualCap := 1
	for actualCap < capacity {
		actualCap <<= 1
	}

	return &RingBuffer{
		buffer:   make([]types.ThrusterSample, actualCap),
		capacity: actualCap,
		mask:     actualCap - 1,
		waitCh:   make(chan struct{}, 1),
	}
}

func (rb *RingBuffer) Write(samples []types.ThrusterSample) int {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.closed.Load() {
		return 0
	}

	n := len(samples)
	if n == 0 {
		return 0
	}

	available := rb.capacity - int(rb.count)
	if n > available {
		n = available
	}

	for i := 0; i < n; i++ {
		idx := int(rb.writeIdx) & rb.mask
		rb.buffer[idx] = samples[i]
		rb.writeIdx++
	}

	rb.count += uint64(n)

	select {
	case rb.waitCh <- struct{}{}:
	default:
	}

	return n
}

func (rb *RingBuffer) WriteSingle(sample types.ThrusterSample) bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.closed.Load() {
		return false
	}

	if rb.count >= uint64(rb.capacity) {
		return false
	}

	idx := int(rb.writeIdx) & rb.mask
	rb.buffer[idx] = sample
	rb.writeIdx++
	rb.count++

	select {
	case rb.waitCh <- struct{}{}:
	default:
	}

	return true
}

func (rb *RingBuffer) Read(out []types.ThrusterSample) int {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.count == 0 {
		return 0
	}

	n := len(out)
	if n > int(rb.count) {
		n = int(rb.count)
	}

	for i := 0; i < n; i++ {
		idx := int(rb.readIdx) & rb.mask
		out[i] = rb.buffer[idx]
		rb.readIdx++
	}

	rb.count -= uint64(n)
	return n
}

func (rb *RingBuffer) Peek(out []types.ThrusterSample) int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 {
		return 0
	}

	n := len(out)
	if n > int(rb.count) {
		n = int(rb.count)
	}

	readIdx := rb.readIdx
	for i := 0; i < n; i++ {
		idx := int(readIdx) & rb.mask
		out[i] = rb.buffer[idx]
		readIdx++
	}

	return n
}

func (rb *RingBuffer) PeekLatest(n int) []types.ThrusterSample {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 {
		return nil
	}

	if n > int(rb.count) {
		n = int(rb.count)
	}

	result := make([]types.ThrusterSample, n)
	startIdx := rb.writeIdx - uint64(n)

	for i := 0; i < n; i++ {
		idx := int(startIdx+uint64(i)) & rb.mask
		result[i] = rb.buffer[idx]
	}

	return result
}

func (rb *RingBuffer) Count() int {
	return int(atomic.LoadUint64(&rb.count))
}

func (rb *RingBuffer) Capacity() int {
	return rb.capacity
}

func (rb *RingBuffer) IsFull() bool {
	return int(atomic.LoadUint64(&rb.count)) >= rb.capacity
}

func (rb *RingBuffer) IsEmpty() bool {
	return atomic.LoadUint64(&rb.count) == 0
}

func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.writeIdx = 0
	rb.readIdx = 0
	rb.count = 0
}

func (rb *RingBuffer) Close() {
	rb.closed.Store(true)
	select {
	case rb.waitCh <- struct{}{}:
	default:
	}
}

func (rb *RingBuffer) WaitForData() chan struct{} {
	return rb.waitCh
}

type StreamProcessor struct {
	buffer   *RingBuffer
	windowSize int
	stepSize   int

	callback func([]types.ThrusterSample)

	running atomic.Bool
	closeCh chan struct{}
}

func NewStreamProcessor(bufferSize int, windowSize int, stepSize int) *StreamProcessor {
	return &StreamProcessor{
		buffer:     NewRingBuffer(bufferSize),
		windowSize: windowSize,
		stepSize:   stepSize,
		closeCh:    make(chan struct{}),
	}
}

func (sp *StreamProcessor) SetCallback(cb func([]types.ThrusterSample)) {
	sp.callback = cb
}

func (sp *StreamProcessor) Start() {
	sp.running.Store(true)
	go sp.processLoop()
}

func (sp *StreamProcessor) Stop() {
	sp.running.Store(false)
	sp.buffer.Close()
	close(sp.closeCh)
}

func (sp *StreamProcessor) Write(samples []types.ThrusterSample) int {
	return sp.buffer.Write(samples)
}

func (sp *StreamProcessor) processLoop() {
	windowBuf := make([]types.ThrusterSample, sp.windowSize)

	for sp.running.Load() {
		if sp.buffer.Count() < sp.windowSize {
			select {
			case <-sp.buffer.WaitForData():
				continue
			case <-sp.closeCh:
				return
			}
		}

		for sp.buffer.Count() >= sp.windowSize {
			n := sp.buffer.Read(windowBuf)
			if n < sp.windowSize {
				break
			}

			if sp.callback != nil {
				sp.callback(windowBuf[:n])
			}
		}
	}
}

type DoubleBuffer struct {
	front []types.ThrusterSample
	back  []types.ThrusterSample
	size  int

	writeIdx int
	swapped  chan struct{}

	mu sync.Mutex
}

func NewDoubleBuffer(size int) *DoubleBuffer {
	return &DoubleBuffer{
		front:   make([]types.ThrusterSample, size),
		back:    make([]types.ThrusterSample, size),
		size:    size,
		swapped: make(chan struct{}, 1),
	}
}

func (db *DoubleBuffer) Write(sample types.ThrusterSample) bool {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.writeIdx >= db.size {
		return false
	}

	db.back[db.writeIdx] = sample
	db.writeIdx++

	if db.writeIdx >= db.size {
		db.front, db.back = db.back, db.front
		db.writeIdx = 0
		select {
		case db.swapped <- struct{}{}:
		default:
		}
	}

	return true
}

func (db *DoubleBuffer) WriteMany(samples []types.ThrusterSample) int {
	db.mu.Lock()
	defer db.mu.Unlock()

	written := 0
	for _, s := range samples {
		if db.writeIdx >= db.size {
			db.front, db.back = db.back, db.front
			db.writeIdx = 0
			select {
			case db.swapped <- struct{}{}:
			default:
			}
		}
		db.back[db.writeIdx] = s
		db.writeIdx++
		written++
	}

	return written
}

func (db *DoubleBuffer) ReadFront() []types.ThrusterSample {
	return db.front
}

func (db *DoubleBuffer) SwapNotify() chan struct{} {
	return db.swapped
}

func (db *DoubleBuffer) Size() int {
	return db.size
}
