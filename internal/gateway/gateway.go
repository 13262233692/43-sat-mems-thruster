package gateway

import (
	"sync/atomic"
	"time"

	"github.com/cubesat/mems-thruster-gateway/internal/attitude"
	"github.com/cubesat/mems-thruster-gateway/internal/capture"
	"github.com/cubesat/mems-thruster-gateway/internal/config"
	"github.com/cubesat/mems-thruster-gateway/internal/lockfree"
	"github.com/cubesat/mems-thruster-gateway/internal/safety"
	"github.com/cubesat/mems-thruster-gateway/internal/thrust"
	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

type ThrusterGateway struct {
	config *config.GatewayConfig

	captureGw  *capture.Gateway
	safetyMon  *safety.Monitor
	estimator  *thrust.FastEstimator
	controller *attitude.Controller
	decision   *attitude.DecisionEngine

	sampleRing *lockfree.SPSCRing
	latestRing *lockfree.SPSCRing

	running atomic.Bool
	closeCh chan struct{}

	sampleCount  uint64
	commandCount uint64
	dropCount    uint64

	commandCallback func([]types.ThrustCommand)
	statusCallback  func(types.SafetyStatus, uint8)

	processCloseCh chan struct{}
}

func New(cfg *config.GatewayConfig) *ThrusterGateway {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	gw := &ThrusterGateway{
		config:         cfg,
		closeCh:        make(chan struct{}),
		processCloseCh: make(chan struct{}),
	}

	gw.captureGw = capture.NewGateway(cfg.NetworkInterface, cfg.BPFFilter)
	gw.safetyMon = safety.NewMonitor(cfg.Safety)
	gw.estimator = thrust.NewFastEstimator(
		cfg.ThrustEstimationModel.ThrustCoefficients,
		cfg.ThrustEstimationModel.TorqueCoefficients,
	)
	gw.sampleRing = lockfree.NewSPSCRing(cfg.SampleBufferSize)
	gw.latestRing = lockfree.NewSPSCRing(1024)
	gw.controller = attitude.NewController(cfg.AttitudeControl, cfg.ThrusterCount)
	gw.decision = attitude.NewDecisionEngine(gw.controller, gw.safetyMon)

	return gw
}

func (gw *ThrusterGateway) Start() error {
	if gw.running.Load() {
		return nil
	}

	gw.captureGw.SetSampleCallback(gw.onSamples)
	gw.captureGw.SetErrorCallback(gw.onCaptureError)
	gw.safetyMon.AddCallback(gw.onSafetyEvent)
	gw.controller.SetCommandCallback(gw.onCommands)

	if err := gw.captureGw.Start(); err != nil {
		return err
	}

	gw.running.Store(true)
	go gw.processLoop()
	go gw.attitudeUpdateLoop()

	return nil
}

func (gw *ThrusterGateway) Stop() {
	if !gw.running.Load() {
		return
	}

	gw.running.Store(false)
	gw.captureGw.Stop()
	close(gw.closeCh)
	close(gw.processCloseCh)
}

func (gw *ThrusterGateway) onSamples(samples []types.ThrusterSample) {
	gw.estimator.EstimateBatch(samples)
	gw.safetyMon.CheckBatch(samples)

	n := gw.sampleRing.WriteBatch(samples)
	if n < len(samples) {
		atomic.AddUint64(&gw.dropCount, uint64(len(samples)-n))
	}

	for i := 0; i < n; i++ {
		gw.latestRing.Write(samples[i])
	}

	atomic.AddUint64(&gw.sampleCount, uint64(n))
}

func (gw *ThrusterGateway) onCaptureError(err error) {
	_ = err
}

func (gw *ThrusterGateway) onSafetyEvent(status types.SafetyStatus, faultCode uint8) {
	if gw.statusCallback != nil {
		gw.statusCallback(status, faultCode)
	}

	if !status.SystemHealthy {
		gw.decision.Disable()
	}
}

func (gw *ThrusterGateway) onCommands(commands []types.ThrustCommand) {
	atomic.AddUint64(&gw.commandCount, uint64(len(commands)))

	if gw.commandCallback != nil {
		gw.commandCallback(commands)
	}
}

func (gw *ThrusterGateway) processLoop() {
	batch := make([]types.ThrusterSample, 256)

	for gw.running.Load() {
		n := gw.sampleRing.ReadBatch(batch)
		if n == 0 {
			time.Sleep(1 * time.Microsecond)
			continue
		}

		_ = batch[:n]
	}
}

func (gw *ThrusterGateway) attitudeUpdateLoop() {
	ticker := time.NewTicker(1 * time.Millisecond)
	defer ticker.Stop()

	for gw.running.Load() {
		select {
		case <-ticker.C:
			timestamp := uint64(time.Now().UnixNano())
			gw.decision.Process(timestamp)
		case <-gw.closeCh:
			return
		}
	}
}

func (gw *ThrusterGateway) SetCommandCallback(cb func([]types.ThrustCommand)) {
	gw.commandCallback = cb
}

func (gw *ThrusterGateway) SetStatusCallback(cb func(types.SafetyStatus, uint8)) {
	gw.statusCallback = cb
}

func (gw *ThrusterGateway) SetTargetAttitude(state types.AttitudeState) {
	gw.decision.SetTarget(state)
}

func (gw *ThrusterGateway) SetMode(mode attitude.ControlMode) {
	gw.decision.SetMode(mode)
}

func (gw *ThrusterGateway) EnableControl() {
	gw.decision.Enable()
}

func (gw *ThrusterGateway) DisableControl() {
	gw.decision.Disable()
}

type GatewayStats struct {
	SamplesProcessed uint64
	CommandsIssued   uint64
	SamplesDropped   uint64
	BufferUsed       int
	BufferCapacity   int
	CaptureStats     capture.GatewayStats
	SafetyStatus     types.SafetyStatus
}

func (gw *ThrusterGateway) Stats() GatewayStats {
	capStats := gw.captureGw.Stats()
	return GatewayStats{
		SamplesProcessed: atomic.LoadUint64(&gw.sampleCount),
		CommandsIssued:   atomic.LoadUint64(&gw.commandCount),
		SamplesDropped:   atomic.LoadUint64(&gw.dropCount),
		BufferUsed:       gw.sampleRing.Count(),
		BufferCapacity:   gw.sampleRing.Capacity(),
		CaptureStats:     capStats,
		SafetyStatus:     gw.safetyMon.Status(),
	}
}

func (gw *ThrusterGateway) GetLatestSamples(n int) []types.ThrusterSample {
	return gw.latestRing.PeekLatest(n)
}

func (gw *ThrusterGateway) IsHealthy() bool {
	return gw.safetyMon.IsHealthy()
}

func (gw *ThrusterGateway) SafetyStatus() types.SafetyStatus {
	return gw.safetyMon.Status()
}

func (gw *ThrusterGateway) ReadSamples(out []types.ThrusterSample) int {
	return gw.sampleRing.ReadBatch(out)
}
