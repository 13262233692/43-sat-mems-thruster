package vallock

import (
	"math"
	"sync"

	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

type MaxwellMeyerModel struct {
	mu sync.RWMutex

	springK        float64
	dampingEta     float64
	meyerN         float64
	meyerK         float64
	yieldStress    float64

	elasticStrain  float64
	viscousStrain  float64
	plasticStrain  float64
	totalStrain    float64

	stress         float64
	stressRate     float64
	lastStress     float64

	wearAccum      float64
	crawlVel       float64
	backlash       float64

	totalCycles    uint64
	zeroCrossings  uint64
	lastSign       int8

	dt             float64
	initialized    bool
}

func NewMaxwellMeyerModel() *MaxwellMeyerModel {
	return &MaxwellMeyerModel{
		springK:     1500.0,
		dampingEta:  120.0,
		meyerN:      0.28,
		meyerK:      8.5e-6,
		yieldStress: 0.025,
		dt:          SampleIntervalSec,
		lastSign:    0,
	}
}

func (m *MaxwellMeyerModel) Configure(springK, dampingEta, meyerN, meyerK, yieldStress float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.springK = springK
	m.dampingEta = dampingEta
	m.meyerN = meyerN
	m.meyerK = meyerK
	m.yieldStress = yieldStress
}

func (m *MaxwellMeyerModel) Update(gridCurrent float64, thrust float64, timestamp uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	normalizedStress := gridCurrent
	if math.Abs(normalizedStress) < 1e-9 {
		normalizedStress = 0
	}

	m.stress = normalizedStress
	m.stressRate = (normalizedStress - m.lastStress) / m.dt
	m.lastStress = normalizedStress

	strainElastic := normalizedStress / m.springK
	m.elasticStrain = strainElastic

	sign := 1.0
	if m.stressRate < 0 {
		sign = -1.0
	}

	if math.Abs(normalizedStress) > m.yieldStress {
		excess := math.Abs(normalizedStress) - m.yieldStress
		plasticRate := m.meyerK * math.Pow(excess, m.meyerN) * sign
		m.viscousStrain += plasticRate * m.dt
		m.plasticStrain += math.Abs(plasticRate) * m.dt
	}

	viscousRelax := math.Exp(-m.springK * m.dt / m.dampingEta)
	m.viscousStrain *= viscousRelax
	m.viscousStrain += (normalizedStress / m.dampingEta) * m.dt

	m.totalStrain = m.elasticStrain + m.viscousStrain

	currSign := int8(0)
	if normalizedStress > 0 {
		currSign = 1
	} else if normalizedStress < 0 {
		currSign = -1
	}

	if m.initialized && currSign != 0 && currSign != m.lastSign {
		m.zeroCrossings++
		m.totalCycles++
		m.backlash = math.Abs(m.viscousStrain) * 0.35
		m.wearAccum += m.backlash * 0.001
	}

	m.lastSign = currSign
	m.initialized = true

	m.crawlVel = m.viscousStrain / m.dt
}

func (m *MaxwellMeyerModel) HealthScore() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	wearFactor := math.Exp(-m.wearAccum * 50.0)
	plasticFactor := math.Exp(-m.plasticStrain * 20.0)
	backlashFactor := math.Exp(-m.backlash * 100.0)

	score := wearFactor * 0.45 * 100
	score += plasticFactor * 0.30 * 100
	score += backlashFactor * 0.25 * 100

	return math.Max(0, math.Min(100, score))
}

func (m *MaxwellMeyerModel) WearAccumulation() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.wearAccum
}

func (m *MaxwellMeyerModel) CrawlVelocity() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.crawlVel
}

func (m *MaxwellMeyerModel) Backlash() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.backlash
}

func (m *MaxwellMeyerModel) PlasticStrain() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.plasticStrain
}

func (m *MaxwellMeyerModel) ZeroCrossings() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.zeroCrossings
}

func (m *MaxwellMeyerModel) TotalCycles() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.totalCycles
}

func (m *MaxwellMeyerModel) Stress() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stress
}

func (m *MaxwellMeyerModel) StressRate() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stressRate
}

func (m *MaxwellMeyerModel) GetState() ValveHealthState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	score := m.healthScoreLocked()
	state := ValveStateHealthy

	switch {
	case score < 30:
		state = ValveStateCritical
	case score < 60:
		state = ValveStateWear
	case m.wearAccum > 0.05:
		state = ValveStateWear
	}

	return ValveHealthState{
		State:              state,
		HealthScore:        score,
		WearAccumulation:   m.wearAccum,
		CrawlVelocity:      m.crawlVel,
		BacklashAmount:     m.backlash,
		PlasticDeformation: m.plasticStrain,
		StressLevel:        m.stress,
		ZeroCrossingCount:  m.zeroCrossings,
	}
}

func (m *MaxwellMeyerModel) healthScoreLocked() float64 {
	wearFactor := math.Exp(-m.wearAccum * 50.0)
	plasticFactor := math.Exp(-m.plasticStrain * 20.0)
	backlashFactor := math.Exp(-m.backlash * 100.0)

	score := wearFactor*0.45*100 + plasticFactor*0.30*100 + backlashFactor*0.25*100
	return math.Max(0, math.Min(100, score))
}

func (m *MaxwellMeyerModel) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.elasticStrain = 0
	m.viscousStrain = 0
	m.plasticStrain = 0
	m.totalStrain = 0
	m.stress = 0
	m.stressRate = 0
	m.lastStress = 0
	m.wearAccum = 0
	m.crawlVel = 0
	m.backlash = 0
	m.totalCycles = 0
	m.zeroCrossings = 0
	m.lastSign = 0
	m.initialized = false
}

func (m *MaxwellMeyerModel) ProcessBatch(samples []types.ThrusterSample) {
	for i := range samples {
		s := &samples[i]
		if !s.Valid {
			continue
		}
		m.Update(s.GridCurrent, s.Thrust, s.Timestamp)
	}
}
