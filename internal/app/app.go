// Package app owns the runtime lifecycle of admin-bot's services: which ones
// are running, how configuration changes map to restarts, and graceful drain.
package app

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/mohammedhabas11/admin-bot/pkg/cachecleaner"
	"github.com/mohammedhabas11/admin-bot/pkg/config"
	"github.com/mohammedhabas11/admin-bot/pkg/httpserver"
)

// App coordinates the HTTP server and the proxy-cache cleanup worker.
type App struct {
	version string

	mu           sync.Mutex
	activeConfig *config.Config
	httpServer   *httpserver.Server
	cleanerStop  func()
	serverWg     sync.WaitGroup // tracks the HTTP server goroutine
}

// New creates an App; version is surfaced via the /healthz endpoint.
func New(version string) *App {
	return &App{version: version}
}

// ActiveConfig returns the config currently driving the running services.
func (a *App) ActiveConfig() *config.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.activeConfig
}

// Start brings up initial services based on cfg and records it as active.
func (a *App) Start(cfg *config.Config) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.activeConfig = cfg
	a.startServicesLocked(cfg)
}

// Reload applies a freshly loaded configuration: it stops only the services
// whose settings changed and starts them back with newCfg.
func (a *App) Reload(newCfg *config.Config) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.activeConfig == nil || newCfg == nil {
		slog.Warn("reloading from nil configuration; forcing full restart")
		a.stopServicesLocked(true, true)
		a.activeConfig = newCfg
		a.startServicesLocked(newCfg)
		return
	}

	restartServer, restartCleaner := CompareConfigs(a.activeConfig, newCfg)
	if !restartServer && !restartCleaner {
		slog.Info("no config changes requiring service restart")
		a.activeConfig = newCfg // keep comparison baseline fresh
		return
	}

	slog.Info("config changes detected; restarting relevant services")
	a.stopServicesLocked(restartServer, restartCleaner)
	a.activeConfig = newCfg
	a.startServicesLocked(newCfg)
	slog.Info("relevant services restarted with new configuration")
}

// Shutdown stops all services.
func (a *App) Shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopServicesLocked(true, true)
}

// Wait blocks until background service goroutines have finished draining.
func (a *App) Wait() {
	a.serverWg.Wait()
}

// CompareConfigs reports which services need a restart to apply newCfg.
func CompareConfigs(oldCfg, newCfg *config.Config) (restartServer bool, restartCleaner bool) {
	if oldCfg == nil || newCfg == nil {
		slog.Warn("comparing nil configurations; forcing restart")
		return true, true
	}

	// The server depends on everything under http except proxy-cache details:
	// cache-only tweaks are handled by restarting just the cleanup worker.
	if serverSectionDiffers(oldCfg.HTTP, newCfg.HTTP) {
		slog.Info("http configuration change requires server restart")
		restartServer = true
	}

	oldCacheActive := cacheActive(oldCfg)
	newCacheActive := cacheActive(newCfg)

	if newCacheActive {
		if !oldCacheActive ||
			oldCfg.ProxyCacheCleanup.Interval != newCfg.ProxyCacheCleanup.Interval ||
			oldCfg.HTTP.ForwardProxy.Cache.CacheDir != newCfg.HTTP.ForwardProxy.Cache.CacheDir ||
			oldCfg.HTTP.ForwardProxy.Cache.CacheTTL != newCfg.HTTP.ForwardProxy.Cache.CacheTTL {
			slog.Info("cache cleaner configuration change requires cleaner restart")
			restartCleaner = true
		}
	} else if oldCacheActive {
		slog.Info("cache cleaner disabled in new configuration; stopping")
		restartCleaner = true
	}

	return restartServer, restartCleaner
}

// serverSectionDiffers compares the HTTP sections ignoring proxy-cache fields,
// which never require bouncing the listener.
func serverSectionDiffers(old, new config.HTTPConfig) bool {
	old.ForwardProxy.Cache = config.CacheConfig{}
	new.ForwardProxy.Cache = config.CacheConfig{}
	return !reflect.DeepEqual(old, new)
}

// cacheActive reports whether the proxy cache worker should be running for cfg.
func cacheActive(cfg *config.Config) bool {
	fc := cfg.HTTP.ForwardProxy.Cache
	return cfg.HTTP.ForwardProxy.Enabled && fc.Enabled && fc.CacheDir != ""
}

// startServicesLocked starts services required by cfg if not already running,
// and stops any that cfg disables. Callers must hold a.mu.
func (a *App) startServicesLocked(cfg *config.Config) {
	slog.Debug("starting necessary services")

	switch {
	case cfg.HTTP.Enabled:
		if a.httpServer == nil {
			a.httpServer = httpserver.NewServer(cfg, a.version)
			a.serverWg.Add(1)
			go func(server *httpserver.Server) {
				defer a.serverWg.Done()
				slog.Debug("starting http server goroutine")
				// Background context: shutdown is driven by stopServicesLocked.
				if err := server.Start(context.Background()); err != nil {
					slog.Error("http server error", "err", err)
				}
				slog.Debug("http server goroutine finished")
			}(a.httpServer)
		} else {
			slog.Debug("http server already running")
		}
	default:
		slog.Info("http server disabled by configuration")
		if a.httpServer != nil {
			slog.Info("stopping http server as it is now disabled")
			if err := a.httpServer.Stop(); err != nil {
				slog.Error("error stopping disabled http server", "err", err)
			}
			a.httpServer = nil
		}
	}

	if cacheActive(cfg) {
		if a.cleanerStop == nil {
			interval, ttl := cleanerParams(cfg)
			a.cleanerStop = cachecleaner.StartCleaner(context.Background(), interval, cfg.HTTP.ForwardProxy.Cache.CacheDir, ttl)
		} else {
			slog.Debug("cache cleaner already running")
		}
	} else {
		slog.Info("proxy cache cleaning disabled by configuration")
		if a.cleanerStop != nil {
			slog.Info("stopping cache cleaner as it is now disabled")
			a.cleanerStop()
			a.cleanerStop = nil
		}
	}
	slog.Debug("startServices completed")
}

// stopServicesLocked selectively stops running services. Callers must hold a.mu.
func (a *App) stopServicesLocked(stopServer, stopCleaner bool) {
	slog.Debug("stopping services")

	if stopServer && a.httpServer != nil {
		slog.Info("stopping http server")
		if err := a.httpServer.Stop(); err != nil {
			slog.Error("error stopping http server", "err", err)
		} else {
			slog.Info("http server stop initiated")
		}
		a.httpServer = nil
	} else if stopServer {
		slog.Debug("http server stop requested but was not running")
	}

	if stopCleaner && a.cleanerStop != nil {
		slog.Info("stopping cache cleaner")
		a.cleanerStop()
		slog.Info("cache cleaner stopped")
		a.cleanerStop = nil
	} else if stopCleaner {
		slog.Debug("cache cleaner stop requested but was not running")
	}
	slog.Debug("stopServices completed")
}

// cleanerParams derives the cleanup worker settings with safe fallbacks.
func cleanerParams(cfg *config.Config) (interval time.Duration, ttl time.Duration) {
	interval, err := cfg.ProxyCacheCleanup.GetInterval()
	if err != nil {
		slog.Warn("invalid cache cleanup interval; using default", "err", err)
		interval = time.Hour
	}
	ttl, err = cfg.HTTP.ForwardProxy.Cache.GetCacheTTL()
	if err != nil {
		slog.Warn("invalid cache TTL; using default for cleanup", "err", err)
		ttl, _ = config.StrToDuration("7d")
	}
	return interval, ttl
}
