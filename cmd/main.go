package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"syscall"
	"time"

	"github.com/mohammedhabas11/admin-bot/pkg/cachecleaner"
	"github.com/mohammedhabas11/admin-bot/pkg/config"
	"github.com/mohammedhabas11/admin-bot/pkg/httpserver"
)

// Global state for running services (protected by mutex)
var (
	appStateMutex      sync.Mutex
	activeConfig       *config.Config // Config currently used by running services
	currentHttpServer  *httpserver.Server
	currentCleanerStop func()
	serverWg           sync.WaitGroup // WaitGroup specifically for the server goroutine
)

func main() {
	log.Println("Starting admin-bot...")

	// Channel for signaling config reloads
	reloadChan := make(chan bool, 1) // Buffered channel

	// Load initial configuration and start watching
	initialCfg, err := config.LoadConfig("config.yaml", reloadChan) // Pass the channel
	if err != nil {
		log.Fatalf("FATAL: Failed to load initial configuration: %v", err)
	}
	activeConfig = initialCfg // Set the initial active config

	// Start initial services based on the first loaded config
	startServices(activeConfig)

	// --- Graceful Shutdown / Reload Handling ---
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	log.Println("Application started. Press Ctrl+C to shut down.")

	// Main loop to wait for signals or reload triggers
	keepRunning := true
	for keepRunning {
		select {
		case sig := <-signalChan:
			log.Printf("Shutdown signal received: %v. Starting graceful shutdown...", sig)
			keepRunning = false      // Exit loop after handling shutdown
			stopServices(true, true) // Stop all services on shutdown

		case <-reloadChan:
			log.Println("Reload signal received. Checking for necessary restarts...")
			newCfg := config.GetConfig() // Get the newly loaded config

			// --- Compare configurations ---
			restartServer, restartCleaner := compareConfigs(activeConfig, newCfg)

			if !restartServer && !restartCleaner {
				log.Println("No configuration changes requiring service restart detected.")
				// Update activeConfig even if no restart, so next comparison is correct
				appStateMutex.Lock()
				activeConfig = newCfg
				appStateMutex.Unlock()
				continue // Go back to waiting for signals
			}

			log.Println("Configuration changes detected, restarting relevant services...")
			stopServices(restartServer, restartCleaner) // Stop only affected services

			// Update active config *before* starting with it
			appStateMutex.Lock()
			activeConfig = newCfg
			appStateMutex.Unlock()

			startServices(activeConfig) // Start services (will only start those stopped)
			log.Println("Relevant services restarted with new configuration.")
		}
	}

	// --- Wait for Services to Finish on Shutdown ---
	log.Println("Waiting for background tasks (HTTP server) to complete...")
	serverWg.Wait() // Wait for HTTP server goroutine to finish its shutdown

	log.Println("Application exiting.")
}

// compareConfigs checks if restarts are needed based on config differences.
func compareConfigs(oldCfg, newCfg *config.Config) (restartServer bool, restartCleaner bool) {
	if oldCfg == nil || newCfg == nil {
		log.Println("WARN: Comparing nil configurations, forcing restart.")
		return true, true // Force restart if something went wrong
	}

	// 1. Check for HTTP Server restart conditions
	// Restart if HTTP enabled status, address, or port changes.
	// Also restart if static or proxy sections change (simplest way to reload handlers).
	if oldCfg.HTTP.Enabled != newCfg.HTTP.Enabled ||
		oldCfg.HTTP.Addr != newCfg.HTTP.Addr ||
		oldCfg.HTTP.Port != newCfg.HTTP.Port ||
		!reflect.DeepEqual(oldCfg.HTTP.Static, newCfg.HTTP.Static) || // Check static config
		!reflect.DeepEqual(oldCfg.HTTP.ForwardProxy, newCfg.HTTP.ForwardProxy) { // Check proxy config
		log.Println("Change detected in HTTP configuration requiring server restart.")
		restartServer = true
	}

	// 2. Check for Cache Cleaner restart conditions
	// Restart if cleaner interval changes OR if relevant proxy cache settings change
	// (enabled status, dir, ttl - as these affect cleaner operation)
	oldProxyCacheEnabled := oldCfg.HTTP.ForwardProxy.Enabled && oldCfg.HTTP.ForwardProxy.Cache.Enabled && oldCfg.HTTP.ForwardProxy.Cache.CacheDir != ""
	newProxyCacheEnabled := newCfg.HTTP.ForwardProxy.Enabled && newCfg.HTTP.ForwardProxy.Cache.Enabled && newCfg.HTTP.ForwardProxy.Cache.CacheDir != ""

	if oldProxyCacheEnabled != newProxyCacheEnabled || // If caching master status changed
		(newProxyCacheEnabled && // Only compare details if caching is enabled in new config
			(oldCfg.ProxyCacheCleanup.Interval != newCfg.ProxyCacheCleanup.Interval ||
				oldCfg.HTTP.ForwardProxy.Cache.CacheDir != newCfg.HTTP.ForwardProxy.Cache.CacheDir ||
				oldCfg.HTTP.ForwardProxy.Cache.CacheTTL != newCfg.HTTP.ForwardProxy.Cache.CacheTTL)) {
		log.Println("Change detected in Cache Cleaner or relevant Proxy Cache configuration requiring cleaner restart.")
		restartCleaner = true
	}

	// If server restarts, cleaner might also need restarting if its config depends on server state
	// For now, we treat them independently based on their direct config sections.

	return restartServer, restartCleaner
}

// startServices starts services based on config, only if they aren't already running.
func startServices(cfg *config.Config) {
	appStateMutex.Lock()
	defer appStateMutex.Unlock()

	log.Println("Attempting to start necessary services...")

	// --- Start HTTP Server ---
	if cfg.HTTP.Enabled {
		if currentHttpServer == nil { // Only start if not already running
			currentHttpServer = httpserver.NewServer(cfg)
			serverWg.Add(1)
			go func(server *httpserver.Server) {
				defer serverWg.Done()
				log.Println("Starting HTTP server goroutine...")
				// Use a background context - shutdown is handled by stopServices
				if err := server.Start(context.Background()); err != nil {
					log.Printf("HTTP server error: %v", err)
				}
				log.Println("HTTP server goroutine finished.")
			}(currentHttpServer)
		} else {
			log.Println("HTTP server already running.")
		}
	} else {
		log.Println("HTTP server is disabled by configuration.")
		// Ensure server is stopped if it was running and is now disabled
		if currentHttpServer != nil {
			log.Println("Stopping HTTP server as it's now disabled...")
			if err := currentHttpServer.Stop(); err != nil {
				log.Printf("Error stopping disabled HTTP server: %v", err)
			}
			currentHttpServer = nil
		}
	}

	// --- Start Cache Cleaner ---
	shouldRunCleaner := cfg.HTTP.ForwardProxy.Enabled && cfg.HTTP.ForwardProxy.Cache.Enabled && cfg.HTTP.ForwardProxy.Cache.CacheDir != ""
	if shouldRunCleaner {
		if currentCleanerStop == nil { // Only start if not already running
			cleanerInterval, err := cfg.ProxyCacheCleanup.GetInterval()
			if err != nil {
				log.Printf("WARNING: Invalid cache cleanup interval, using default: %v", err)
				cleanerInterval = time.Hour
			}
			cacheDir := cfg.HTTP.ForwardProxy.Cache.CacheDir
			cacheTTL, err := cfg.HTTP.ForwardProxy.Cache.GetCacheTTL()
			if err != nil {
				log.Printf("WARNING: Invalid cache TTL, using default for cleanup: %v", err)
				cacheTTL, _ = config.StrToDuration("7d")
			}
			currentCleanerStop = cachecleaner.StartCleaner(context.Background(), cleanerInterval, cacheDir, cacheTTL)
		} else {
			log.Println("Cache cleaner already running.")
		}
	} else {
		log.Println("Proxy cache cleaning is disabled by configuration.")
		// Ensure cleaner is stopped if it was running and is now disabled
		if currentCleanerStop != nil {
			log.Println("Stopping cache cleaner as it's now disabled...")
			currentCleanerStop()
			currentCleanerStop = nil
		}
	}
	log.Println("startServices completed.")
}

// stopServices gracefully stops running services selectively.
func stopServices(stopServer bool, stopCleaner bool) {
	appStateMutex.Lock()
	defer appStateMutex.Unlock()

	log.Println("Attempting to stop services...")

	// Stop HTTP Server
	if stopServer && currentHttpServer != nil {
		log.Println("Stopping HTTP server...")
		if err := currentHttpServer.Stop(); err != nil {
			log.Printf("Error stopping HTTP server: %v", err)
		} else {
			log.Println("HTTP server stop initiated.")
		}
		currentHttpServer = nil // Clear variable after initiating stop
	} else if stopServer {
		log.Println("HTTP server stop requested but was not running.")
	}

	// Stop Cache Cleaner
	if stopCleaner && currentCleanerStop != nil {
		log.Println("Stopping cache cleaner...")
		currentCleanerStop()
		log.Println("Cache cleaner stopped.")
		currentCleanerStop = nil // Clear variable
	} else if stopCleaner {
		log.Println("Cache cleaner stop requested but was not running.")
	}
	log.Println("stopServices completed.")
}
