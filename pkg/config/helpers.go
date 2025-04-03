package config

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// --- Helper Methods ---

// GetCacheTTL parses the proxy's cache TTL string.
func (c *CacheCfg) GetCacheTTL() (time.Duration, error) {
	ttlStr := c.CacheTTL // Use correct field name CacheTTL
	if ttlStr == "" {
		ttlStr = "7d" // Default if not set
	}
	d, err := StrToDuration(ttlStr)
	if err != nil {
		return 0, fmt.Errorf("invalid forward-proxy.cache.cache-ttl '%s': %w", ttlStr, err)
	}
	return d, nil
}

// GetCacheDir returns the cache directory.
func (c *CacheCfg) GetCacheDir() string {
	return c.CacheDir
}

// GetInterval parses the cleanup interval string.
func (c *CacheCleanupConfig) GetInterval() (time.Duration, error) {
	intervalStr := c.Interval
	if intervalStr == "" {
		intervalStr = "1h" // Default to 1 hour if not set
	}
	d, err := StrToDuration(intervalStr) // Use same parser
	if err != nil {
		return 0, fmt.Errorf("invalid proxy-cache-cleanup interval '%s': %w", intervalStr, err)
	}
	if d <= 0 { // Ensure interval is positive
		// Return default and log warning instead of error?
		log.Printf("WARN: proxy-cache-cleanup interval '%s' is not positive, using default 1h", intervalStr)
		return time.Hour, nil
	}
	return d, nil
}

// ShouldCacheDomain checks if a given host should be cached based on config.
// Performs case-insensitive comparison.
func (p *ProxyConfig) ShouldCacheDomain(host string) bool {
	if !p.Cache.Enabled || p.Cache.CacheDir == "" {
		// log.Printf("DBG: ShouldCacheDomain(%s): Cache disabled globally or no cache dir.", host) // Optional Debug
		return false
	}
	// Remove port if present (e.g., "example.com:80")
	hostOnly := strings.Split(host, ":")[0]
	hostLower := strings.ToLower(hostOnly)

	for _, domain := range p.Domains {
		domainLower := strings.ToLower(domain)
		// log.Printf("DBG: ShouldCacheDomain(%s): Checking against configured domain '%s'", hostLower, domainLower) // Optional Debug
		if domainLower == hostLower {
			// log.Printf("DBG: ShouldCacheDomain(%s): MATCH FOUND.", host) // Optional Debug
			return true
		}
	}
	// log.Printf("DBG: ShouldCacheDomain(%s): No match found in configured domains.", host) // Optional Debug
	return false
}

// --- Duration Parsing Helper (handles 'd' and 'w') ---

// StrToDuration converts a string defining time period and return a time.Duration
func StrToDuration(durationStr string) (time.Duration, error) {
	durationStr = strings.TrimSpace(durationStr)
	if durationStr == "" {
		return 0, fmt.Errorf("empty duration string")
	}
	if durationStr == "0" { // Allow "0" to explicitly disable (TTL)
		return 0, nil
	}

	var numStr string
	var unitStr string
	splitIndex := -1
	for i, r := range durationStr {
		if !unicode.IsDigit(r) && r != '.' {
			splitIndex = i
			break
		}
	}

	if splitIndex == -1 {
		// Might be just a number (treat as seconds? nanoseconds? error?)
		// Or a standard unit like "1h". Let time.ParseDuration handle it.
		numStr = durationStr
		unitStr = ""
	} else {
		numStr = durationStr[:splitIndex]
		unitStr = durationStr[splitIndex:]
	}

	unitStrLower := strings.ToLower(unitStr)

	switch unitStrLower {
	case "d":
		days, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number '%s' for days unit 'd': %w", numStr, err)
		}
		hours := days * 24
		// use high precision format specifier for float to string conversion
		d, err := time.ParseDuration(fmt.Sprintf("%fh", hours))
		if err != nil {
			return 0, fmt.Errorf("failed to parse calculated days to hours '%fh': %w", hours, err)
		}
		return d, nil
	case "w":
		weeks, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number '%s' for weeks unit 'w': %w", numStr, err)
		}
		hours := weeks * 7 * 24
		// use high precision format specifier for float to string conversion
		d, err := time.ParseDuration(fmt.Sprintf("%fh", hours))
		if err != nil {
			return 0, fmt.Errorf("failed to parse calculated weeks to hours '%fh': %w", hours, err)
		}
		return d, nil
	default:
		// Fallback to standard time.ParseDuration
		d, err := time.ParseDuration(durationStr)
		if err != nil {
			return 0, fmt.Errorf("failed to parse duration '%s' using standard units: %w", durationStr, err)
		}
		return d, nil
	}
}
