package lockfree

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

const (
	stressTestDuration = 2 * time.Second
	stressQueueSize    = 65536
)

func TestSeqLockRingBasic(t *testing.T) {
	ring := NewSeqLockRing(16)

	if ring.Capacity() != 16 {
		t.Errorf("Expected capacity 16, got %d", ring.Capacity())
	}

	if !ring.IsEmpty() {
		t.Error("Ring should be empty initially")
	}

	samples := make([]types.ThrusterSample, 5)
	for i := range samples {
		samples[i].Timestamp = uint64(i)
		samples[i].Thrust = float64(i) * 1e-6
	}

	n := ring.Write(samples)
	if n != 5 {
		t.Errorf("Expected to write 5 samples, wrote %d", n)
	}

	if ring.Count() != 5 {
		t.Errorf("Expected count 5, got %d", ring.Count())
	}

	out := make([]types.ThrusterSample, 10)
	n = ring.Read(out)
	if n != 5 {
		t.Errorf("Expected to read 5 samples, read %d", n)
	}

	for i := 0; i < n; i++ {
		if out[i].Timestamp != uint64(i) {
			t.Errorf("Sample %d timestamp mismatch: expected %d, got %d",
				i, i, out[i].Timestamp)
		}
	}
}

func TestSeqLockRingFull(t *testing.T) {
	ring := NewSeqLockRing(8)

	samples := make([]types.ThrusterSample, 12)
	for i := range samples {
		samples[i].Timestamp = uint64(i)
	}

	n := ring.Write(samples)
	if n != 8 {
		t.Errorf("Expected to write 8 samples (buffer full), wrote %d", n)
	}

	if !ring.IsFull() {
		t.Error("Ring should be full")
	}

	n = ring.Write(samples)
	if n != 0 {
		t.Errorf("Expected to write 0 when full, wrote %d", n)
	}
}

func TestSeqLockRingPeekLatest(t *testing.T) {
	ring := NewSeqLockRing(16)

	samples := make([]types.ThrusterSample, 10)
	for i := range samples {
		samples[i].Timestamp = uint64(i)
	}

	ring.Write(samples)

	latest := ring.PeekLatest(3)
	if len(latest) != 3 {
		t.Errorf("Expected 3 latest samples, got %d", len(latest))
	}

	if latest[0].Timestamp != 7 {
		t.Errorf("Expected first latest sample timestamp 7, got %d", latest[0].Timestamp)
	}

	if latest[2].Timestamp != 9 {
		t.Errorf("Expected last latest sample timestamp 9, got %d", latest[2].Timestamp)
	}
}

func TestHPRingBasic(t *testing.T) {
	ring := NewHPRing(16)

	if ring.Capacity() != 16 {
		t.Errorf("Expected capacity 16, got %d", ring.Capacity())
	}

	if !ring.IsEmpty() {
		t.Error("Ring should be empty initially")
	}

	for i := 0; i < 5; i++ {
		sample := types.ThrusterSample{
			Timestamp: uint64(i),
			Thrust:    float64(i) * 1e-6,
		}
		if !ring.Enqueue(sample) {
			t.Errorf("Failed to enqueue sample %d", i)
		}
	}

	if ring.Count() != 5 {
		t.Errorf("Expected count 5, got %d", ring.Count())
	}

	for i := 0; i < 5; i++ {
		sample, ok := ring.Dequeue()
		if !ok {
			t.Errorf("Failed to dequeue sample %d", i)
		}
		if sample.Timestamp != uint64(i) {
			t.Errorf("Sample %d timestamp mismatch: expected %d, got %d",
				i, i, sample.Timestamp)
		}
	}
}

func TestHPRingFull(t *testing.T) {
	ring := NewHPRing(8)

	for i := 0; i < 8; i++ {
		sample := types.ThrusterSample{Timestamp: uint64(i)}
		if !ring.Enqueue(sample) {
			t.Errorf("Failed to enqueue sample %d", i)
		}
	}

	if !ring.IsFull() {
		t.Error("Ring should be full")
	}

	sample := types.ThrusterSample{Timestamp: 99}
	if ring.Enqueue(sample) {
		t.Error("Should not enqueue when full")
	}
}

func TestAtomicRingBasic(t *testing.T) {
	ring := NewAtomicRing(16)

	if ring.Capacity() != 16 {
		t.Errorf("Expected capacity 16, got %d", ring.Capacity())
	}

	sample := types.ThrusterSample{Timestamp: 123, Thrust: 456e-6}
	if !ring.TryWrite(sample) {
		t.Error("Failed to write sample")
	}

	if ring.Count() != 1 {
		t.Errorf("Expected count 1, got %d", ring.Count())
	}

	read, ok := ring.TryRead()
	if !ok {
		t.Error("Failed to read sample")
	}

	if read.Timestamp != 123 {
		t.Errorf("Expected timestamp 123, got %d", read.Timestamp)
	}
}

func TestAtomicRingBatch(t *testing.T) {
	ring := NewAtomicRing(64)

	samples := make([]types.ThrusterSample, 20)
	for i := range samples {
		samples[i].Timestamp = uint64(i)
		samples[i].Thrust = float64(i) * 1e-6
	}

	n := ring.WriteBatch(samples)
	if n != 20 {
		t.Errorf("Expected to write 20 samples, wrote %d", n)
	}

	if ring.Count() != 20 {
		t.Errorf("Expected count 20, got %d", ring.Count())
	}

	out := make([]types.ThrusterSample, 15)
	n = ring.ReadBatch(out)
	if n != 15 {
		t.Errorf("Expected to read 15 samples, read %d", n)
	}

	for i := 0; i < n; i++ {
		if out[i].Timestamp != uint64(i) {
			t.Errorf("Sample %d: expected timestamp %d, got %d", i, i, out[i].Timestamp)
		}
	}
}

func TestHPLinkedQueue(t *testing.T) {
	q := NewHPLinkedQueue()

	if !q.IsEmpty() {
		t.Error("Queue should be empty initially")
	}

	sample := types.ThrusterSample{Timestamp: 123, Thrust: 456e-6}
	q.Enqueue(sample)

	if q.IsEmpty() {
		t.Error("Queue should not be empty after enqueue")
	}

	if q.ApproxCount() != 1 {
		t.Errorf("Expected count 1, got %d", q.ApproxCount())
	}

	dequeued, ok := q.Dequeue()
	if !ok {
		t.Error("Failed to dequeue")
	}

	if dequeued.Timestamp != 123 {
		t.Errorf("Expected timestamp 123, got %d", dequeued.Timestamp)
	}

	if !q.IsEmpty() {
		t.Error("Queue should be empty after dequeue")
	}
}

func TestMPSeqRingConcurrent(t *testing.T) {
	ring := NewMPSeqRing(1024)

	numWriters := 4
	numReaders := 2
	itemsPerWriter := 10000

	var wg sync.WaitGroup
	var writeErrors uint64
	var readCount uint64

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < itemsPerWriter; j++ {
				sample := types.ThrusterSample{
					Timestamp:    uint64(writerID*itemsPerWriter + j),
					AnodeVoltage: float64(writerID) * 100.0,
				}
				for !ring.Write(sample) {
					atomic.AddUint64(&writeErrors, 1)
					runtime.Gosched()
				}
			}
		}(i)
	}

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				_, ok := ring.Read()
				if ok {
					atomic.AddUint64(&readCount, 1)
				} else {
					if atomic.LoadUint64(&readCount) >= uint64(numWriters*itemsPerWriter) {
						break
					}
					runtime.Gosched()
				}
			}
		}()
	}

	wg.Wait()

	expected := numWriters * itemsPerWriter
	if int(readCount) != expected {
		t.Logf("Read %d items, expected %d", readCount, expected)
	}
}

func TestSPSCRingBasic(t *testing.T) {
	ring := NewSPSCRing(16)

	if ring.Capacity() != 16 {
		t.Errorf("Expected capacity 16, got %d", ring.Capacity())
	}

	if !ring.IsEmpty() {
		t.Error("Ring should be empty initially")
	}

	sample := types.ThrusterSample{Timestamp: 123, Thrust: 456e-6, Valid: true}
	if !ring.Write(sample) {
		t.Error("Failed to write sample")
	}

	if ring.Count() != 1 {
		t.Errorf("Expected count 1, got %d", ring.Count())
	}

	read, ok := ring.Read()
	if !ok {
		t.Error("Failed to read sample")
	}

	if read.Timestamp != 123 {
		t.Errorf("Expected timestamp 123, got %d", read.Timestamp)
	}

	if !ring.IsEmpty() {
		t.Error("Ring should be empty after read")
	}
}

func TestSPSCRingBatch(t *testing.T) {
	ring := NewSPSCRing(64)

	samples := make([]types.ThrusterSample, 20)
	for i := range samples {
		samples[i].Timestamp = uint64(i)
		samples[i].Thrust = float64(i) * 1e-6
		samples[i].Valid = true
	}

	n := ring.WriteBatch(samples)
	if n != 20 {
		t.Errorf("Expected to write 20 samples, wrote %d", n)
	}

	if ring.Count() != 20 {
		t.Errorf("Expected count 20, got %d", ring.Count())
	}

	out := make([]types.ThrusterSample, 15)
	n = ring.ReadBatch(out)
	if n != 15 {
		t.Errorf("Expected to read 15 samples, read %d", n)
	}

	for i := 0; i < n; i++ {
		if out[i].Timestamp != uint64(i) {
			t.Errorf("Sample %d: expected timestamp %d, got %d", i, i, out[i].Timestamp)
		}
	}
}

func TestSPSCRingFull(t *testing.T) {
	ring := NewSPSCRing(8)

	for i := 0; i < 8; i++ {
		sample := types.ThrusterSample{Timestamp: uint64(i)}
		if !ring.Write(sample) {
			t.Errorf("Failed to write sample %d", i)
		}
	}

	if !ring.IsFull() {
		t.Error("Ring should be full")
	}

	sample := types.ThrusterSample{Timestamp: 99}
	if ring.Write(sample) {
		t.Error("Should not write when full")
	}
}

func TestSPSCRingPeekLatest(t *testing.T) {
	ring := NewSPSCRing(16)

	samples := make([]types.ThrusterSample, 10)
	for i := range samples {
		samples[i].Timestamp = uint64(i)
	}

	ring.WriteBatch(samples)

	latest := ring.PeekLatest(3)
	if len(latest) != 3 {
		t.Errorf("Expected 3 latest samples, got %d", len(latest))
	}

	if latest[0].Timestamp != 7 {
		t.Errorf("Expected first latest sample timestamp 7, got %d", latest[0].Timestamp)
	}

	if latest[2].Timestamp != 9 {
		t.Errorf("Expected last latest sample timestamp 9, got %d", latest[2].Timestamp)
	}
}

func TestSPSCRingDataIntegrity(t *testing.T) {
	ring := NewSPSCRing(stressQueueSize)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	var corruptedSamples uint64
	var totalRead uint64
	var totalWritten uint64

	wg.Add(1)
	go func() {
		defer wg.Done()
		seq := uint64(0)
		for {
			select {
			case <-stop:
				return
			default:
				sample := types.ThrusterSample{
					Timestamp:      seq,
					AnodeVoltage:   float64(seq)*0.1 + 1000.0,
					GridCurrent:    float64(seq)*1e-6 + 0.1,
					XenonMassFlow:  float64(seq)*1e-12 + 1e-6,
					Thrust:         float64(seq) * 1e-9,
					AxialTorque:    float64(seq) * 1e-12,
					SequenceNumber: seq,
					Valid:          true,
				}
				if ring.Write(sample) {
					atomic.AddUint64(&totalWritten, 1)
					seq++
				} else {
					runtime.Gosched()
				}
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				sample, ok := ring.Read()
				if ok {
					atomic.AddUint64(&totalRead, 1)

					expectedAnode := float64(sample.Timestamp)*0.1 + 1000.0
					expectedGrid := float64(sample.Timestamp)*1e-6 + 0.1
					expectedFlow := float64(sample.Timestamp)*1e-12 + 1e-6
					expectedThrust := float64(sample.Timestamp) * 1e-9

					if sample.Valid &&
						(sample.AnodeVoltage != expectedAnode ||
							sample.GridCurrent != expectedGrid ||
							sample.XenonMassFlow != expectedFlow ||
							sample.Thrust != expectedThrust ||
							sample.SequenceNumber != sample.Timestamp) {
						atomic.AddUint64(&corruptedSamples, 1)
					}
				} else {
					runtime.Gosched()
				}
			}
		}
	}()

	time.Sleep(3 * time.Second)
	close(stop)
	wg.Wait()

	t.Logf("SPSC Data Integrity - Written: %d, Read: %d, Corrupted: %d",
		totalWritten, totalRead, corruptedSamples)

	if corruptedSamples > 0 {
		t.Errorf("Detected %d corrupted samples - memory ordering issue!", corruptedSamples)
	}
}

func TestSlotRingBasic(t *testing.T) {
	ring := NewSlotRing(16)

	if ring.Capacity() != 16 {
		t.Errorf("Expected capacity 16, got %d", ring.Capacity())
	}

	sample := types.ThrusterSample{Timestamp: 123, Thrust: 456e-6}
	if !ring.Write(sample) {
		t.Error("Failed to write sample")
	}

	if ring.Count() != 1 {
		t.Errorf("Expected count 1, got %d", ring.Count())
	}

	read, ok := ring.Read()
	if !ok {
		t.Error("Failed to read sample")
	}

	if read.Timestamp != 123 {
		t.Errorf("Expected timestamp 123, got %d", read.Timestamp)
	}
}

func TestSlotRingConcurrent(t *testing.T) {
	ring := NewSlotRing(1024)

	numWriters := 4
	numReaders := 4
	itemsPerWriter := 5000

	var wg sync.WaitGroup
	var writeErrors uint64

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < itemsPerWriter; j++ {
				sample := types.ThrusterSample{
					Timestamp: uint64(writerID*itemsPerWriter + j),
					Valid:     true,
				}
				for !ring.Write(sample) {
					atomic.AddUint64(&writeErrors, 1)
					runtime.Gosched()
				}
			}
		}(i)
	}

	var totalRead uint64
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				_, ok := ring.Read()
				if ok {
					atomic.AddUint64(&totalRead, 1)
				} else {
					if atomic.LoadUint64(&totalRead) >= uint64(numWriters*itemsPerWriter) {
						break
					}
					runtime.Gosched()
				}
			}
		}()
	}

	wg.Wait()

	expected := numWriters * itemsPerWriter
	t.Logf("Slot Ring - Written: %d, Read: %d, Write retries: %d",
		expected, totalRead, writeErrors)

	if int(totalRead) != expected {
		t.Errorf("Expected %d read items, got %d", expected, totalRead)
	}
}

func TestSeqLockRingStress(t *testing.T) {
	ring := NewSeqLockRing(stressQueueSize)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	var totalWritten uint64
	var totalRead uint64

	wg.Add(1)
	go func() {
		defer wg.Done()
		seq := uint64(0)
		batch := make([]types.ThrusterSample, 64)
		for {
			select {
			case <-stop:
				return
			default:
				for i := range batch {
					batch[i] = types.ThrusterSample{
						Timestamp: seq + uint64(i),
						Thrust:    float64(seq+uint64(i)) * 1e-9,
						Valid:     true,
					}
				}
				n := ring.Write(batch)
				atomic.AddUint64(&totalWritten, uint64(n))
				seq += uint64(n)
				if n == 0 {
					runtime.Gosched()
				}
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		out := make([]types.ThrusterSample, 32)
		for {
			select {
			case <-stop:
				return
			default:
				n := ring.Read(out)
				if n > 0 {
					atomic.AddUint64(&totalRead, uint64(n))
				} else {
					runtime.Gosched()
				}
			}
		}
	}()

	time.Sleep(stressTestDuration)
	close(stop)
	wg.Wait()

	t.Logf("SeqLock Ring - Written: %d, Read: %d", totalWritten, totalRead)

	if totalRead > totalWritten {
		t.Error("Read more samples than written - data corruption!")
	}
}

func TestAtomicRingStress(t *testing.T) {
	ring := NewAtomicRing(stressQueueSize)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	var totalWritten uint64
	var totalRead uint64

	numWriters := 2
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			seq := uint64(writerID * 1000000)
			for {
				select {
				case <-stop:
					return
				default:
					sample := types.ThrusterSample{
						Timestamp:    seq,
						AnodeVoltage: float64(writerID) * 100.0,
						GridCurrent:  float64(seq) * 1e-9,
						Valid:        true,
					}
					if ring.TryWrite(sample) {
						atomic.AddUint64(&totalWritten, 1)
						seq++
					} else {
						runtime.Gosched()
					}
				}
			}
		}(i)
	}

	numReaders := 3
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, ok := ring.TryRead()
					if ok {
						atomic.AddUint64(&totalRead, 1)
					} else {
						runtime.Gosched()
					}
				}
			}
		}(i)
	}

	time.Sleep(stressTestDuration)
	close(stop)
	wg.Wait()

	t.Logf("AtomicRing - Written: %d, Read: %d", totalWritten, totalRead)
}

func TestHPRingConcurrent(t *testing.T) {
	ring := NewHPRing(4096)

	numWriters := 4
	numReaders := 4
	itemsPerWriter := 5000

	var wg sync.WaitGroup
	var enqueueFailures uint64

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < itemsPerWriter; j++ {
				sample := types.ThrusterSample{
					Timestamp:    uint64(writerID*itemsPerWriter + j),
					AnodeVoltage: 1500.0,
					GridCurrent:  0.15,
					Valid:        true,
				}
				for !ring.Enqueue(sample) {
					atomic.AddUint64(&enqueueFailures, 1)
					runtime.Gosched()
				}
			}
		}(i)
	}

	var totalDequeued uint64
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				_, ok := ring.Dequeue()
				if ok {
					atomic.AddUint64(&totalDequeued, 1)
				} else {
					if atomic.LoadUint64(&totalDequeued) >= uint64(numWriters*itemsPerWriter) {
						break
					}
					runtime.Gosched()
				}
			}
		}()
	}

	wg.Wait()

	expected := numWriters * itemsPerWriter
	t.Logf("HP Ring - Enqueued: %d, Dequeued: %d, Failures: %d",
		expected, totalDequeued, enqueueFailures)

	if int(totalDequeued) != expected {
		t.Errorf("Expected %d dequeued items, got %d", expected, totalDequeued)
	}
}

func TestDataIntegrityStress(t *testing.T) {
	ring := NewAtomicRing(16384)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	var corruptedSamples uint64
	var totalRead uint64
	var totalWritten uint64

	wg.Add(1)
	go func() {
		defer wg.Done()
		seq := uint64(0)
		for {
			select {
			case <-stop:
				return
			default:
				sample := types.ThrusterSample{
					Timestamp:     seq,
					AnodeVoltage:  float64(seq)*0.1 + 1000.0,
					GridCurrent:   float64(seq)*1e-6 + 0.1,
					XenonMassFlow: float64(seq)*1e-12 + 1e-6,
					Thrust:        float64(seq) * 1e-9,
					AxialTorque:   float64(seq) * 1e-12,
					SequenceNumber: seq,
					Valid:         true,
				}
				if ring.TryWrite(sample) {
					atomic.AddUint64(&totalWritten, 1)
					seq++
				} else {
					runtime.Gosched()
				}
			}
		}
	}()

	numReaders := 3
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					sample, ok := ring.TryRead()
					if ok {
						atomic.AddUint64(&totalRead, 1)

						expectedAnode := float64(sample.Timestamp)*0.1 + 1000.0
						expectedGrid := float64(sample.Timestamp)*1e-6 + 0.1
						expectedFlow := float64(sample.Timestamp)*1e-12 + 1e-6
						expectedThrust := float64(sample.Timestamp) * 1e-9

						if sample.Valid &&
							(sample.AnodeVoltage != expectedAnode ||
								sample.GridCurrent != expectedGrid ||
								sample.XenonMassFlow != expectedFlow ||
								sample.Thrust != expectedThrust ||
								sample.SequenceNumber != sample.Timestamp) {
							atomic.AddUint64(&corruptedSamples, 1)
						}
					} else {
						runtime.Gosched()
					}
				}
			}
		}()
	}

	time.Sleep(3 * time.Second)
	close(stop)
	wg.Wait()

	t.Logf("Data Integrity - Written: %d, Read: %d, Corrupted: %d",
		totalWritten, totalRead, corruptedSamples)

	if corruptedSamples > 0 {
		t.Errorf("Detected %d corrupted samples - memory ordering issue!", corruptedSamples)
	}
}

func TestHPLinkedQueueConcurrent(t *testing.T) {
	q := NewHPLinkedQueue()

	numWriters := 4
	numReaders := 4
	itemsPerWriter := 10000

	var wg sync.WaitGroup
	var totalEnqueued uint64
	var totalDequeued uint64

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < itemsPerWriter; j++ {
				sample := types.ThrusterSample{
					Timestamp: uint64(writerID*itemsPerWriter + j),
					Valid:     true,
				}
				q.Enqueue(sample)
				atomic.AddUint64(&totalEnqueued, 1)
			}
		}(i)
	}

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				_, ok := q.Dequeue()
				if ok {
					atomic.AddUint64(&totalDequeued, 1)
				} else {
					if atomic.LoadUint64(&totalDequeued) >= uint64(numWriters*itemsPerWriter) {
						break
					}
					runtime.Gosched()
				}
			}
		}()
	}

	wg.Wait()

	t.Logf("HP Linked Queue - Enqueued: %d, Dequeued: %d",
		totalEnqueued, totalDequeued)

	if totalDequeued != totalEnqueued {
		t.Errorf("Expected %d dequeued, got %d", totalEnqueued, totalDequeued)
	}
}

func BenchmarkSeqLockRingWrite(b *testing.B) {
	ring := NewSeqLockRing(65536)
	sample := types.ThrusterSample{
		Timestamp:     1234567890,
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Valid:         true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !ring.WriteSingle(sample) {
			ring.Clear()
			ring.WriteSingle(sample)
		}
	}
}

func BenchmarkSeqLockRingRead(b *testing.B) {
	ring := NewSeqLockRing(65536)
	samples := make([]types.ThrusterSample, 1000)
	for i := range samples {
		samples[i] = types.ThrusterSample{Timestamp: uint64(i), Valid: true}
	}
	ring.Write(samples)

	out := make([]types.ThrusterSample, 1)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ring.Read(out)
		if ring.IsEmpty() {
			ring.Write(samples)
		}
	}
}

func BenchmarkAtomicRingWrite(b *testing.B) {
	ring := NewAtomicRing(65536)
	sample := types.ThrusterSample{
		Timestamp:     1234567890,
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Valid:         true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !ring.TryWrite(sample) {
			ring = NewAtomicRing(65536)
		}
	}
}

func BenchmarkAtomicRingRead(b *testing.B) {
	ring := NewAtomicRing(65536)
	for i := 0; i < 1000; i++ {
		ring.TryWrite(types.ThrusterSample{Timestamp: uint64(i), Valid: true})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ring.TryRead()
		if ring.IsEmpty() {
			for j := 0; j < 1000; j++ {
				ring.TryWrite(types.ThrusterSample{Timestamp: uint64(j), Valid: true})
			}
		}
	}
}

func BenchmarkHPRingEnqueue(b *testing.B) {
	ring := NewHPRing(65536)
	sample := types.ThrusterSample{
		Timestamp:     1234567890,
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Valid:         true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !ring.Enqueue(sample) {
			ring = NewHPRing(65536)
		}
	}
}

func BenchmarkHPRingDequeue(b *testing.B) {
	ring := NewHPRing(65536)
	for i := 0; i < 1000; i++ {
		ring.Enqueue(types.ThrusterSample{Timestamp: uint64(i), Valid: true})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ring.Dequeue()
		if ring.IsEmpty() {
			for j := 0; j < 1000; j++ {
				ring.Enqueue(types.ThrusterSample{Timestamp: uint64(j), Valid: true})
			}
		}
	}
}

func BenchmarkHPLinkedQueueEnqueue(b *testing.B) {
	q := NewHPLinkedQueue()
	sample := types.ThrusterSample{
		Timestamp:     1234567890,
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Valid:         true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q.Enqueue(sample)
	}
}

func BenchmarkSeqLockRingBatchWrite(b *testing.B) {
	ring := NewSeqLockRing(65536)
	samples := make([]types.ThrusterSample, 64)
	for i := range samples {
		samples[i] = types.ThrusterSample{
			Timestamp:     uint64(i),
			AnodeVoltage:  1500.0,
			GridCurrent:   0.15,
			XenonMassFlow: 1.5e-6,
			Valid:         true,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := ring.Write(samples)
		if n < len(samples) {
			ring.Clear()
			ring.Write(samples)
		}
	}
}

func BenchmarkAtomicRingBatchWrite(b *testing.B) {
	ring := NewAtomicRing(65536)
	samples := make([]types.ThrusterSample, 64)
	for i := range samples {
		samples[i] = types.ThrusterSample{
			Timestamp:     uint64(i),
			AnodeVoltage:  1500.0,
			GridCurrent:   0.15,
			XenonMassFlow: 1.5e-6,
			Valid:         true,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ring.WriteBatch(samples)
		if ring.IsFull() {
			ring = NewAtomicRing(65536)
		}
	}
}
