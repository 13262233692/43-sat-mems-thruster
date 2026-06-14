package safety

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/cubesat/mems-thruster-gateway/internal/config"
	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

type Monitor struct {
	config config.SafetyConfig

	status     types.SafetyStatus
	faultCount uint32

	consecutiveFaults uint32
	inFaultState      atomic.Bool
	faultStartTime    uint64
	lastFaultTime     uint64

	anodeVoltHist []float64
	gridCurrHist  []float64
	xenonFlowHist []float64
	thrustHist    []float64
	histIdx       int
	histSize      int

	mu sync.RWMutex

	callbacks []func(types.SafetyStatus, uint8)
}

func NewMonitor(cfg config.SafetyConfig) *Monitor {
	histSize := 100
	return &Monitor{
		config:       cfg,
		histSize:     histSize,
		anodeVoltHist: make([]float64, histSize),
		gridCurrHist:  make([]float64, histSize),
		xenonFlowHist: make([]float64, histSize),
		thrustHist:    make([]float64, histSize),
	}
}

func (m *Monitor) AddCallback(cb func(types.SafetyStatus, uint8)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacks = append(m.callbacks, cb)
}

func (m *Monitor) CheckSample(sample *types.ThrusterSample) bool {
	if !sample.Valid {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.anodeVoltHist[m.histIdx] = sample.AnodeVoltage
	m.gridCurrHist[m.histIdx] = sample.GridCurrent
	m.xenonFlowHist[m.histIdx] = sample.XenonMassFlow
	m.thrustHist[m.histIdx] = sample.Thrust
	m.histIdx = (m.histIdx + 1) % m.histSize

	faultCode := types.FaultNone

	if sample.AnodeVoltage > m.config.MaxAnodeVoltage {
		faultCode = types.FaultAnodeOverVolt
		sample.FaultCode = faultCode
	} else if sample.GridCurrent > m.config.MaxGridCurrent {
		faultCode = types.FaultGridOverCurrent
		sample.FaultCode = faultCode
	} else if sample.XenonMassFlow > m.config.MaxXenonFlow {
		faultCode = types.FaultFlowHigh
		sample.FaultCode = faultCode
	} else if sample.XenonMassFlow < m.config.MinXenonFlow {
		faultCode = types.FaultFlowLow
		sample.FaultCode = faultCode
	}

	avgThrust := m.avgThrust()
	if avgThrust > 0 && sample.Thrust > 0 {
		deviation := (sample.Thrust - avgThrust) / avgThrust
		if deviation > m.config.MaxThrustDeviation {
			faultCode = types.FaultThrustMismatch
			sample.FaultCode = faultCode
		}
	}

	if faultCode != types.FaultNone {
		m.consecutiveFaults++
		m.faultCount++
		m.lastFaultTime = sample.Timestamp

		if m.consecutiveFaults >= m.config.FaultThresholdCount {
			if !m.inFaultState.Load() {
				m.enterFaultState(sample.Timestamp, faultCode)
			}
		}
	} else {
		if m.consecutiveFaults > 0 {
			m.consecutiveFaults--
		}

		if m.inFaultState.Load() && m.config.AutoRecovery {
			cooldownNs := uint64(m.config.FaultCooldownMs) * 1_000_000
			if sample.Timestamp > m.lastFaultTime+cooldownNs {
				m.exitFaultState()
			}
		}
	}

	m.updateStatus(sample.Timestamp, faultCode)

	return faultCode == types.FaultNone
}

func (m *Monitor) CheckBatch(samples []types.ThrusterSample) int {
	validCount := 0
	for i := range samples {
		if m.CheckSample(&samples[i]) {
			validCount++
		}
	}
	return validCount
}

func (m *Monitor) avgThrust() float64 {
	sum := 0.0
	count := 0
	for _, v := range m.thrustHist {
		if v > 0 {
			sum += v
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func (m *Monitor) enterFaultState(timestamp uint64, faultCode uint8) {
	m.inFaultState.Store(true)
	m.faultStartTime = timestamp

	status := m.getStatusLocked()
	for _, cb := range m.callbacks {
		cb(status, faultCode)
	}
}

func (m *Monitor) exitFaultState() {
	m.inFaultState.Store(false)
	m.consecutiveFaults = 0
}

func (m *Monitor) updateStatus(timestamp uint64, faultCode uint8) {
	m.status.Timestamp = timestamp
	m.status.SystemHealthy = !m.inFaultState.Load()
	m.status.FaultCount = m.faultCount
	m.status.LastFaultTime = m.lastFaultTime

	if faultCode == types.FaultAnodeOverVolt {
		m.status.AnodeOverVolt = true
	} else {
		m.status.AnodeOverVolt = false
	}

	if faultCode == types.FaultGridOverCurrent {
		m.status.GridOverCurrent = true
	} else {
		m.status.GridOverCurrent = false
	}

	if faultCode == types.FaultFlowHigh || faultCode == types.FaultFlowLow {
		m.status.FlowOutOfRange = true
	} else {
		m.status.FlowOutOfRange = false
	}

	if faultCode == types.FaultThrustMismatch {
		m.status.ThrustMismatch = true
	} else {
		m.status.ThrustMismatch = false
	}

	if faultCode != types.FaultNone {
		m.status.LastFaultCode = faultCode
	}
}

func (m *Monitor) getStatusLocked() types.SafetyStatus {
	return m.status
}

func (m *Monitor) Status() types.SafetyStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *Monitor) IsHealthy() bool {
	return !m.inFaultState.Load()
}

func (m *Monitor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.inFaultState.Store(false)
	m.consecutiveFaults = 0
	m.faultCount = 0
	m.histIdx = 0

	for i := range m.anodeVoltHist {
		m.anodeVoltHist[i] = 0
		m.gridCurrHist[i] = 0
		m.xenonFlowHist[i] = 0
		m.thrustHist[i] = 0
	}

	m.status = types.SafetyStatus{}
}

type Watchdog struct {
	timeout      time.Duration
	lastUpdate   atomic.Value
	alive        atomic.Bool
	callbacks    []func()
	ticker       *time.Ticker
	stopCh       chan struct{}
}

func NewWatchdog(timeout time.Duration) *Watchdog {
	return &Watchdog{
		timeout: timeout,
		stopCh:  make(chan struct{}),
	}
}

func (w *Watchdog) Start() {
	w.alive.Store(true)
	w.lastUpdate.Store(time.Now())
	w.ticker = time.NewTicker(w.timeout / 4)

	go w.monitorLoop()
}

func (w *Watchdog) Stop() {
	w.ticker.Stop()
	close(w.stopCh)
}

func (w *Watchdog) Feed() {
	w.lastUpdate.Store(time.Now())
}

func (w *Watchdog) monitorLoop() {
	for {
		select {
		case <-w.ticker.C:
			last := w.lastUpdate.Load().(time.Time)
			if time.Since(last) > w.timeout {
				if w.alive.Load() {
					w.alive.Store(false)
					for _, cb := range w.callbacks {
						cb()
					}
				}
			} else {
				w.alive.Store(true)
			}
		case <-w.stopCh:
			return
		}
	}
}

func (w *Watchdog) IsAlive() bool {
	return w.alive.Load()
}

func (w *Watchdog) AddCallback(cb func()) {
	w.callbacks = append(w.callbacks, cb)
}
