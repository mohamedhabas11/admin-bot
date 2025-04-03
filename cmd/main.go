package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mohammedhabas11/admin-bot/pkg/cachecleaner"
	"github.com/mohammedhabas11/admin-bot/pkg/config"
	"github.com/mohammedhabas11/admin-bot/pkg/httpserver"
)

func main() {
	// --- Initial Setup ---
	log.Println("Starting admin-bot...")

	// Load initial configuration (now manages global state)
	_, err := config.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("FATAL: Failed to load initial configuration: %v", err)
	}

	// Get the initial config snapshot
	cfg := config.GetConfig()

	// --- Services Setup ---
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background()) // Context for graceful shutdown
	defer cancel()                                          // Ensure cancel is called eventually if main exits unexpectedly

	// HTTP Server
	var httpServer *httpserver.Server // Keep the variable to potentially access later if needed
	if cfg.HTTP.Enabled {
		httpServer = httpserver.NewServer(cfg) // Pass initial config
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Println("Starting HTTP server goroutine...")
			// Start now takes the context and handles its own shutdown call
			if err := httpServer.Start(ctx); err != nil {
				log.Printf("HTTP server error: %v", err)
			}
			log.Println("HTTP server goroutine finished.")
		}()
	} else {
		log.Println("HTTP server is disabled by configuration.")
	}

	// Cache Cleaner
	var cacheCleanerStop func()
	if cfg.HTTP.ForwardProxy.Enabled && cfg.HTTP.ForwardProxy.Cache.Enabled && cfg.HTTP.ForwardProxy.Cache.CacheDir != "" {
		cleanerInterval, err := cfg.ProxyCacheCleanup.GetInterval()
		if err != nil {
			log.Printf("WARNING: Invalid cache cleanup interval, using default: %v", err)
			cleanerInterval = time.Hour
		}
		cacheDir := cfg.HTTP.ForwardProxy.Cache.CacheDir
		cacheTTL, err := cfg.HTTP.ForwardProxy.Cache.GetCacheTTL()
		if err != nil {
			log.Printf("WARNING: Invalid cache TTL, using default for cleanup: %v", err)
			// Use the default TTL used in config loading as fallback
			cacheTTL, _ = config.StrToDuration("7d")
		}

		log.Printf("Starting cache cleaner: Interval=%s, Dir=%s, TTL=%s", cleanerInterval, cacheDir, cacheTTL)
		cacheCleanerStop = cachecleaner.StartCleaner(ctx, cleanerInterval, cacheDir, cacheTTL)
	} else {
		log.Println("Proxy cache cleaning is disabled (proxy disabled, caching disabled, or cache-dir not set).")
	}

	// --- Graceful Shutdown Handling ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	log.Println("Application started. Press Ctrl+C to shut down.")

	// Block until a termination signal is received
	sig := <-quit
	log.Printf("Shutdown signal received: %v. Starting graceful shutdown...", sig)

	// --- Trigger Shutdown ---
	// Signal all goroutines that depend on the context to stop.
	// The HTTP server's Start method will now call Stop internally.
	log.Println("Cancelling context to signal shutdown...")
	cancel()

	// --- Stop Services NOT directly using the main context ---
	// (Example: If cache cleaner didn't take context, stop it here)
	if cacheCleanerStop != nil {
		log.Println("Stopping cache cleaner...")
		cacheCleanerStop() // Call the specific stop function for the cleaner
		log.Println("Cache cleaner stopped.")
	}

	// --- Wait for Services to Finish ---
	// Wait for long-running goroutines (like the HTTP server) to complete their shutdown.
	log.Println("Waiting for background tasks (HTTP server) to complete...")
	wg.Wait() // Wait for HTTP server goroutine to finish (which includes its internal Stop call)

	log.Println("Application exiting.")
}
