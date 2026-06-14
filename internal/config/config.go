package config

type GatewayConfig struct {
	NetworkInterface string
	BPFFilter        string
	SampleBufferSize int
	ThrusterCount    int

	AnodeVoltMin    float64
	AnodeVoltMax    float64
	GridCurrentMin  float64
	GridCurrentMax  float64
	XenonFlowMin    float64
	XenonFlowMax    float64

	ThrustEstimationModel RegressionModelConfig

	AttitudeControl AttitudeControlConfig

	Safety SafetyConfig
}

type RegressionModelConfig struct {
	ThrustCoefficients    []float64
	TorqueCoefficients    []float64
	PolyDegree            int
	UseCrossTerms         bool
}

type AttitudeControlConfig struct {
	KpRoll    float64
	KpPitch   float64
	KpYaw     float64
	KiRoll    float64
	KiPitch   float64
	KiYaw     float64
	KdRoll    float64
	KdPitch   float64
	KdYaw     float64

	MaxThrustPerAxis float64
	MinPulseDurationUs uint32
	DeadBandAngle    float64
}

type SafetyConfig struct {
	MaxAnodeVoltage     float64
	MaxGridCurrent      float64
	MaxXenonFlow        float64
	MinXenonFlow        float64
	MaxThrustDeviation  float64
	FaultThresholdCount uint32
	FaultCooldownMs     uint32
	AutoRecovery        bool
}

func DefaultConfig() *GatewayConfig {
	return &GatewayConfig{
		NetworkInterface: "eth0",
		BPFFilter:        "udp port 10000",
		SampleBufferSize: 65536,
		ThrusterCount:    4,

		AnodeVoltMin: 200.0,
		AnodeVoltMax: 2000.0,
		GridCurrentMin: 0.001,
		GridCurrentMax: 0.5,
		XenonFlowMin: 0.05e-6,
		XenonFlowMax: 5.0e-6,

		ThrustEstimationModel: RegressionModelConfig{
			ThrustCoefficients: []float64{
				0.0,
				1.2e-3,
				5.8e2,
				2.5e7,
				-1.1e-6,
				3.2e-1,
				-7.5e3,
			},
			TorqueCoefficients: []float64{
				0.0,
				8.5e-6,
				1.2e1,
				6.3e5,
				-4.2e-9,
				7.8e-4,
				-2.1e1,
			},
			PolyDegree:    2,
			UseCrossTerms: true,
		},

		AttitudeControl: AttitudeControlConfig{
			KpRoll:    0.15,
			KpPitch:   0.15,
			KpYaw:     0.12,
			KiRoll:    0.01,
			KiPitch:   0.01,
			KiYaw:     0.008,
			KdRoll:    0.05,
			KdPitch:   0.05,
			KdYaw:     0.04,

			MaxThrustPerAxis:   200e-6,
			MinPulseDurationUs: 100,
			DeadBandAngle:      0.001,
		},

		Safety: SafetyConfig{
			MaxAnodeVoltage:     2200.0,
			MaxGridCurrent:      0.6,
			MaxXenonFlow:        6.0e-6,
			MinXenonFlow:        0.01e-6,
			MaxThrustDeviation:  0.25,
			FaultThresholdCount: 5,
			FaultCooldownMs:     100,
			AutoRecovery:        true,
		},
	}
}
