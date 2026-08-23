package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

func TestLoadAndValidateValidConfig(t *testing.T) {
	cfg, err := loadAndValidate(writeTempConfig(t, `
http:
  enabled: true
  addr: "127.0.0.1"
  port: 9090
  forward-proxy:
    enabled: true
    cache:
      enabled: true
      cache-dir: "/tmp/admin-bot-cache"
      cache-ttl: "3d"
proxy-cache-cleanup:
  interval: "30m"
`))
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if cfg.HTTP.Port != 9090 || cfg.HTTP.Addr != "127.0.0.1" {
		t.Errorf("unexpected http settings: addr=%s port=%d", cfg.HTTP.Addr, cfg.HTTP.Port)
	}
	ttl, err := cfg.HTTP.ForwardProxy.Cache.GetCacheTTL()
	if err != nil || ttl != 72*time.Hour {
		t.Errorf("cache TTL = %v (err=%v), want 72h", ttl, err)
	}
}

func TestValidateAggregatesAllProblems(t *testing.T) {
	cfg := &Config{}
	cfg.HTTP.ForwardProxy.Enabled = true
	cfg.HTTP.ForwardProxy.Cache.Enabled = true
	cfg.HTTP.ForwardProxy.Cache.CacheTTL = "not-a-duration"
	cfg.ProxyCacheCleanup.Interval = "-5m"

	err := validateConfig(cfg)
	if err == nil {
		t.Fatal("invalid config passed validation")
	}
	for _, want := range []string{"cache-dir", "cache-ttl", "interval"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("validation error missing %q:\n%v", want, err)
		}
	}
}

func TestValidateAcceptsDisabledProxyWithEmptyCache(t *testing.T) {
	cfg := &Config{} // proxy disabled everywhere: nothing to validate
	if err := validateConfig(cfg); err != nil {
		t.Errorf("disabled proxy should not require cache settings: %v", err)
	}
}

func TestValidateRejectsNonPositiveCleanupInterval(t *testing.T) {
	cfg := &Config{}
	cfg.HTTP.ForwardProxy.Enabled = true
	cfg.HTTP.ForwardProxy.Cache.Enabled = true
	cfg.HTTP.ForwardProxy.Cache.CacheDir = "/tmp/cache"
	cfg.ProxyCacheCleanup.Interval = "0"

	err := validateConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "positive") {
		t.Errorf("want positive-interval error, got: %v", err)
	}
}

func TestGetIntervalDefaultsWhenUnset(t *testing.T) {
	interval, err := (&CacheCleanupConfig{}).GetInterval()
	if err != nil {
		t.Fatalf("unset interval should default cleanly: %v", err)
	}
	if interval != time.Hour {
		t.Errorf("default interval = %v, want 1h", interval)
	}
}

func TestStrToDurationExtendedUnits(t *testing.T) {
	cases := map[string]time.Duration{
		"7d":   7 * 24 * time.Hour,
		"2w":   14 * 24 * time.Hour,
		"90m":  90 * time.Minute,
		"1h":   time.Hour,
		"0":    0,
		"0.5d": 12 * time.Hour,
	}
	for in, want := range cases {
		got, err := StrToDuration(in)
		if err != nil {
			t.Errorf("StrToDuration(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("StrToDuration(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"", "abc"} {
		if _, err := StrToDuration(in); err == nil {
			t.Errorf("StrToDuration(%q): want error", in)
		}
	}
}
