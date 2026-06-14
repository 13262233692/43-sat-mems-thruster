package thrust

import (
	"math"
	"sync"

	"github.com/cubesat/mems-thruster-gateway/internal/config"
	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

type Estimator struct {
	config config.RegressionModelConfig

	thrustCoefs []float64
	torqueCoefs []float64

	polyDegree   int
	useCross     bool
	featureCount int

	featureBuf []float64

	mu sync.RWMutex
}

func NewEstimator(cfg config.RegressionModelConfig) *Estimator {
	e := &Estimator{
		config:     cfg,
		polyDegree: cfg.PolyDegree,
		useCross:   cfg.UseCrossTerms,
	}

	e.calcFeatureCount()
	e.featureBuf = make([]float64, e.featureCount)
	e.thrustCoefs = make([]float64, len(cfg.ThrustCoefficients))
	copy(e.thrustCoefs, cfg.ThrustCoefficients)
	e.torqueCoefs = make([]float64, len(cfg.TorqueCoefficients))
	copy(e.torqueCoefs, cfg.TorqueCoefficients)

	return e
}

func (e *Estimator) calcFeatureCount() {
	n := 3

	count := 1
	for d := 1; d <= e.polyDegree; d++ {
		count += combWithRep(n, d)
	}
	e.featureCount = count
}

func combWithRep(n, k int) int {
	return factorial(n+k-1) / (factorial(k) * factorial(n-1))
}

func factorial(n int) int {
	if n <= 1 {
		return 1
	}
	result := 1
	for i := 2; i <= n; i++ {
		result *= i
	}
	return result
}

func (e *Estimator) buildFeatures(anodeVolt, gridCurrent, xenonFlow float64) []float64 {
	features := e.featureBuf
	features[0] = 1.0
	idx := 1

	x := [3]float64{anodeVolt, gridCurrent, xenonFlow}

	for d := 1; d <= e.polyDegree; d++ {
		e.addPolyFeatures(x, d, 0, 1.0, 0, &idx, features)
	}

	return features[:idx]
}

func (e *Estimator) addPolyFeatures(x [3]float64, degree, start int, current float64, currentDegree int, idx *int, features []float64) {
	if currentDegree == degree {
		if *idx < len(features) {
			features[*idx] = current
			*idx++
		}
		return
	}

	for i := start; i < 3; i++ {
		e.addPolyFeatures(x, degree, i, current*x[i], currentDegree+1, idx, features)
	}
}

func (e *Estimator) Estimate(sample *types.ThrusterSample) {
	if !sample.Valid {
		sample.Thrust = 0
		sample.AxialTorque = 0
		return
	}

	features := e.buildFeatures(
		sample.AnodeVoltage,
		sample.GridCurrent,
		sample.XenonMassFlow,
	)

	thrust := 0.0
	torque := 0.0

	n := len(features)
	if n > len(e.thrustCoefs) {
		n = len(e.thrustCoefs)
	}

	for i := 0; i < n; i++ {
		thrust += e.thrustCoefs[i] * features[i]
	}

	if n > len(e.torqueCoefs) {
		n = len(e.torqueCoefs)
	}
	for i := 0; i < n; i++ {
		torque += e.torqueCoefs[i] * features[i]
	}

	sample.Thrust = math.Max(0, thrust)
	sample.AxialTorque = torque
}

func (e *Estimator) EstimateBatch(samples []types.ThrusterSample) {
	for i := range samples {
		e.Estimate(&samples[i])
	}
}

func (e *Estimator) EstimateStream(samples []types.ThrusterSample) int {
	count := 0
	for i := range samples {
		if samples[i].Valid {
			e.Estimate(&samples[i])
			count++
		}
	}
	return count
}

type FastEstimator struct {
	a0, a1, a2, a3 float64
	b0, b1, b2, b3 float64
	c1, c2, c3     float64
	d1, d2         float64

	tq_a0, tq_a1, tq_a2, tq_a3 float64
	tq_b0, tq_b1, tq_b2, tq_b3 float64
	tq_c1, tq_c2, tq_c3        float64
	tq_d1, tq_d2               float64
}

func NewFastEstimator(thrustCoefs []float64, torqueCoefs []float64) *FastEstimator {
	fe := &FastEstimator{}

	if len(thrustCoefs) >= 1 {
		fe.a0 = thrustCoefs[0]
	}
	if len(thrustCoefs) >= 2 {
		fe.a1 = thrustCoefs[1]
	}
	if len(thrustCoefs) >= 3 {
		fe.a2 = thrustCoefs[2]
	}
	if len(thrustCoefs) >= 4 {
		fe.a3 = thrustCoefs[3]
	}
	if len(thrustCoefs) >= 5 {
		fe.b1 = thrustCoefs[4]
	}
	if len(thrustCoefs) >= 6 {
		fe.b2 = thrustCoefs[5]
	}
	if len(thrustCoefs) >= 7 {
		fe.b3 = thrustCoefs[6]
	}

	if len(thrustCoefs) >= 8 {
		fe.c1 = thrustCoefs[7]
	}
	if len(thrustCoefs) >= 9 {
		fe.c2 = thrustCoefs[8]
	}
	if len(thrustCoefs) >= 10 {
		fe.c3 = thrustCoefs[9]
	}

	if len(torqueCoefs) >= 1 {
		fe.tq_a0 = torqueCoefs[0]
	}
	if len(torqueCoefs) >= 2 {
		fe.tq_a1 = torqueCoefs[1]
	}
	if len(torqueCoefs) >= 3 {
		fe.tq_a2 = torqueCoefs[2]
	}
	if len(torqueCoefs) >= 4 {
		fe.tq_a3 = torqueCoefs[3]
	}
	if len(torqueCoefs) >= 5 {
		fe.tq_b1 = torqueCoefs[4]
	}
	if len(torqueCoefs) >= 6 {
		fe.tq_b2 = torqueCoefs[5]
	}
	if len(torqueCoefs) >= 7 {
		fe.tq_b3 = torqueCoefs[6]
	}

	return fe
}

func (fe *FastEstimator) Estimate(anodeVolt, gridCurrent, xenonFlow float64) (thrust float64, torque float64) {
	v := anodeVolt
	i := gridCurrent
	m := xenonFlow

	v2 := v * v
	v3 := v2 * v
	i2 := i * i
	i3 := i2 * i
	m2 := m * m

	vi := v * i
	vm := v * m
	im := i * m

	thrust = fe.a0 +
		fe.a1*v + fe.a2*i + fe.a3*m +
		fe.b1*v2 + fe.b2*i2 + fe.b3*m2 +
		fe.c1*vi + fe.c2*vm + fe.c3*im +
		fe.d1*v3 + fe.d2*i3

	torque = fe.tq_a0 +
		fe.tq_a1*v + fe.tq_a2*i + fe.tq_a3*m +
		fe.tq_b1*v2 + fe.tq_b2*i2 + fe.tq_b3*m2 +
		fe.tq_c1*vi + fe.tq_c2*vm + fe.tq_c3*im +
		fe.tq_d1*v3 + fe.tq_d2*i3

	if thrust < 0 {
		thrust = 0
	}

	return thrust, torque
}

func (fe *FastEstimator) EstimateSample(sample *types.ThrusterSample) {
	if !sample.Valid {
		sample.Thrust = 0
		sample.AxialTorque = 0
		return
	}

	thrust, torque := fe.Estimate(
		sample.AnodeVoltage,
		sample.GridCurrent,
		sample.XenonMassFlow,
	)

	sample.Thrust = thrust
	sample.AxialTorque = torque
}

func (fe *FastEstimator) EstimateBatch(samples []types.ThrusterSample) {
	for i := range samples {
		fe.EstimateSample(&samples[i])
	}
}

type MovingAverageFilter struct {
	window     []float64
	windowSize int
	index      int
	count      int
	sum        float64
}

func NewMovingAverageFilter(windowSize int) *MovingAverageFilter {
	return &MovingAverageFilter{
		window:     make([]float64, windowSize),
		windowSize: windowSize,
	}
}

func (m *MovingAverageFilter) Add(value float64) float64 {
	old := m.window[m.index]
	m.window[m.index] = value

	if m.count < m.windowSize {
		m.count++
		m.sum += value
	} else {
		m.sum += value - old
	}

	m.index = (m.index + 1) % m.windowSize

	if m.count == 0 {
		return 0
	}
	return m.sum / float64(m.count)
}

func (m *MovingAverageFilter) Value() float64 {
	if m.count == 0 {
		return 0
	}
	return m.sum / float64(m.count)
}

func (m *MovingAverageFilter) Reset() {
	m.index = 0
	m.count = 0
	m.sum = 0
	for i := range m.window {
		m.window[i] = 0
	}
}

type ThrustState struct {
	Thrust        float64
	Torque        float64
	ThrustSmoothed float64
	TorqueSmoothed float64
	Trend         float64
	Variance      float64
}

type StatefulEstimator struct {
	fast        *FastEstimator
	thrustMA    *MovingAverageFilter
	torqueMA    *MovingAverageFilter
	history     []float64
	histIdx     int
	histCount   int
}

func NewStatefulEstimator(thrustCoefs, torqueCoefs []float64, smoothWindow int) *StatefulEstimator {
	return &StatefulEstimator{
		fast:     NewFastEstimator(thrustCoefs, torqueCoefs),
		thrustMA: NewMovingAverageFilter(smoothWindow),
		torqueMA: NewMovingAverageFilter(smoothWindow),
		history:  make([]float64, smoothWindow),
	}
}

func (se *StatefulEstimator) Update(sample *types.ThrusterSample) ThrustState {
	se.fast.EstimateSample(sample)

	thrustSmoothed := se.thrustMA.Add(sample.Thrust)
	torqueSmoothed := se.torqueMA.Add(sample.AxialTorque)

	se.history[se.histIdx] = sample.Thrust
	se.histIdx = (se.histIdx + 1) % len(se.history)
	if se.histCount < len(se.history) {
		se.histCount++
	}

	trend := 0.0
	variance := 0.0

	if se.histCount >= 2 {
		first := se.history[(se.histIdx+1)%len(se.history)]
		last := se.history[(se.histIdx-1+len(se.history))%len(se.history)]
		trend = last - first

		mean := thrustSmoothed
		sumSq := 0.0
		for i := 0; i < se.histCount; i++ {
			diff := se.history[i] - mean
			sumSq += diff * diff
		}
		variance = sumSq / float64(se.histCount)
	}

	return ThrustState{
		Thrust:         sample.Thrust,
		Torque:         sample.AxialTorque,
		ThrustSmoothed: thrustSmoothed,
		TorqueSmoothed: torqueSmoothed,
		Trend:          trend,
		Variance:       variance,
	}
}
