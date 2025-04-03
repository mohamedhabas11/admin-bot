package config

import (
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

var (
	currentConfig *Config
	configMutex   sync.RWMutex
)

// LoadConfig loads configuration from a file, sets defaults,
// unmarshals into the Config struct, watches for changes,
// and sends a signal on the reloadChan when config is successfully reloaded.
func LoadConfig(path string, reloadChan chan<- bool) (*Config, error) { // Added reloadChan parameter
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// --- Set Defaults ---
	v.SetDefault("http.enabled", true)
	v.SetDefault("http.addr", "0.0.0.0")
	v.SetDefault("http.port", 8080)
	v.SetDefault("http.static.enabled", false)
	v.SetDefault("http.forward-proxy.enabled", false)
	v.SetDefault("http.forward-proxy.cache.enabled", false)
	v.SetDefault("http.forward-proxy.cache.cache-ttl", "7d")
	v.SetDefault("proxy-cache-cleanup.interval", "1h")
	// --- End Defaults ---

	err := v.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
				log.Printf("INFO: Config file not found at %s, using defaults.", path)
			} else {
				return nil, fmt.Errorf("config file found at %s but could not be read: %w", path, err)
			}
		} else {
			return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
		}
	}

	var initialCfg Config
	if err := v.Unmarshal(&initialCfg); err != nil {
		return nil, fmt.Errorf("unable to decode initial config into struct: %w", err)
	}

	// applyDefaults(&initialCfg) // Add back if needed

	if !validateConfig(&initialCfg) {
		log.Println("WARNING: Initial configuration contains validation warnings.")
	}

	configMutex.Lock()
	currentConfig = &initialCfg
	configMutex.Unlock()

	v.WatchConfig()
	v.OnConfigChange(func(e fsnotify.Event) {
		log.Printf("Config file changed: %s. Reloading...", e.Name)
		if err := v.ReadInConfig(); err != nil {
			log.Printf("ERROR: Error re-reading config file on change: %v", err)
			return
		}
		var tempCfg Config
		if err := v.Unmarshal(&tempCfg); err != nil {
			log.Printf("ERROR: Failed to reload config into struct: %v", err)
			return
		}
		// applyDefaults(&tempCfg) // Add back if needed
		if !validateConfig(&tempCfg) {
			log.Printf("ERROR: Reloaded configuration is invalid. Keeping previous configuration.")
			return
		}

		// --- Update global config AND notify main ---
		configMutex.Lock()
		currentConfig = &tempCfg
		configMutex.Unlock()
		log.Println("Configuration reloaded successfully.")

		// Send signal to main goroutine
		if reloadChan != nil {
			// Use non-blocking send in case main isn't ready (though it should be)
			select {
			case reloadChan <- true:
				log.Println("Sent reload signal to main.")
			default:
				log.Println("WARN: Failed to send reload signal to main (channel full or nil).")
			}
		}
		// --- End notification ---
	})

	log.Printf("Configuration loaded successfully from %s (or defaults).", v.ConfigFileUsed())
	return currentConfig, nil
}

// GetConfig provides thread-safe access to the current configuration.
func GetConfig() *Config {
	configMutex.RLock()
	defer configMutex.RUnlock()
	if currentConfig == nil {
		log.Println("WARNING: GetConfig called before LoadConfig completed or after failure.")
		return &Config{}
	}
	// Return a deep copy? For now, return pointer but be aware it can change.
	// A safer pattern is for components to get the config once at startup/restart.
	return currentConfig
}

// validateConfig checks the validity of the loaded configuration.
func validateConfig(cfg *Config) bool {
	isValid := true
	// Validate Proxy Cache Settings
	if cfg.HTTP.ForwardProxy.Enabled && cfg.HTTP.ForwardProxy.Cache.Enabled {
		if cfg.HTTP.ForwardProxy.Cache.CacheDir == "" {
			log.Println("WARNING: http.forward-proxy.cache.enabled is true, but cache-dir is not set. Caching will be disabled.")
		}
		if _, err := cfg.HTTP.ForwardProxy.Cache.GetCacheTTL(); err != nil {
			log.Printf("WARNING: Invalid format for http.forward-proxy.cache.cache-ttl ('%s'): %v. Caching might use default or fail.", cfg.HTTP.ForwardProxy.Cache.CacheTTL, err)
			// isValid = false // Decide if fatal
		}
	}
	// Validate Cleanup Interval
	if cfg.HTTP.ForwardProxy.Enabled && cfg.HTTP.ForwardProxy.Cache.Enabled && cfg.HTTP.ForwardProxy.Cache.CacheDir != "" {
		if _, err := cfg.ProxyCacheCleanup.GetInterval(); err != nil {
			log.Printf("WARNING: Invalid format for proxy-cache-cleanup.interval ('%s'): %v. Cleanup might use default or fail.", cfg.ProxyCacheCleanup.Interval, err)
			// isValid = false // Decide if fatal
		}
	}
	return isValid
}

// applyDefaults function can be added back here if needed for mirror or other defaults
// func applyDefaults(cfg *Config) { ... }
