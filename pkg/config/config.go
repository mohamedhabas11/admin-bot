package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/fsnotify/fsnotify"
	// "github.com/robfig/cron/v3" // Only needed if validating cron strings
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

	// Apply defaults that might depend on structure (like default index path)
	applyDefaults(&cfg)

	// Validate the loaded configuration
	if !validateConfig(&cfg) {
		// Return a generic validation error
		return &cfg, errors.New("configuration validation failed (see warnings/errors above)")
	}

	log.Printf("Configuration successfully loaded and validated from %s.", path)
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
			log.Printf("INFO: Config file not found at %s. Attempting to run with defaults.", path)
			// Create config purely from defaults set on viperInstance
			var defaultCfg Config
			if defaultUnmarshalErr := viperInstance.Unmarshal(&defaultCfg); defaultUnmarshalErr != nil {
				// This should be rare if defaults are simple
				return nil, fmt.Errorf("failed to unmarshal default configuration: %w", defaultUnmarshalErr)
			}
			applyDefaults(&defaultCfg) // Apply structural defaults
			if !validateConfig(&defaultCfg) {
				// If even defaults are invalid, treat as fatal
				return nil, errors.New("default configuration is invalid, cannot start")
			}
			initialCfg = &defaultCfg // Use the validated default config
			log.Println("Successfully initialized with default configuration.")
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
		log.Printf("Config file changed: %s. Reloading...", e.Name)

		// Re-read using the persistent viper instance
		if err := viperInstance.ReadInConfig(); err != nil {
			// Log error, but don't necessarily stop watching or kill app
			// Maybe the file is temporarily unreadable?
			log.Printf("ERROR: Error re-reading config file on change: %v", err)
			return // Keep old config if re-read fails
		}

		var tempCfg Config
		if err := viperInstance.Unmarshal(&tempCfg); err != nil {
			log.Printf("ERROR: Failed to reload config into struct: %v", err)
			return // Keep old config if unmarshal fails
		}

		applyDefaults(&tempCfg) // Apply structural defaults

		if !validateConfig(&tempCfg) {
			log.Printf("ERROR: Reloaded configuration is invalid. Keeping previous configuration.")
			return
		}

		// Update global config atomically
		configMutex.Lock()
		currentConfig = &tempCfg
		configMutex.Unlock()
		log.Println("Configuration reloaded successfully.")

		// Send signal to main goroutine
		if reloadChan != nil {
			select {
			case reloadChan <- true:
				log.Println("Sent reload signal to main.")
			default:
				log.Println("WARN: Failed to send reload signal to main (channel full or nil).")
			}
		}
	})

	log.Printf("Configuration monitoring active for %s (or defaults).", viperInstance.ConfigFileUsed())
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

// applyDefaults sets default values for nested config fields if they are empty.
// This is needed for defaults that depend on other values.
func applyDefaults(cfg *Config) {
	// Add any necessary structural defaults here if needed in the future
	// Example:
	// if cfg.SomeSection.SomeField == "" && cfg.SomeSection.OtherField != "" {
	//     cfg.SomeSection.SomeField = calculateDefault(cfg.SomeSection.OtherField)
	// }
}

// GetConfig provides thread-safe access to the current configuration.
func GetConfig() *Config {
	configMutex.RLock()
	defer configMutex.RUnlock()
	if currentConfig == nil {
		log.Println("WARNING: GetConfig called before LoadConfig completed or after failure.")
		return &Config{}
	}
	return currentConfig
}

// validateConfig checks the validity of the loaded configuration.
// Returns true if valid, false otherwise. Logs warnings/errors.
func validateConfig(cfg *Config) bool {
	isValid := true
	errorPrefix := "Config validation error:" // Prefix for fatal validation errors

	// Validate Proxy Cache Settings
	if cfg.HTTP.ForwardProxy.Enabled && cfg.HTTP.ForwardProxy.Cache.Enabled {
		if cfg.HTTP.ForwardProxy.Cache.CacheDir == "" {
			log.Printf("%s http.forward-proxy.cache.enabled is true, but cache-dir is not set.", errorPrefix)
			isValid = false // Make this an error
		}
		if _, err := cfg.HTTP.ForwardProxy.Cache.GetCacheTTL(); err != nil {
			log.Printf("%s Invalid format for http.forward-proxy.cache.cache-ttl ('%s'): %v.", errorPrefix, cfg.HTTP.ForwardProxy.Cache.CacheTTL, err)
			isValid = false // Make this an error
		}
	}
	// Validate Cleanup Interval (only relevant if proxy caching is enabled)
	if cfg.HTTP.ForwardProxy.Enabled && cfg.HTTP.ForwardProxy.Cache.Enabled && cfg.HTTP.ForwardProxy.Cache.CacheDir != "" {
		if _, err := cfg.ProxyCacheCleanup.GetInterval(); err != nil {
			log.Printf("%s Invalid format for proxy-cache-cleanup.interval ('%s'): %v.", errorPrefix, cfg.ProxyCacheCleanup.Interval, err)
			isValid = false // Make this an error
		}
	}

	// Validate Static Dirs Exist? Optional, might be annoying if dirs are created later.
	// if cfg.HTTP.Static.Enabled {
	// 	for key, dirCfg := range cfg.HTTP.Static.Dirs {
	// 		if dirCfg.Path == "" {
	// 			log.Printf("%s http.static.dirs.%s: path cannot be empty.", errorPrefix, key)
	// 			isValid = false
	// 			continue
	// 		}
	// 		if _, err := os.Stat(dirCfg.Path); os.IsNotExist(err) {
	// 			log.Printf("WARNING: Static directory path for '%s' does not exist: %s", key, dirCfg.Path)
	// 			// isValid = false // Decide if this is a warning or error
	// 		} else if err != nil {
	// 			log.Printf("%s Cannot access static directory path for '%s' (%s): %v", errorPrefix, key, dirCfg.Path, err)
	// 			isValid = false
	// 		}
	// 	}
	// }

	return isValid
}
