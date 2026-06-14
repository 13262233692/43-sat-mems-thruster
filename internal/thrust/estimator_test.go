package thrust

import (
	"testing"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

func TestFastEstimatorEstimate(t *testing.T) {
	thrustCoefs := []float64{
		0.0,
		1.2e-3,
		5.8e2,
		2.5e7,
		-1.1e-6,
		3.2e-1,
		-7.5e3,
		0.0,
		0.0,
		0.0,
	}
	torqueCoefs := []float64{
		0.0,
		8.5e-6,
		1.2e1,
		6.3e5,
		-4.2e-9,
		7.8e-4,
		-2.1e1,
		0.0,
		0.0,
		0.0,
	}

	fe := NewFastEstimator(thrustCoefs, torqueCoefs)

	thrust, torque := fe.Estimate(1500.0, 0.15, 1.5e-6)

	if thrust < 0 {
		t.Error("Thrust should not be negative")
	}

	if thrust == 0 {
		t.Log("Warning: thrust is zero, check coefficients")
	}

	t.Logf("Thrust: %.4f uN", thrust*1e6)
	t.Logf("Torque: %.6f Nm", torque)
}

func TestFastEstimatorEstimateSample(t *testing.T) {
	thrustCoefs := []float64{
		0.0,
		1.2e-3,
		5.8e2,
		2.5e7,
		-1.1e-6,
		3.2e-1,
		-7.5e3,
		0, 0, 0,
	}
	torqueCoefs := []float64{
		0.0,
		8.5e-6,
		1.2e1,
		6.3e5,
		-4.2e-9,
		7.8e-4,
		-2.1e1,
		0, 0, 0,
	}

	fe := NewFastEstimator(thrustCoefs, torqueCoefs)

	sample := &types.ThrusterSample{
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Valid:         true,
	}

	fe.EstimateSample(sample)

	if !sample.Valid {
		t.Error("Sample should still be valid")
	}

	if sample.Thrust < 0 {
		t.Error("Thrust should not be negative")
	}

	t.Logf("Sample Thrust: %.4f uN", sample.Thrust*1e6)
	t.Logf("Sample Torque: %.6f Nm", sample.AxialTorque)
}

func TestFastEstimatorInvalidSample(t *testing.T) {
	fe := NewFastEstimator([]float64{1, 1, 1}, []float64{1, 1, 1})

	sample := &types.ThrusterSample{
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Valid:         false,
	}

	fe.EstimateSample(sample)

	if sample.Thrust != 0 {
		t.Errorf("Expected thrust 0 for invalid sample, got %f", sample.Thrust)
	}

	if sample.AxialTorque != 0 {
		t.Errorf("Expected torque 0 for invalid sample, got %f", sample.AxialTorque)
	}
}

func TestFastEstimatorBatch(t *testing.T) {
	fe := NewFastEstimator(
		[]float64{0, 1.2e-3, 5.8e2, 2.5e7, -1.1e-6, 3.2e-1, -7.5e3, 0, 0, 0},
		[]float64{0, 8.5e-6, 1.2e1, 6.3e5, -4.2e-9, 7.8e-4, -2.1e1, 0, 0, 0},
	)

	samples := make([]types.ThrusterSample, 10)
	for i := range samples {
		samples[i] = types.ThrusterSample{
			AnodeVoltage:  1500.0 + float64(i)*10,
			GridCurrent:   0.15 + float64(i)*0.001,
			XenonMassFlow: 1.5e-6 + float64(i)*0.1e-6,
			Valid:         true,
		}
	}

	fe.EstimateBatch(samples)

	for i, s := range samples {
		if s.Thrust <= 0 {
			t.Errorf("Sample %d: expected positive thrust, got %f", i, s.Thrust)
		}
	}
}

func TestMovingAverageFilter(t *testing.T) {
	maf := NewMovingAverageFilter(5)

	values := []float64{1.0, 2.0, 3.0, 4.0, 5.0}
	var avg float64

	for _, v := range values {
		avg = maf.Add(v)
	}

	expected := 3.0
	if avg != expected {
		t.Errorf("Expected average %f, got %f", expected, avg)
	}

	avg = maf.Add(6.0)
	expected = 4.0
	if avg != expected {
		t.Errorf("Expected average %f after adding 6.0, got %f", expected, avg)
	}
}

func TestMovingAverageFilterPartial(t *testing.T) {
	maf := NewMovingAverageFilter(10)

	avg := maf.Add(5.0)
	if avg != 5.0 {
		t.Errorf("Expected average 5.0 with one sample, got %f", avg)
	}

	avg = maf.Add(3.0)
	if avg != 4.0 {
		t.Errorf("Expected average 4.0 with two samples, got %f", avg)
	}
}

func TestStatefulEstimator(t *testing.T) {
	se := NewStatefulEstimator(
		[]float64{0, 1.2e-3, 5.8e2, 2.5e7, -1.1e-6, 3.2e-1, -7.5e3, 0, 0, 0},
		[]float64{0, 8.5e-6, 1.2e1, 6.3e5, -4.2e-9, 7.8e-4, -2.1e1, 0, 0, 0},
		10,
	)

	sample := &types.ThrusterSample{
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Valid:         true,
	}

	state := se.Update(sample)

	if state.Thrust <= 0 {
		t.Error("Expected positive thrust")
	}

	if state.ThrustSmoothed <= 0 {
		t.Error("Expected positive smoothed thrust")
	}
}

func BenchmarkFastEstimatorEstimate(b *testing.B) {
	fe := NewFastEstimator(
		[]float64{0, 1.2e-3, 5.8e2, 2.5e7, -1.1e-6, 3.2e-1, -7.5e3, 0, 0, 0},
		[]float64{0, 8.5e-6, 1.2e1, 6.3e5, -4.2e-9, 7.8e-4, -2.1e1, 0, 0, 0},
	)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fe.Estimate(1500.0, 0.15, 1.5e-6)
	}
}

func BenchmarkFastEstimatorEstimateSample(b *testing.B) {
	fe := NewFastEstimator(
		[]float64{0, 1.2e-3, 5.8e2, 2.5e7, -1.1e-6, 3.2e-1, -7.5e3, 0, 0, 0},
		[]float64{0, 8.5e-6, 1.2e1, 6.3e5, -4.2e-9, 7.8e-4, -2.1e1, 0, 0, 0},
	)

	sample := &types.ThrusterSample{
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Valid:         true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fe.EstimateSample(sample)
	}
}

func BenchmarkFastEstimatorBatch100(b *testing.B) {
	fe := NewFastEstimator(
		[]float64{0, 1.2e-3, 5.8e2, 2.5e7, -1.1e-6, 3.2e-1, -7.5e3, 0, 0, 0},
		[]float64{0, 8.5e-6, 1.2e1, 6.3e5, -4.2e-9, 7.8e-4, -2.1e1, 0, 0, 0},
	)

	samples := make([]types.ThrusterSample, 100)
	for i := range samples {
		samples[i] = types.ThrusterSample{
			AnodeVoltage:  1500.0,
			GridCurrent:   0.15,
			XenonMassFlow: 1.5e-6,
			Valid:         true,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fe.EstimateBatch(samples)
	}
}

func BenchmarkMovingAverageFilter(b *testing.B) {
	maf := NewMovingAverageFilter(50)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		maf.Add(float64(i) * 1e-6)
	}
}

func BenchmarkStatefulEstimator(b *testing.B) {
	se := NewStatefulEstimator(
		[]float64{0, 1.2e-3, 5.8e2, 2.5e7, -1.1e-6, 3.2e-1, -7.5e3, 0, 0, 0},
		[]float64{0, 8.5e-6, 1.2e1, 6.3e5, -4.2e-9, 7.8e-4, -2.1e1, 0, 0, 0},
		10,
	)

	sample := &types.ThrusterSample{
		AnodeVoltage:  1500.0,
		GridCurrent:   0.15,
		XenonMassFlow: 1.5e-6,
		Valid:         true,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		se.Update(sample)
	}
}
