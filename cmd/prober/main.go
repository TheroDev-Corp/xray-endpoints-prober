package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/config"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/metrics"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/prober"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/server"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/subscription"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/xrayconfig"
	"github.com/TheroDev-Corp/xray-endpoints-prober/pkg/xraycore"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.LUTC)
	log.Println("Starting xray-endpoints-prober...")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	appCtx, cancelApp := context.WithCancel(context.Background())
	defer cancelApp()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Initialize metrics
	promMetrics := metrics.NewMetrics(cfg.Region, cfg.XrayVersion)

	// Initialize HTTP server
	srv := server.NewServer(cfg.ListenAddr, promMetrics)
	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("HTTP server stopped: %v", err)
		}
	}()

	// Ensure Xray-core binary is available
	pm := xraycore.NewProcessManager(cfg.XrayCacheDir, cfg.XrayVersion)
	if err := pm.EnsureBinary(appCtx); err != nil {
		log.Printf("Warning: failed to acquire Xray binary directly (%v). Checking local fallback...", err)
		if path, pErr := xraycore.FindBinaryInPath("xray"); pErr == nil {
			log.Printf("Found system Xray binary at %s", path)
		} else {
			log.Fatalf("Failed to obtain Xray binary: %v", err)
		}
	}
	defer func() {
		_ = pm.Stop()
	}()

	// Initialize prober
	probeRunner := prober.NewProber(cfg, promMetrics)

	// Config file destination
	xrayConfigFile := filepath.Join(cfg.XrayCacheDir, "config.json")

	// Subscription Manager
	var subManager *subscription.Manager
	onUpdate := func(endpoints []subscription.Endpoint) error {
		if len(endpoints) == 0 {
			log.Printf("No endpoints available after parsing/filtering")
			srv.SetReady(false)
			return nil
		}

		// Generate Xray config
		configJSON, assignedEndpoints, err := xrayconfig.GenerateConfigBytes(endpoints, 10800)
		if err != nil {
			return fmt.Errorf("failed to generate Xray config: %w", err)
		}

		if err := os.WriteFile(xrayConfigFile, configJSON, 0644); err != nil {
			return fmt.Errorf("failed to write Xray config: %w", err)
		}

		// Restart Xray process
		log.Printf("Starting/Restarting Xray process with %d endpoints...", len(assignedEndpoints))
		if err := pm.Restart(xrayConfigFile); err != nil {
			return fmt.Errorf("failed to restart Xray: %w", err)
		}

		// Allow Xray to initialize listeners before probing
		time.Sleep(500 * time.Millisecond)

		// Update prober targets
		probeRunner.UpdateEndpoints(assignedEndpoints)
		srv.SetReady(true)
		log.Printf("Xray updated and prober reconfigured with %d endpoints", len(assignedEndpoints))
		return nil
	}

	subManager = subscription.NewManager(cfg, onUpdate)
	if err := subManager.Start(appCtx); err != nil {
		log.Printf("Subscription manager start error: %v", err)
	}

	// Update metrics periodically for subscription
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-appCtx.Done():
				return
			case <-ticker.C:
				eps := subManager.GetEndpoints()
				lastPoll := subManager.GetLastPollTime()
				pollErr := subManager.GetPollErrors()
				promMetrics.SetSubscriptionState(len(eps), lastPoll, pollErr)
			}
		}
	}()

	// Start prober background loops
	go probeRunner.StartLatencyLoop(appCtx)
	go probeRunner.StartSpeedtestLoop(appCtx)

	// Wait for OS signals
	sig := <-sigChan
	log.Printf("Received signal %s, initiating graceful shutdown...", sig)
	cancelApp()

	// Shutdown HTTP server with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	srv.SetLive(false)
	srv.SetReady(false)
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP shutdown error: %v", err)
	}

	_ = pm.Stop()
	log.Println("xray-endpoints-prober shutdown complete")
}
