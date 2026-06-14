package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cubesat/mems-thruster-gateway/internal/config"
	"github.com/cubesat/mems-thruster-gateway/internal/gateway"
	"github.com/cubesat/mems-thruster-gateway/pkg/types"
)

func main() {
	fmt.Println("=== MEMS Micro-Thruster Attitude Control Gateway ===")
	fmt.Println("CubeSat On-Board Computer Bypass Deployment System")
	fmt.Println("Sampling Rate: 20 kHz | Protocol: CCSDS Space Link")
	fmt.Println()

	cfg := config.DefaultConfig()

	gw := gateway.New(cfg)

	gw.SetCommandCallback(func(cmds []types.ThrustCommand) {
		for _, cmd := range cmds {
			fmt.Printf("[CMD] Thruster %d: %.2f uN, %d us\n",
				cmd.ThrusterID, cmd.ThrustLevel*1e6, cmd.DurationUs)
		}
	})

	gw.SetStatusCallback(func(status types.SafetyStatus, faultCode uint8) {
		if !status.SystemHealthy {
			fmt.Printf("[SAFETY] FAULT detected: code=%d, count=%d\n",
				faultCode, status.FaultCount)
		}
	})

	if err := gw.Start(); err != nil {
		fmt.Printf("Failed to start gateway: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Gateway started successfully")
	fmt.Println("Press Ctrl+C to stop...")
	fmt.Println()

	go printStats(gw)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down...")
	gw.Stop()

	stats := gw.Stats()
	fmt.Printf("\nFinal Statistics:\n")
	fmt.Printf("  Samples Processed:  %d\n", stats.SamplesProcessed)
	fmt.Printf("  Commands Issued:    %d\n", stats.CommandsIssued)
	fmt.Printf("  Samples Dropped:    %d\n", stats.SamplesDropped)
	fmt.Printf("  Buffer Utilization: %d/%d\n", stats.BufferUsed, stats.BufferCapacity)
	fmt.Printf("  System Health:      %v\n", stats.SafetyStatus.SystemHealthy)

	fmt.Println("Gateway stopped.")
}

func printStats(gw *gateway.ThrusterGateway) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		stats := gw.Stats()
		fmt.Printf("\r[STAT] Samples: %d | Rate: %.1f kHz | Health: %v | Buf: %d%%",
			stats.SamplesProcessed,
			float64(stats.CaptureStats.SamplesDecoded)/2000.0,
			stats.SafetyStatus.SystemHealthy,
			stats.BufferUsed*100/stats.BufferCapacity,
		)
	}
}
