package config

import (
	"fmt"
	"log"
	"os"
	"sync" // Import sync for mutex

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

var (
	currentConfig *Config
	configMutex   sync.RWMutex
)

// LoadConfig loads configuration from a file, sets defaults,
// unmarshals into the Config struct, and watches for changes.
// It now manages a global currentConfig internally.
func LoadConfig(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml") // Explicitly set type

	// --- Set Defaults for the New Structure ---
	v.SetDefault("http.enabled", true)
	v.SetDefault("http.addr", "0.0.0.0")
	v.SetDefault("http.port", 8080)

	v.SetDefault("http.static.enabled", false) // Default static serving off

	v.SetDefault("http.forward-proxy.enabled", false)       // Default proxy off
	v.SetDefault("http.forward-proxy.cache.enabled", false) // Default caching off
	v.SetDefault("http.forward-proxy.cache.cache-ttl", "7d")
	// No default for cache-dir or domains

	// Note: proxy-cache-cleanup.enabled is implicitly handled based on proxy cache settings
	v.SetDefault("proxy-cache-cleanup.interval", "1h")
	// --- End Defaults ---

	// Attempt to read the config file
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

	// --- Post-Unmarshal Validation ---
	if !validateConfig(&initialCfg) {
		// Decide if invalid config should prevent startup
		log.Println("WARNING: Initial configuration contains validation warnings.")
		// return nil, fmt.Errorf("initial configuration is invalid") // Uncomment to make validation fatal
	}

	// Set initial global config
	configMutex.Lock()
	currentConfig = &initialCfg
	configMutex.Unlock()

	// Watch for changes - NOTE: onChange callback removed, reload handled internally
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

		// Validate the newly loaded config
		if !validateConfig(&tempCfg) {
			log.Printf("ERROR: Reloaded configuration is invalid. Keeping previous configuration.")
			return
		}

		// Update the global config atomically
		configMutex.Lock()
		currentConfig = &tempCfg
		configMutex.Unlock()
		log.Println("Configuration reloaded successfully.")
		// NOTE: The application components (server, cleaner) need to be designed
		// to periodically check the global config or be restarted/reconfigured.
		// A simple approach is often to restart the affected components.
		// A more complex approach involves channels or callbacks for dynamic updates.
		// For now, we assume components might need manual restart or check GetConfig().
	})

	log.Printf("Configuration loaded successfully from %s (or defaults).", v.ConfigFileUsed())
	return currentConfig, nil // Return the initial config
}

// GetConfig provides thread-safe access to the current configuration.
func GetConfig() *Config {
	configMutex.RLock()
	defer configMutex.RUnlock()
	// Return a pointer. Callers should be aware it might change.
	// For safety, components could copy the relevant parts they need.
	if currentConfig == nil {
		// Should not happen if LoadConfig was called, but handle defensively
		log.Println("WARNING: GetConfig called before LoadConfig completed or after failure.")
		// Return empty config to avoid nil pointer dereference
		return &Config{}
	}
	return currentConfig
}

// validateConfig checks the validity of the loaded configuration.
// Returns true if valid, false otherwise. Logs warnings/errors.
func validateConfig(cfg *Config) bool {
	isValid := true
	// Validate Proxy Cache Settings
	if cfg.HTTP.ForwardProxy.Enabled && cfg.HTTP.ForwardProxy.Cache.Enabled {
		if cfg.HTTP.ForwardProxy.Cache.CacheDir == "" {
			log.Println("WARNING: http.forward-proxy.cache.enabled is true, but cache-dir is not set. Caching will be disabled.")
			// We don't modify cfg here, let consuming code check CacheDir
		}
		if _, err := cfg.HTTP.ForwardProxy.Cache.GetCacheTTL(); err != nil {
			log.Printf("WARNING: Invalid format for http.forward-proxy.cache.cache-ttl ('%s'): %v. Caching might use default or fail.", cfg.HTTP.ForwardProxy.Cache.CacheTTL, err)
			// Decide if this is a fatal error
			// isValid = false
		}
	}
	// Validate Cleanup Interval (only relevant if proxy caching is enabled)
	if cfg.HTTP.ForwardProxy.Enabled && cfg.HTTP.ForwardProxy.Cache.Enabled && cfg.HTTP.ForwardProxy.Cache.CacheDir != "" {
		if _, err := cfg.ProxyCacheCleanup.GetInterval(); err != nil {
			log.Printf("WARNING: Invalid format for proxy-cache-cleanup.interval ('%s'): %v. Cleanup might use default or fail.", cfg.ProxyCacheCleanup.Interval, err)
			// Decide if this is a fatal error
			// isValid = false
		}
	}

	// Add more validation rules as needed (e.g., check static dir paths exist?)

	return isValid
}
