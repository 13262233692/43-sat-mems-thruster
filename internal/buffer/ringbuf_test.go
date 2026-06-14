package buffer

import (
	"testing"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

func TestRingBufferWriteRead(t *testing.T) {
	rb := NewRingBuffer(16)

	if rb.Capacity() != 16 {
		t.Errorf("Expected capacity 16, got %d", rb.Capacity())
	}

	if !rb.IsEmpty() {
		t.Error("Buffer should be empty initially")
	}

	samples := make([]types.ThrusterSample, 5)
	for i := range samples {
		samples[i].Timestamp = uint64(i)
		samples[i].Thrust = float64(i) * 1e-6
	}

	n := rb.Write(samples)
	if n != 5 {
		t.Errorf("Expected to write 5 samples, wrote %d", n)
	}

	if rb.Count() != 5 {
		t.Errorf("Expected count 5, got %d", rb.Count())
	}

	out := make([]types.ThrusterSample, 10)
	n = rb.Read(out)
	if n != 5 {
		t.Errorf("Expected to read 5 samples, read %d", n)
	}

	for i := 0; i < n; i++ {
		if out[i].Timestamp != uint64(i) {
			t.Errorf("Sample %d timestamp mismatch: expected %d, got %d", i, i, out[i].Timestamp)
		}
	}

	if rb.Count() != 0 {
		t.Errorf("Expected count 0 after read, got %d", rb.Count())
	}
}

func TestRingBufferOverflow(t *testing.T) {
	rb := NewRingBuffer(8)

	samples := make([]types.ThrusterSample, 12)
	for i := range samples {
		samples[i].Timestamp = uint64(i)
	}

	n := rb.Write(samples)
	if n != 8 {
		t.Errorf("Expected to write 8 samples (buffer full), wrote %d", n)
	}

	if !rb.IsFull() {
		t.Error("Buffer should be full")
	}

	n = rb.Write(samples)
	if n != 0 {
		t.Errorf("Expected to write 0 samples when full, wrote %d", n)
	}
}

func TestRingBufferPeek(t *testing.T) {
	rb := NewRingBuffer(16)

	samples := make([]types.ThrusterSample, 10)
	for i := range samples {
		samples[i].Timestamp = uint64(i)
	}

	rb.Write(samples)

	latest := rb.PeekLatest(3)
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

func TestRingBufferWrapAround(t *testing.T) {
	rb := NewRingBuffer(8)

	for i := 0; i < 6; i++ {
		sample := types.ThrusterSample{Timestamp: uint64(i)}
		rb.WriteSingle(sample)
	}

	out := make([]types.ThrusterSample, 4)
	rb.Read(out)

	for i := 0; i < 6; i++ {
		sample := types.ThrusterSample{Timestamp: uint64(i + 10)}
		rb.WriteSingle(sample)
	}

	if rb.Count() != 8 {
		t.Errorf("Expected count 8, got %d", rb.Count())
	}

	out = make([]types.ThrusterSample, 8)
	n := rb.Read(out)
	if n != 8 {
		t.Errorf("Expected to read 8 samples, got %d", n)
	}

	expectedTimestamps := []uint64{4, 5, 10, 11, 12, 13, 14, 15}
	for i, expected := range expectedTimestamps {
		if out[i].Timestamp != expected {
			t.Errorf("Sample %d: expected timestamp %d, got %d", i, expected, out[i].Timestamp)
		}
	}
}

func TestRingBufferClear(t *testing.T) {
	rb := NewRingBuffer(16)

	samples := make([]types.ThrusterSample, 10)
	rb.Write(samples)

	if rb.Count() != 10 {
		t.Errorf("Expected count 10, got %d", rb.Count())
	}

	rb.Clear()

	if !rb.IsEmpty() {
		t.Error("Buffer should be empty after clear")
	}
}

func TestDoubleBuffer(t *testing.T) {
	db := NewDoubleBuffer(8)

	for i := 0; i < 8; i++ {
		sample := types.ThrusterSample{Timestamp: uint64(i)}
		db.Write(sample)
	}

	front := db.ReadFront()
	if len(front) != 8 {
		t.Errorf("Expected front buffer size 8, got %d", len(front))
	}

	for i := 8; i < 16; i++ {
		sample := types.ThrusterSample{Timestamp: uint64(i)}
		db.Write(sample)
	}

	front = db.ReadFront()
	if front[0].Timestamp != 8 {
		t.Errorf("Expected first sample timestamp 8, got %d", front[0].Timestamp)
	}
}

func BenchmarkRingBufferWrite(b *testing.B) {
	rb := NewRingBuffer(65536)
	sample := types.ThrusterSample{
		Timestamp:     1234567890,
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Valid:         true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !rb.WriteSingle(sample) {
			rb.Clear()
			rb.WriteSingle(sample)
		}
	}
}

func BenchmarkRingBufferBatchWrite(b *testing.B) {
	rb := NewRingBuffer(65536)
	samples := make([]types.ThrusterSample, 100)
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
		n := rb.Write(samples)
		if n < len(samples) {
			rb.Clear()
			rb.Write(samples)
		}
	}
}

func BenchmarkRingBufferRead(b *testing.B) {
	rb := NewRingBuffer(65536)
	samples := make([]types.ThrusterSample, 1000)
	for i := range samples {
		samples[i] = types.ThrusterSample{Timestamp: uint64(i), Valid: true}
	}
	rb.Write(samples)

	out := make([]types.ThrusterSample, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rb.Read(out)
		if rb.Count() < 100 {
			rb.Write(samples)
		}
	}
}

func BenchmarkDoubleBufferWrite(b *testing.B) {
	db := NewDoubleBuffer(1024)
	sample := types.ThrusterSample{
		Timestamp:     1234567890,
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Valid:         true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		db.Write(sample)
	}
}
