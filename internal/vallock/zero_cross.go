package vallock

import (
	"math"
	"sync"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

type ZeroCrossingDetector struct {
	mu sync.RWMutex

	windowSize    int
	history       []float64
	histIdx       int
	histCount     int
	lastSample    float64
	lastSign      int8
	initialized   bool

	eventBuffer   []ZeroCrossingEvent
	eventIdx      int
	eventCount    int

	nonlinearThresh float64
	anomalyThresh   float64

	dt            float64
	dt2           float64
	dt3           float64
}

func NewZeroCrossingDetector(windowSize int) *ZeroCrossingDetector {
	if windowSize < 5 {
		windowSize = 5
	}
	if windowSize > 1024 {
		windowSize = 1024
	}

	dt := SampleIntervalSec
	return &ZeroCrossingDetector{
		windowSize:        windowSize,
		history:           make([]float64, windowSize),
		eventBuffer:       make([]ZeroCrossingEvent, 128),
		nonlinearThresh:   2.5,
		anomalyThresh:     0.7,
		dt:                dt,
		dt2:               dt * dt,
		dt3:               dt * dt * dt,
		lastSign:          0,
	}
}

func (d *ZeroCrossingDetector) SetThresholds(nonlinear, anomaly float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nonlinearThresh = nonlinear
	d.anomalyThresh = anomaly
}

func (d *ZeroCrossingDetector) Update(sample *types.ThrusterSample) *ZeroCrossingEvent {
	if !sample.Valid {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	curr := sample.GridCurrent
	d.history[d.histIdx] = curr
	d.histIdx = (d.histIdx + 1) % d.windowSize
	if d.histCount < d.windowSize {
		d.histCount++
	}

	currSign := int8(0)
	if curr > 0 {
		currSign = 1
	} else if curr < 0 {
		currSign = -1
	}

	var event *ZeroCrossingEvent

	if d.initialized && currSign != 0 && d.lastSign != 0 && currSign != d.lastSign {
		event = d.detectZeroCrossingLocked(sample)
	}

	d.lastSample = curr
	d.lastSign = currSign
	d.initialized = true

	return event
}

func (d *ZeroCrossingDetector) detectZeroCrossingLocked(sample *types.ThrusterSample) *ZeroCrossingEvent {
	if d.histCount < 5 {
		return nil
	}

	idx := d.histIdx
	n := d.windowSize

	x := make([]float64, 5)
	for i := 0; i < 5; i++ {
		offset := idx - 5 + i
		if offset < 0 {
			offset += n
		}
		x[i] = d.history[offset]
	}

	d1 := (-3*x[0] - 10*x[1] + 18*x[2] - 6*x[3] + x[4]) / (12 * d.dt)
	d2 := (2*x[0] - x[1] - 2*x[2] - x[3] + 2*x[4]) / (7 * d.dt2)
	d3 := (-x[0] + 2*x[1] - 2*x[3] + x[4]) / (2 * d.dt3)

	d1Abs := math.Abs(d1)
	d2Abs := math.Abs(d2)
	d3Abs := math.Abs(d3)

	nonlinearity := 0.0
	if d1Abs > 1e-12 {
		nonlinearity = d2Abs / d1Abs * d.dt
	}

	direction := int8(1)
	if d1 < 0 {
		direction = -1
	}

	anomalyScore := d.calculateAnomalyScoreLocked(d1Abs, d2Abs, d3Abs, nonlinearity)

	event := ZeroCrossingEvent{
		Timestamp:        sample.Timestamp,
		GridCurrent:      sample.GridCurrent,
		FirstDerivative:  d1,
		SecondDerivative: d2,
		ThirdDerivative:  d3,
		Nonlinearity:     nonlinearity,
		Direction:        direction,
		AnomalyScore:     anomalyScore,
	}

	d.eventBuffer[d.eventIdx] = event
	d.eventIdx = (d.eventIdx + 1) % len(d.eventBuffer)
	if d.eventCount < len(d.eventBuffer) {
		d.eventCount++
	}

	return &event
}

func (d *ZeroCrossingDetector) calculateAnomalyScoreLocked(d1, d2, d3, nonlinearity float64) float64 {
	score := 0.0

	if nonlinearity > d.nonlinearThresh {
		score += 0.4 * math.Min(1.0, nonlinearity/(d.nonlinearThresh*2))
	}

	if d3 > 0 && d2 > 0 {
		jerkFactor := math.Min(1.0, d.dt*d.dt*d3/d2)
		score += 0.3 * jerkFactor
	}

	referenceD2 := d1 / d.dt
	if math.Abs(referenceD2) > 1e-12 {
		d2Ratio := d2 / math.Abs(referenceD2)
		if d2Ratio > 2.0 {
			score += 0.2 * math.Min(1.0, (d2Ratio-2.0)/3.0)
		}
	}

	highFreqRatio := 0.0
	if d.histCount >= d.windowSize {
		highFreqRatio = d.highFrequencyRatioLocked()
	}
	score += 0.1 * highFreqRatio

	return math.Min(1.0, score)
}

func (d *ZeroCrossingDetector) highFrequencyRatioLocked() float64 {
	if d.histCount < 8 {
		return 0
	}

	n := d.windowSize
	idx := d.histIdx

	sumLow := 0.0
	sumHigh := 0.0
	count := 0

	for i := 2; i < n-2 && count < 64; i++ {
		offset := (idx - i + n) % n
		curr := d.history[offset]
		prev2 := d.history[(offset-2+n)%n]
		next2 := d.history[(offset+2)%n]

		lowFreq := (prev2 + 2*curr + next2) / 4.0
		highFreq := curr - lowFreq

		sumLow += lowFreq * lowFreq
		sumHigh += highFreq * highFreq
		count++
	}

	if sumLow < 1e-20 {
		return 0
	}
	return math.Sqrt(sumHigh / sumLow)
}

func (d *ZeroCrossingDetector) ProcessBatch(samples []types.ThrusterSample) []ZeroCrossingEvent {
	events := make([]ZeroCrossingEvent, 0, 4)
	for i := range samples {
		evt := d.Update(&samples[i])
		if evt != nil {
			events = append(events, *evt)
		}
	}
	return events
}

func (d *ZeroCrossingDetector) IsAnomalous(event *ZeroCrossingEvent) bool {
	if event == nil {
		return false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return event.AnomalyScore >= d.anomalyThresh
}

func (d *ZeroCrossingDetector) RecentEvents(maxCount int) []ZeroCrossingEvent {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if maxCount > d.eventCount {
		maxCount = d.eventCount
	}
	if maxCount <= 0 {
		return nil
	}

	result := make([]ZeroCrossingEvent, maxCount)
	for i := 0; i < maxCount; i++ {
		offset := (d.eventIdx - maxCount + i + len(d.eventBuffer)) % len(d.eventBuffer)
		result[i] = d.eventBuffer[offset]
	}
	return result
}

func (d *ZeroCrossingDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()

	for i := range d.history {
		d.history[i] = 0
	}
	for i := range d.eventBuffer {
		d.eventBuffer[i] = ZeroCrossingEvent{}
	}
	d.histIdx = 0
	d.histCount = 0
	d.eventIdx = 0
	d.eventCount = 0
	d.lastSample = 0
	d.lastSign = 0
	d.initialized = false
}
