package lockfree

import (
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

const (
	HazardPointersPerThread = 2
	MaxRetiredNodes         = 256
)

type hpNode struct {
	value types.ThrusterSample
	next  unsafe.Pointer
}

type HazardDomain struct {
	mu sync.Mutex

	threadHazards []*threadHazardPointers
	retiredList   []*hpNode
	retireCount   int
}

type threadHazardPointers struct {
	hazards [HazardPointersPerThread]unsafe.Pointer
	active  bool
}

var globalHazardDomain = NewHazardDomain()

func NewHazardDomain() *HazardDomain {
	return &HazardDomain{
		threadHazards: make([]*threadHazardPointers, 0),
		retiredList:   make([]*hpNode, 0, MaxRetiredNodes),
	}
}

func (d *HazardDomain) acquireThread() *threadHazardPointers {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, thp := range d.threadHazards {
		if !thp.active {
			thp.active = true
			return thp
		}
	}

	thp := &threadHazardPointers{active: true}
	d.threadHazards = append(d.threadHazards, thp)
	return thp
}

func (d *HazardDomain) releaseThread(thp *threadHazardPointers) {
	for i := range thp.hazards {
		atomic.StorePointer(&thp.hazards[i], nil)
	}
	thp.active = false
}

func (d *HazardDomain) setHazard(index int, ptr unsafe.Pointer) {
	atomic.StorePointer(&getThreadHazards().hazards[index], ptr)
}

var threadHazardPool = sync.Pool{
	New: func() interface{} {
		return &threadHazardPointers{active: true}
	},
}

func getThreadHazards() *threadHazardPointers {
	thp := globalHazardDomain.acquireThread()
	return thp
}

func putThreadHazards(thp *threadHazardPointers) {
	globalHazardDomain.releaseThread(thp)
}

func (d *HazardDomain) retireNode(node *hpNode) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.retiredList = append(d.retiredList, node)
	d.retireCount++

	if len(d.retiredList) >= MaxRetiredNodes {
		d.scanAndReclaim()
	}
}

func (d *HazardDomain) scanAndReclaim() {
	hazardSet := make(map[unsafe.Pointer]struct{})

	for _, thp := range d.threadHazards {
		if thp.active {
			for i := 0; i < HazardPointersPerThread; i++ {
				ptr := atomic.LoadPointer(&thp.hazards[i])
				if ptr != nil {
					hazardSet[ptr] = struct{}{}
				}
			}
		}
	}

	newRetired := d.retiredList[:0]
	for _, node := range d.retiredList {
		ptr := unsafe.Pointer(node)
		if _, hazardous := hazardSet[ptr]; !hazardous {
		} else {
			newRetired = append(newRetired, node)
		}
	}
	d.retiredList = newRetired
}

type HPRing struct {
	buffer   []hpNodeSlot
	capacity uint64
	mask     uint64

	head uint64
	tail uint64

	domain *HazardDomain

	writeLock uint32
}

type hpNodeSlot struct {
	data types.ThrusterSample
	seq  uint64
}

func NewHPRing(capacity int) *HPRing {
	actualCap := 1
	for actualCap < capacity {
		actualCap <<= 1
	}

	ring := &HPRing{
		buffer:   make([]hpNodeSlot, actualCap),
		capacity: uint64(actualCap),
		mask:     uint64(actualCap - 1),
		domain:   globalHazardDomain,
	}

	for i := range ring.buffer {
		ring.buffer[i].seq = uint64(i)
	}

	return ring
}

func (r *HPRing) Capacity() int {
	return int(r.capacity)
}

func (r *HPRing) Count() int {
	head := atomic.LoadUint64(&r.head)
	tail := atomic.LoadUint64(&r.tail)
	return int(head - tail)
}

func (r *HPRing) IsEmpty() bool {
	return r.Count() == 0
}

func (r *HPRing) IsFull() bool {
	return r.Count() >= int(r.capacity)
}

func (r *HPRing) Enqueue(sample types.ThrusterSample) bool {
	for {
		head := atomic.LoadUint64(&r.head)
		idx := head & r.mask
		seq := atomic.LoadUint64(&r.buffer[idx].seq)

		if seq == head {
			if atomic.CompareAndSwapUint64(&r.head, head, head+1) {
				r.buffer[idx].data = sample
				atomic.StoreUint64(&r.buffer[idx].seq, head+1)
				return true
			}
		} else if seq < head {
			return false
		}

		runtime.Gosched()
	}
}

func (r *HPRing) Dequeue() (types.ThrusterSample, bool) {
	for {
		tail := atomic.LoadUint64(&r.tail)
		idx := tail & r.mask
		seq := atomic.LoadUint64(&r.buffer[idx].seq)

		expected := tail + 1
		if seq == expected {
			if atomic.CompareAndSwapUint64(&r.tail, tail, tail+1) {
				data := r.buffer[idx].data
				atomic.StoreUint64(&r.buffer[idx].seq, tail+r.capacity)
				return data, true
			}
		} else if seq < expected {
			return types.ThrusterSample{}, false
		}

		runtime.Gosched()
	}
}

func (r *HPRing) EnqueueBatch(samples []types.ThrusterSample) int {
	count := 0
	for _, s := range samples {
		if r.Enqueue(s) {
			count++
		} else {
			break
		}
	}
	return count
}

func (r *HPRing) DequeueBatch(out []types.ThrusterSample) int {
	count := 0
	for i := range out {
		if sample, ok := r.Dequeue(); ok {
			out[i] = sample
			count++
		} else {
			break
		}
	}
	return count
}

type hpQueueNode struct {
	value types.ThrusterSample
	next  unsafe.Pointer
}

type HPLinkedQueue struct {
	head   unsafe.Pointer
	tail   unsafe.Pointer
	domain *HazardDomain

	enqueueCount uint64
	dequeueCount uint64
}

func NewHPLinkedQueue() *HPLinkedQueue {
	dummy := &hpQueueNode{}
	q := &HPLinkedQueue{
		head:   unsafe.Pointer(dummy),
		tail:   unsafe.Pointer(dummy),
		domain: globalHazardDomain,
	}
	return q
}

func (q *HPLinkedQueue) Enqueue(value types.ThrusterSample) {
	node := &hpQueueNode{value: value}

	for {
		tail := atomic.LoadPointer(&q.tail)
		next := atomic.LoadPointer(&((*hpQueueNode)(tail)).next)

		tail2 := atomic.LoadPointer(&q.tail)
		if tail == tail2 {
			if next == nil {
				if atomic.CompareAndSwapPointer(&((*hpQueueNode)(tail)).next, nil, unsafe.Pointer(node)) {
					atomic.CompareAndSwapPointer(&q.tail, tail, unsafe.Pointer(node))
					atomic.AddUint64(&q.enqueueCount, 1)
					return
				}
			} else {
				atomic.CompareAndSwapPointer(&q.tail, tail, next)
			}
		}

		runtime.Gosched()
	}
}

func (q *HPLinkedQueue) Dequeue() (types.ThrusterSample, bool) {
	for {
		head := atomic.LoadPointer(&q.head)
		tail := atomic.LoadPointer(&q.tail)
		next := atomic.LoadPointer(&((*hpQueueNode)(head)).next)

		head2 := atomic.LoadPointer(&q.head)
		if head == head2 {
			if head == tail {
				if next == nil {
					return types.ThrusterSample{}, false
				}
				atomic.CompareAndSwapPointer(&q.tail, tail, next)
			} else {
				value := (*hpQueueNode)(next).value
				if atomic.CompareAndSwapPointer(&q.head, head, next) {
					q.domain.retireNode((*hpNode)(head))
					atomic.AddUint64(&q.dequeueCount, 1)
					return value, true
				}
			}
		}

		runtime.Gosched()
	}
}

func (q *HPLinkedQueue) IsEmpty() bool {
	head := atomic.LoadPointer(&q.head)
	next := atomic.LoadPointer(&((*hpQueueNode)(head)).next)
	return next == nil
}

func (q *HPLinkedQueue) ApproxCount() int {
	enq := atomic.LoadUint64(&q.enqueueCount)
	deq := atomic.LoadUint64(&q.dequeueCount)
	return int(enq - deq)
}

type AtomicRing struct {
	buffer   []types.ThrusterSample
	capacity uint64
	mask     uint64

	writePos uint64
	readPos  uint64

	writeLock uint32
}

func NewAtomicRing(capacity int) *AtomicRing {
	actualCap := 1
	for actualCap < capacity {
		actualCap <<= 1
	}

	return &AtomicRing{
		buffer:   make([]types.ThrusterSample, actualCap),
		capacity: uint64(actualCap),
		mask:     uint64(actualCap - 1),
	}
}

func (r *AtomicRing) Capacity() int {
	return int(r.capacity)
}

func (r *AtomicRing) Count() int {
	write := atomic.LoadUint64(&r.writePos)
	read := atomic.LoadUint64(&r.readPos)
	return int(write - read)
}

func (r *AtomicRing) IsEmpty() bool {
	return r.Count() == 0
}

func (r *AtomicRing) IsFull() bool {
	return r.Count() >= int(r.capacity)
}

func (r *AtomicRing) TryWrite(sample types.ThrusterSample) bool {
	write := atomic.LoadUint64(&r.writePos)
	read := atomic.LoadUint64(&r.readPos)

	if write-read >= r.capacity {
		return false
	}

	idx := write & r.mask
	r.buffer[idx] = sample

	atomic.StoreUint64(&r.writePos, write+1)
	return true
}

func (r *AtomicRing) Write(sample types.ThrusterSample) bool {
	for {
		if r.TryWrite(sample) {
			return true
		}
		runtime.Gosched()
	}
}

func (r *AtomicRing) TryRead() (types.ThrusterSample, bool) {
	write := atomic.LoadUint64(&r.writePos)
	read := atomic.LoadUint64(&r.readPos)

	if write == read {
		return types.ThrusterSample{}, false
	}

	idx := read & r.mask
	sample := r.buffer[idx]

	atomic.StoreUint64(&r.readPos, read+1)
	return sample, true
}

func (r *AtomicRing) Read() (types.ThrusterSample, bool) {
	for {
		if sample, ok := r.TryRead(); ok {
			return sample, true
		}
		runtime.Gosched()
	}
}

func (r *AtomicRing) WriteBatch(samples []types.ThrusterSample) int {
	write := atomic.LoadUint64(&r.writePos)
	read := atomic.LoadUint64(&r.readPos)

	available := r.capacity - (write - read)
	n := len(samples)
	if uint64(n) > available {
		n = int(available)
	}

	for i := 0; i < n; i++ {
		idx := (write + uint64(i)) & r.mask
		r.buffer[idx] = samples[i]
	}

	atomic.StoreUint64(&r.writePos, write+uint64(n))
	return n
}

func (r *AtomicRing) ReadBatch(out []types.ThrusterSample) int {
	write := atomic.LoadUint64(&r.writePos)
	read := atomic.LoadUint64(&r.readPos)

	count := write - read
	n := len(out)
	if uint64(n) > count {
		n = int(count)
	}

	for i := 0; i < n; i++ {
		idx := (read + uint64(i)) & r.mask
		out[i] = r.buffer[idx]
	}

	atomic.StoreUint64(&r.readPos, read+uint64(n))
	return n
}

func (r *AtomicRing) PeekLatest(n int) []types.ThrusterSample {
	write := atomic.LoadUint64(&r.writePos)
	read := atomic.LoadUint64(&r.readPos)

	count := write - read
	if count == 0 {
		return nil
	}

	if uint64(n) > count {
		n = int(count)
	}

	result := make([]types.ThrusterSample, n)
	start := write - uint64(n)

	for i := 0; i < n; i++ {
		idx := (start + uint64(i)) & r.mask
		result[i] = r.buffer[idx]
	}

	return result
}
