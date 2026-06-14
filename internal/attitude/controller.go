package attitude

import (
	"math"
	"sync"

	"github.com/cubesat/mems-thruster-gateway/internal/config"
	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

type PIDController struct {
	Kp, Ki, Kd float64

	integral   float64
	prevError  float64
	prevTime   uint64
	initialized bool

	outputMin float64
	outputMax float64

	mu sync.Mutex
}

func NewPIDController(kp, ki, kd float64, outMin, outMax float64) *PIDController {
	return &PIDController{
		Kp:        kp,
		Ki:        ki,
		Kd:        kd,
		outputMin: outMin,
		outputMax: outMax,
	}
}

func (p *PIDController) Update(setpoint, measured float64, timestamp uint64) float64 {
	p.mu.Lock()
	defer p.mu.Unlock()

	error := setpoint - measured

	if !p.initialized {
		p.prevError = error
		p.prevTime = timestamp
		p.initialized = true
		output := p.Kp * error
		if output > p.outputMax {
			output = p.outputMax
		} else if output < p.outputMin {
			output = p.outputMin
		}
		return output
	}

	dt := float64(timestamp - p.prevTime) / 1e9
	if dt <= 0 {
		dt = 0.00005
	}

	p.integral += error * dt
	derivative := (error - p.prevError) / dt

	output := p.Kp*error + p.Ki*p.integral + p.Kd*derivative

	if output > p.outputMax {
		excess := output - p.outputMax
		if p.Ki != 0 {
			p.integral -= excess / p.Ki
		}
		output = p.outputMax
	} else if output < p.outputMin {
		excess := p.outputMin - output
		if p.Ki != 0 {
			p.integral += excess / p.Ki
		}
		output = p.outputMin
	}

	p.prevError = error
	p.prevTime = timestamp

	return output
}

func (p *PIDController) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.integral = 0
	p.prevError = 0
	p.initialized = false
}

type Controller struct {
	config config.AttitudeControlConfig

	rollPID  *PIDController
	pitchPID *PIDController
	yawPID   *PIDController

	targetState types.AttitudeState
	currentState types.AttitudeState

	thrusterCount int
	thrustPerThruster float64

	mu sync.RWMutex

	commandCallback func([]types.ThrustCommand)
}

func NewController(cfg config.AttitudeControlConfig, thrusterCount int) *Controller {
	maxThrust := cfg.MaxThrustPerAxis

	c := &Controller{
		config:          cfg,
		thrusterCount:   thrusterCount,
		rollPID:         NewPIDController(cfg.KpRoll, cfg.KiRoll, cfg.KdRoll, -maxThrust, maxThrust),
		pitchPID:        NewPIDController(cfg.KpPitch, cfg.KiPitch, cfg.KdPitch, -maxThrust, maxThrust),
		yawPID:          NewPIDController(cfg.KpYaw, cfg.KiYaw, cfg.KdYaw, -maxThrust, maxThrust),
	}

	return c
}

func (c *Controller) SetTarget(state types.AttitudeState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.targetState = state
}

func (c *Controller) UpdateCurrent(state types.AttitudeState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.currentState = state
}

func (c *Controller) Compute(timestamp uint64) []types.ThrustCommand {
	c.mu.RLock()
	target := c.targetState
	current := c.currentState
	c.mu.RUnlock()

	rollTorque := c.rollPID.Update(target.RollAngle, current.RollAngle, timestamp)
	pitchTorque := c.pitchPID.Update(target.PitchAngle, current.PitchAngle, timestamp)
	yawTorque := c.yawPID.Update(target.YawAngle, current.YawAngle, timestamp)

	if math.Abs(current.RollAngle-target.RollAngle) < c.config.DeadBandAngle {
		rollTorque = 0
	}
	if math.Abs(current.PitchAngle-target.PitchAngle) < c.config.DeadBandAngle {
		pitchTorque = 0
	}
	if math.Abs(current.YawAngle-target.YawAngle) < c.config.DeadBandAngle {
		yawTorque = 0
	}

	commands := c.allocateThrust(rollTorque, pitchTorque, yawTorque, timestamp)

	if c.commandCallback != nil && len(commands) > 0 {
		c.commandCallback(commands)
	}

	return commands
}

func (c *Controller) allocateThrust(rollTorque, pitchTorque, yawTorque float64, timestamp uint64) []types.ThrustCommand {
	var commands []types.ThrustCommand

	thrusterForces := make([]float64, c.thrusterCount)

	if c.thrusterCount == 4 {
		torquePerThruster := 0.05
		forcePerThruster := 200e-6

		rollForce := rollTorque / torquePerThruster
		pitchForce := pitchTorque / torquePerThruster
		yawForce := yawTorque / torquePerThruster

		thrusterForces[0] = pitchForce + rollForce + yawForce
		thrusterForces[1] = pitchForce - rollForce - yawForce
		thrusterForces[2] = -pitchForce - rollForce + yawForce
		thrusterForces[3] = -pitchForce + rollForce - yawForce

		maxForce := 0.0
		for _, f := range thrusterForces {
			if math.Abs(f) > maxForce {
				maxForce = math.Abs(f)
			}
		}
		if maxForce > forcePerThruster {
			scale := forcePerThruster / maxForce
			for i := range thrusterForces {
				thrusterForces[i] *= scale
			}
		}
	}

	for i, force := range thrusterForces {
		if math.Abs(force) < 1e-9 {
			continue
		}

		duration := uint32(math.Abs(force) / c.config.MaxThrustPerAxis * 1000)
		if duration < c.config.MinPulseDurationUs {
			continue
		}

		direction := [3]float64{0, 0, 1}
		if force < 0 {
			direction[2] = -1
		}

		cmd := types.ThrustCommand{
			Timestamp:   timestamp,
			ThrusterID:  uint8(i),
			DurationUs:  duration,
			ThrustLevel: math.Abs(force),
			Direction:   direction,
			CommandID:   uint64(timestamp) + uint64(i),
		}
		commands = append(commands, cmd)
	}

	return commands
}

func (c *Controller) Reset() {
	c.rollPID.Reset()
	c.pitchPID.Reset()
	c.yawPID.Reset()
}

func (c *Controller) SetCommandCallback(cb func([]types.ThrustCommand)) {
	c.commandCallback = cb
}

type ControlMode int

const (
	ModeStandby ControlMode = iota
	ModeHold
	ModeSpin
	ModeDetumble
	ModeManual
)

type SafetyChecker interface {
	IsHealthy() bool
	Status() types.SafetyStatus
}

type DecisionEngine struct {
	controller   *Controller
	safety       SafetyChecker
	target       types.AttitudeState

	mode         ControlMode
	enabled      bool
	manualThrust float64

	mu sync.RWMutex
}

func NewDecisionEngine(ctrl *Controller, safety SafetyChecker) *DecisionEngine {
	return &DecisionEngine{
		controller: ctrl,
		safety:     safety,
		mode:       ModeStandby,
		enabled:    false,
	}
}

func (de *DecisionEngine) SetMode(mode ControlMode) {
	de.mu.Lock()
	defer de.mu.Unlock()
	de.mode = mode
}

func (de *DecisionEngine) GetMode() ControlMode {
	de.mu.RLock()
	defer de.mu.RUnlock()
	return de.mode
}

func (de *DecisionEngine) SetTarget(state types.AttitudeState) {
	de.mu.Lock()
	defer de.mu.Unlock()
	de.target = state
	de.controller.SetTarget(state)
}

func (de *DecisionEngine) UpdateState(state types.AttitudeState) {
	de.controller.UpdateCurrent(state)
}

func (de *DecisionEngine) Process(timestamp uint64) []types.ThrustCommand {
	de.mu.RLock()
	mode := de.mode
	enabled := de.enabled
	de.mu.RUnlock()

	if !de.safety.IsHealthy() {
		return nil
	}

	if !enabled {
		return nil
	}

	switch mode {
	case ModeHold:
		return de.controller.Compute(timestamp)
	case ModeDetumble:
		return de.detumble(timestamp)
	case ModeManual:
		return de.manualControl(timestamp)
	default:
		return nil
	}
}

func (de *DecisionEngine) detumble(timestamp uint64) []types.ThrustCommand {
	return nil
}

func (de *DecisionEngine) manualControl(timestamp uint64) []types.ThrustCommand {
	de.mu.RLock()
	thrust := de.manualThrust
	de.mu.RUnlock()

	if math.Abs(thrust) < 1e-9 {
		return nil
	}

	cmd := types.ThrustCommand{
		Timestamp:   timestamp,
		ThrusterID:  0,
		DurationUs:  1000,
		ThrustLevel: math.Abs(thrust),
		Direction:   [3]float64{0, 0, 1},
		CommandID:   uint64(timestamp),
	}
	return []types.ThrustCommand{cmd}
}

func (de *DecisionEngine) Enable() {
	de.mu.Lock()
	defer de.mu.Unlock()
	de.enabled = true
}

func (de *DecisionEngine) Disable() {
	de.mu.Lock()
	defer de.mu.Unlock()
	de.enabled = false
	de.controller.Reset()
}

func (de *DecisionEngine) SetManualThrust(thrust float64) {
	de.mu.Lock()
	defer de.mu.Unlock()
	de.manualThrust = thrust
}
