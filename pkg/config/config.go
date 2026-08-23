package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

var (
	currentConfig *Config
	configMutex   sync.RWMutex
	viperInstance *viper.Viper // Keep viper instance for watching
)

// loadAndValidate performs the core config reading, unmarshalling, and validation.
// It does NOT handle file watching or global state.
func loadAndValidate(path string) (*Config, error) {
	v := viper.New() // Use a temporary viper instance for loading/validation
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// Set defaults directly on the temporary instance
	setDefaults(v)

	// Attempt to read the config file
	err := v.ReadInConfig()
	if err != nil {
		// Return specific errors for handling upstream
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Check if it *really* doesn't exist vs. permission error etc.
			if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
				return nil, err // Return the specific ConfigFileNotFoundError
			}
			// File exists but couldn't be read (permissions? format?)
			return nil, fmt.Errorf("config file found at %s but could not be read: %w", path, err)
		}
		// Other error (e.g., YAML parsing error)
		return nil, fmt.Errorf("failed to read/parse config file %s: %w", path, err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to decode config from %s into struct: %w", path, err)
	}

	// Validate the loaded configuration
	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	slog.Info("configuration loaded and validated", "path", path)
	return &cfg, nil
}

// ValidateConfigFile attempts to load and validate a config file.
// Used by the -validate CLI flag. Returns nil on success, error on failure.
func ValidateConfigFile(path string) error {
	_, err := loadAndValidate(path)
	// For validation command, treat "file not found" as an error too
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Use the original error message from viper if possible
			return fmt.Errorf("config file validation failed: %w", err)
		}
		// Return other errors (parsing, validation, read errors)
		return fmt.Errorf("config file validation failed: %w", err)
	}
	return nil // Success
}

// LoadConfig loads the main application configuration, sets up watching,
// and handles the initial load, potentially using defaults if file not found.
// It FATALS on unrecoverable errors during initial load (parsing, validation).
func LoadConfig(path string, reloadChan chan<- bool) (*Config, error) {
	// Use a persistent viper instance for watching
	viperInstance = viper.New()
	viperInstance.SetConfigFile(path)
	viperInstance.SetConfigType("yaml")
	setDefaults(viperInstance) // Set defaults on the persistent instance too

	// Perform initial load and validation using the core function
	initialCfg, err := loadAndValidate(path)

	// Handle initial load errors specifically for the running service
	if err != nil {
		// Allow service to start with defaults ONLY if the error is file not found
		var isFileNotFoundError bool
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			isFileNotFoundError = true
		}

		if isFileNotFoundError {
			slog.Info("config file not found; running with defaults", "path", path)
			// Create config purely from defaults set on viperInstance
			var defaultCfg Config
			if defaultUnmarshalErr := viperInstance.Unmarshal(&defaultCfg); defaultUnmarshalErr != nil {
				// This should be rare if defaults are simple
				return nil, fmt.Errorf("failed to unmarshal default configuration: %w", defaultUnmarshalErr)
			}
			if err := validateConfig(&defaultCfg); err != nil {
				// If even defaults are invalid, treat as fatal
				return nil, fmt.Errorf("default configuration is invalid, cannot start: %w", err)
			}
			initialCfg = &defaultCfg // Use the validated default config
			slog.Info("initialized with default configuration")
			// Clear the error since we are proceeding with defaults
			err = nil
		} else {
			// Any other error (parsing, validation, read error) is fatal on initial load
			return nil, fmt.Errorf("unrecoverable error loading initial configuration: %w", err)
		}
	}

	// Set initial global config only if no fatal error occurred
	if err == nil {
		configMutex.Lock()
		currentConfig = initialCfg
		configMutex.Unlock()
	} else {
		// This path should ideally not be reached due to fatal error handling above,
		// but included for completeness.
		return nil, err
	}

	// --- Setup Watcher using the persistent viperInstance ---
	// Watch the specific file used, necessary if path wasn't found initially
	// but might be created later. Viper needs to know *what* to watch.
	viperInstance.WatchConfig()
	viperInstance.OnConfigChange(func(e fsnotify.Event) {
		slog.Info("config file changed; reloading", "path", e.Name)

		// Re-read using the persistent viper instance
		if err := viperInstance.ReadInConfig(); err != nil {
			// Log error, but don't necessarily stop watching or kill app
			// Maybe the file is temporarily unreadable?
			slog.Error("error re-reading config file on change", "err", err)
			return // Keep old config if re-read fails
		}

		var tempCfg Config
		if err := viperInstance.Unmarshal(&tempCfg); err != nil {
			slog.Error("failed to decode reloaded config", "err", err)
			return // Keep old config if unmarshal fails
		}

		if err := validateConfig(&tempCfg); err != nil {
			slog.Error("reloaded config invalid; keeping previous", "err", err)
			return
		}

		// Update global config atomically
		configMutex.Lock()
		currentConfig = &tempCfg
		configMutex.Unlock()
		slog.Info("configuration reloaded")

		// Send signal to main goroutine
		if reloadChan != nil {
			select {
			case reloadChan <- true:
				slog.Debug("sent reload signal to main")
			default:
				slog.Warn("failed to send reload signal (channel full or nil)")
			}
		}
	})

	slog.Info("configuration monitoring active", "path", viperInstance.ConfigFileUsed())
	return currentConfig, nil // Return the initial config (loaded or default)
}

// setDefaults applies default values using Viper.
func setDefaults(v *viper.Viper) {
	v.SetDefault("http.enabled", true)
	v.SetDefault("http.addr", "0.0.0.0")
	v.SetDefault("http.port", 8080)
	v.SetDefault("http.static.enabled", false)
	v.SetDefault("http.forward-proxy.enabled", false)
	v.SetDefault("http.forward-proxy.cache.enabled", false)
	v.SetDefault("http.forward-proxy.cache.cache-ttl", "7d")
	v.SetDefault("proxy-cache-cleanup.interval", "1h")
}

// GetConfig provides thread-safe access to the current configuration.
func GetConfig() *Config {
	configMutex.RLock()
	defer configMutex.RUnlock()
	if currentConfig == nil {
		slog.Warn("GetConfig called before LoadConfig completed")
		return &Config{}
	}
	return currentConfig
}

// validateConfig checks the validity of the loaded configuration.
// All problems are collected and returned as a single joined error so callers
// see every issue at once instead of only the first one.
func validateConfig(cfg *Config) error {
	var errs []error

	// Proxy cache settings are only relevant when both switches are on.
	if cfg.HTTP.ForwardProxy.Enabled && cfg.HTTP.ForwardProxy.Cache.Enabled {
		if cfg.HTTP.ForwardProxy.Cache.CacheDir == "" {
			errs = append(errs, errors.New("http.forward-proxy.cache.cache-dir is required when caching is enabled"))
		}
		if _, err := cfg.HTTP.ForwardProxy.Cache.GetCacheTTL(); err != nil {
			errs = append(errs, fmt.Errorf("invalid http.forward-proxy.cache.cache-ttl %q: %w", cfg.HTTP.ForwardProxy.Cache.CacheTTL, err))
		}

		// The cleanup worker only runs alongside an active proxy cache.
		if _, err := cfg.ProxyCacheCleanup.GetInterval(); err != nil {
			errs = append(errs, fmt.Errorf("invalid proxy-cache-cleanup.interval %q: %w", cfg.ProxyCacheCleanup.Interval, err))
		}
	}

	return errors.Join(errs...)
}
