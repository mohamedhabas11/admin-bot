package config

// Config holds the application's entire configuration.
type Config struct {
	HTTP              HTTPConfig         `mapstructure:"http"`
	ProxyCacheCleanup CacheCleanupConfig `mapstructure:"proxy-cache-cleanup"`
}

// HTTPConfig holds all settings related to the main HTTP server.
type HTTPConfig struct {
	Enabled      bool         `mapstructure:"enabled"`
	Addr         string       `mapstructure:"addr"`
	Port         int          `mapstructure:"port"`
	Static       StaticConfig `mapstructure:"static"`
	ForwardProxy ProxyConfig  `mapstructure:"forward-proxy"` // Matches YAML key
}

// StaticConfig holds settings for serving static files.
type StaticConfig struct {
	Enabled bool                       `mapstructure:"enabled"`
	Dirs    map[string]StaticDirConfig `mapstructure:"dirs"` // Key is route path component
}

// StaticDirConfig defines a single directory to be served statically.
type StaticDirConfig struct {
	Path string `mapstructure:"path"` // Local filesystem path
}

// ProxyConfig holds settings for the forward proxy functionality.
type ProxyConfig struct {
	Enabled bool     `mapstructure:"enabled"`
	Cache   CacheCfg `mapstructure:"cache"`
	Domains []string `mapstructure:"domains"` // Domains to cache (exact match)
}

// CacheCfg holds caching specific settings for the proxy.
type CacheCfg struct {
	Enabled  bool   `mapstructure:"enabled"`
	CacheDir string `mapstructure:"cache-dir"`
	CacheTTL string `mapstructure:"cache-ttl"` // Keep as string from YAML
}

// CacheCleanupConfig holds settings for the background cache cleaner worker.
type CacheCleanupConfig struct {
	// Enabled bool `mapstructure:"enabled"` // Implicitly enabled if proxy caching is on
	Interval string `mapstructure:"interval"` // How often to run cleanup
}
