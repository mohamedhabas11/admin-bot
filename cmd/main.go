package main

import (
	"context"
	"flag" // Import flag
	"fmt"
	"log/slog"
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

// --- Command Line Flags ---
var (
	validatePath = flag.String("validate", "", "Path to config file to validate only.")
	configPath   = flag.String("config", "", "Path to config file (overrides ENV var).") // Optional explicit path flag
)

// --- Environment Variable ---
const ConfigPathEnvVar = "ADMINBOT_CONFIG_PATH" // Name of the ENV VAR

// Global state for running services (protected by mutex)
var (
	appStateMutex      sync.Mutex
	activeConfig       *config.Config // Config currently used by running services
	currentHttpServer  *httpserver.Server
	currentCleanerStop func()
	serverWg           sync.WaitGroup // WaitGroup specifically for the server goroutine
)

func main() {
	flag.Parse() // Parse command line flags first

	// --- Handle Validation Command ---
	if *validatePath != "" {
		fmt.Printf("Validating configuration file: %s\n", *validatePath)
		// Use the dedicated validation function from the config package
		err := config.ValidateConfigFile(*validatePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Validation Failed: %v\n", err)
			os.Exit(1) // Exit with error code
		}
		fmt.Println("Configuration file is valid.")
		os.Exit(0) // Exit successfully
	}

	// --- Determine Config Path for Running Service ---
	finalConfigPath := "config.yaml" // Default path
	if *configPath != "" {
		finalConfigPath = *configPath // Use -config flag if provided
		slog.Info("using config path from -config flag", "path", finalConfigPath)
	} else {
		envPath := os.Getenv(ConfigPathEnvVar)
		if envPath != "" {
			finalConfigPath = envPath // Use ENV var if provided and -config wasn't
			slog.Info("using config path from environment", "env", ConfigPathEnvVar, "path", finalConfigPath)
		} else {
			slog.Info("using default config path", "path", finalConfigPath)
		}
	}

	// --- Initial Setup ---
	slog.Info("starting admin-bot")

	// Channel for signaling config reloads
	reloadChan := make(chan bool, 1)

	// Load initial configuration and start watching
	// LoadConfig now FATALS on unrecoverable initial load errors (except file not found with defaults)
	initialCfg, err := config.LoadConfig(finalConfigPath, reloadChan)
	if err != nil {
		slog.Error("failed to load initial configuration", "path", finalConfigPath, "err", err)
		os.Exit(1)
	}
	activeConfig = initialCfg // Set the initial active config

	// Start initial services based on the first loaded config
	startServices(activeConfig)

	// --- Graceful Shutdown / Reload Handling ---
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	slog.Info("application started; press Ctrl+C to shut down")

	// Main loop to wait for signals or reload triggers
	keepRunning := true
	for keepRunning {
		select {
		case sig := <-signalChan:
			slog.Info("shutdown signal received; starting graceful shutdown", "signal", sig)
			keepRunning = false      // Exit loop after handling shutdown
			stopServices(true, true) // Stop all services on shutdown

		case <-reloadChan:
			slog.Info("reload signal received; checking for necessary restarts")
			newCfg := config.GetConfig() // Get the newly loaded config

			// --- Compare configurations ---
			restartServer, restartCleaner := compareConfigs(activeConfig, newCfg)

			if !restartServer && !restartCleaner {
				slog.Info("no config changes requiring service restart")
				// Update activeConfig even if no restart, so next comparison is correct
				appStateMutex.Lock()
				activeConfig = newCfg
				appStateMutex.Unlock()
				continue // Go back to waiting for signals
			}

			slog.Info("config changes detected; restarting relevant services")
			stopServices(restartServer, restartCleaner) // Stop only affected services

			// Update active config *before* starting with it
			appStateMutex.Lock()
			activeConfig = newCfg
			appStateMutex.Unlock()

			startServices(activeConfig) // Start services (will only start those stopped)
			slog.Info("relevant services restarted with new configuration")
		}
	}

	// --- Wait for Services to Finish on Shutdown ---
	slog.Info("waiting for background tasks to complete")
	serverWg.Wait() // Wait for HTTP server goroutine to finish its shutdown

	slog.Info("application exiting")
}

// compareConfigs checks if restarts are needed based on config differences.
func compareConfigs(oldCfg, newCfg *config.Config) (restartServer bool, restartCleaner bool) {
	if oldCfg == nil || newCfg == nil {
		slog.Warn("comparing nil configurations; forcing restart")
		return true, true // Force restart if something went wrong
	}

	// 1. Check for HTTP Server restart conditions
	// Use DeepEqual for simplicity and robustness across all HTTP settings
	if !reflect.DeepEqual(oldCfg.HTTP, newCfg.HTTP) {
		slog.Info("http configuration change requires server restart")
		restartServer = true
	}

	// 2. Check for Cache Cleaner restart conditions
	// Cleaner depends on interval and the proxy cache settings
	oldProxyCacheEnabled := oldCfg.HTTP.ForwardProxy.Enabled && oldCfg.HTTP.ForwardProxy.Cache.Enabled && oldCfg.HTTP.ForwardProxy.Cache.CacheDir != ""
	newProxyCacheEnabled := newCfg.HTTP.ForwardProxy.Enabled && newCfg.HTTP.ForwardProxy.Cache.Enabled && newCfg.HTTP.ForwardProxy.Cache.CacheDir != ""

	// Compare relevant fields only if the cleaner *should* be running in the new config
	if newProxyCacheEnabled {
		// Restart if cleaner wasn't running before OR if its settings changed
		if !oldProxyCacheEnabled ||
			oldCfg.ProxyCacheCleanup.Interval != newCfg.ProxyCacheCleanup.Interval ||
			oldCfg.HTTP.ForwardProxy.Cache.CacheDir != newCfg.HTTP.ForwardProxy.Cache.CacheDir ||
			oldCfg.HTTP.ForwardProxy.Cache.CacheTTL != newCfg.HTTP.ForwardProxy.Cache.CacheTTL {
			slog.Info("cache cleaner configuration change requires cleaner restart")
			restartCleaner = true
		}
	} else {
		// If cleaner should NOT be running in new config, check if it WAS running before
		if oldProxyCacheEnabled {
			slog.Info("cache cleaner disabled in new configuration; stopping")
			restartCleaner = true // Signal stop needed
		}
	}

	return restartServer, restartCleaner
}

// startServices starts services based on config, only if they aren't already running.
func startServices(cfg *config.Config) {
	appStateMutex.Lock()
	defer appStateMutex.Unlock()

	slog.Debug("starting necessary services")

	// --- Start HTTP Server ---
	if cfg.HTTP.Enabled {
		if currentHttpServer == nil { // Only start if not already running
			currentHttpServer = httpserver.NewServer(cfg)
			serverWg.Add(1)
			go func(server *httpserver.Server) {
				defer serverWg.Done()
				slog.Debug("starting http server goroutine")
				// Use a background context - shutdown is handled by stopServices
				if err := server.Start(context.Background()); err != nil {
					slog.Error("http server error", "err", err)
				}
				slog.Debug("http server goroutine finished")
			}(currentHttpServer)
		} else {
			slog.Debug("http server already running")
		}
	} else {
		slog.Info("http server disabled by configuration")
		// Ensure server is stopped if it was running and is now disabled
		if currentHttpServer != nil {
			slog.Info("stopping http server as it is now disabled")
			if err := currentHttpServer.Stop(); err != nil {
				slog.Error("error stopping disabled http server", "err", err)
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
				slog.Warn("invalid cache cleanup interval; using default", "err", err)
				cleanerInterval = time.Hour
			}
			cacheDir := cfg.HTTP.ForwardProxy.Cache.CacheDir
			cacheTTL, err := cfg.HTTP.ForwardProxy.Cache.GetCacheTTL()
			if err != nil {
				slog.Warn("invalid cache TTL; using default for cleanup", "err", err)
				cacheTTL, _ = config.StrToDuration("7d")
			}
			currentCleanerStop = cachecleaner.StartCleaner(context.Background(), cleanerInterval, cacheDir, cacheTTL)
		} else {
			slog.Debug("cache cleaner already running")
		}
	} else {
		slog.Info("proxy cache cleaning disabled by configuration")
		// Ensure cleaner is stopped if it was running and is now disabled
		if currentCleanerStop != nil {
			slog.Info("stopping cache cleaner as it is now disabled")
			currentCleanerStop()
			currentCleanerStop = nil
		}
	}
	slog.Debug("startServices completed")
}

// stopServices gracefully stops running services selectively.
func stopServices(stopServer bool, stopCleaner bool) {
	appStateMutex.Lock()
	defer appStateMutex.Unlock()

	slog.Debug("stopping services")

	// Stop HTTP Server
	if stopServer && currentHttpServer != nil {
		slog.Info("stopping http server")
		if err := currentHttpServer.Stop(); err != nil {
			slog.Error("error stopping http server", "err", err)
		} else {
			slog.Info("http server stop initiated")
		}
		currentHttpServer = nil // Clear variable after initiating stop
	} else if stopServer {
		slog.Debug("http server stop requested but was not running")
	}

	// Stop Cache Cleaner
	if stopCleaner && currentCleanerStop != nil {
		slog.Info("stopping cache cleaner")
		currentCleanerStop()
		slog.Info("cache cleaner stopped")
		currentCleanerStop = nil // Clear variable
	} else if stopCleaner {
		slog.Debug("cache cleaner stop requested but was not running")
	}
	slog.Debug("stopServices completed")
}
