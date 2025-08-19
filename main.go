package main

import (
	"flag"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"paitec-rtmp/config"
	"paitec-rtmp/internal/api"
	"paitec-rtmp/internal/hls"
	"paitec-rtmp/internal/logger"
	"paitec-rtmp/internal/rtmp"
)

var (
	rtmpAddr = flag.String("rtmp-addr", "0.0.0.0:1936", "RTMP server address")
	apiAddr  = flag.String("api-addr", "0.0.0.0:8080", "HTTP API server address")
)

func main() {
	flag.Parse()

	// Set up logging
	logLevel := os.Getenv("PAITEC_RTMP_LOG_LEVEL")
	if logLevel != "" {
		logger.SetLevel(logLevel)
	}

	logFormat := os.Getenv("PAITEC_RTMP_LOG_FORMAT")
	if logFormat != "" {
		logger.SetFormatter(logFormat)
	}

	log := logger.GetLogger()
	log.Info("Starting Paitec RTMP Server...")

	// Load config (optional; proceed with flags if it fails)
	cfg, err := config.Load()
	if err != nil {
		log := logger.GetLogger()
		log.Warnf("failed to load config, continuing with flags/env: %v", err)
		cfg = nil
	}

	// Resolve addresses
	rtmpAddress := *rtmpAddr
	apiAddress := *apiAddr
	if cfg != nil {
		rtmpAddress = cfg.GetRTMPAddr()
		apiAddress = cfg.GetServerAddr()
		// Apply logging config
		logger.SetLevel(cfg.Logging.Level)
		logger.SetFormatter(cfg.Logging.Format)
		logger.SetOutput(cfg.Logging.File)
	}

	// Create HLS server
	hlsServer := hls.NewHLSServer("./hls", 10*time.Second, 10)

	// Create servers
	rtmpServer := rtmp.NewServer(rtmpAddress, hlsServer)
	apiServer := api.NewServer(apiAddress, rtmpServer, hlsServer, cfg)

	// Create ready channels
	rtmpReady := make(chan struct{})
	apiReady := make(chan struct{})

	// Start servers
	var wg sync.WaitGroup
	errChan := make(chan error, 2)

	// Start RTMP server
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := rtmpServer.Start(rtmpReady); err != nil {
			log.Errorf("RTMP server error: %v", err)
			errChan <- err
		}
	}()

	// Start HTTP API server
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := apiServer.Start(apiReady); err != nil {
			log.Errorf("HTTP API server error: %v", err)
			errChan <- err
		}
	}()

	// Wait for both servers to be ready
	<-rtmpReady
	<-apiReady

	log.Info("Paitec RTMP Server started successfully")

	// Wait for shutdown signal or error
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigChan:
		log.Info("Shutdown signal received, stopping servers...")
	case err := <-errChan:
		log.Errorf("Server error: %v", err)
	}

	// Stop servers
	if err := apiServer.Stop(); err != nil {
		log.Errorf("Error stopping HTTP API server: %v", err)
	}
	log.Info("HTTP API server stopped")

	if err := rtmpServer.Stop(); err != nil {
		log.Errorf("Error stopping RTMP server: %v", err)
	}
	log.Info("RTMP server stopped")

	log.Info("Paitec RTMP Server stopped")
}
