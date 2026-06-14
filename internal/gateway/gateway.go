package gateway

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/cubesat/mems-thruster-gateway/internal/attitude"
	"github.com/cubesat/mems-thruster-gateway/internal/buffer"
	"github.com/cubesat/mems-thruster-gateway/internal/capture"
	"github.com/cubesat/mems-thruster-gateway/internal/config"
	"github.com/cubesat/mems-thruster-gateway/internal/safety"
	"github.com/cubesat/mems-thruster-gateway/internal/thrust"
	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

type ThrusterGateway struct {
	config *config.GatewayConfig

	captureGw  *capture.Gateway
	safetyMon  *safety.Monitor
	estimator  *thrust.FastEstimator
	buffer     *buffer.RingBuffer
	controller *attitude.Controller
	decision   *attitude.DecisionEngine

	streamProc *buffer.StreamProcessor

	running atomic.Bool
	closeCh chan struct{}

	sampleCount  uint64
	commandCount uint64
	dropCount    uint64

	mu sync.RWMutex

	commandCallback func([]types.ThrustCommand)
	statusCallback  func(types.SafetyStatus, uint8)
}

func New(cfg *config.GatewayConfig) *ThrusterGateway {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	gw := &ThrusterGateway{
		config:    cfg,
		closeCh:   make(chan struct{}),
	}

	gw.captureGw = capture.NewGateway(cfg.NetworkInterface, cfg.BPFFilter)
	gw.safetyMon = safety.NewMonitor(cfg.Safety)
	gw.estimator = thrust.NewFastEstimator(
		cfg.ThrustEstimationModel.ThrustCoefficients,
		cfg.ThrustEstimationModel.TorqueCoefficients,
	)
	gw.buffer = buffer.NewRingBuffer(cfg.SampleBufferSize)
	gw.controller = attitude.NewController(cfg.AttitudeControl, cfg.ThrusterCount)
	gw.decision = attitude.NewDecisionEngine(gw.controller, gw.safetyMon)

	gw.streamProc = buffer.NewStreamProcessor(cfg.SampleBufferSize, 100, 50)

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
	gw.streamProc.SetCallback(gw.onProcessWindow)

	if err := gw.captureGw.Start(); err != nil {
		return err
	}

	gw.streamProc.Start()

	gw.running.Store(true)
	go gw.processLoop()

	return nil
}

func (gw *ThrusterGateway) Stop() {
	if !gw.running.Load() {
		return
	}

	gw.running.Store(false)
	gw.captureGw.Stop()
	gw.streamProc.Stop()
	close(gw.closeCh)
}

func (gw *ThrusterGateway) onSamples(samples []types.ThrusterSample) {
	gw.estimator.EstimateBatch(samples)
	gw.safetyMon.CheckBatch(samples)

	n := gw.buffer.Write(samples)
	if n < len(samples) {
		atomic.AddUint64(&gw.dropCount, uint64(len(samples)-n))
	}

	gw.streamProc.Write(samples)

	atomic.AddUint64(&gw.sampleCount, uint64(len(samples)))
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

func (gw *ThrusterGateway) onProcessWindow(window []types.ThrusterSample) {
	_ = window
}

func (gw *ThrusterGateway) processLoop() {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for gw.running.Load() {
		select {
		case <-ticker.C:
			timestamp := uint64(time.Now().UnixNano())
			commands := gw.decision.Process(timestamp)
			_ = commands
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
		BufferUsed:       gw.buffer.Count(),
		BufferCapacity:   gw.buffer.Capacity(),
		CaptureStats:     capStats,
		SafetyStatus:     gw.safetyMon.Status(),
	}
}

func (gw *ThrusterGateway) GetLatestSamples(n int) []types.ThrusterSample {
	return gw.buffer.PeekLatest(n)
}

func (gw *ThrusterGateway) IsHealthy() bool {
	return gw.safetyMon.IsHealthy()
}

func (gw *ThrusterGateway) SafetyStatus() types.SafetyStatus {
	return gw.safetyMon.Status()
}
