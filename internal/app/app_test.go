package app

import (
	"testing"

	"github.com/mohammedhabas11/admin-bot/pkg/config"
)

func TestCompareConfigs(t *testing.T) {
	base := func() *config.Config {
		cfg := &config.Config{}
		cfg.HTTP.Enabled = true
		cfg.HTTP.Port = 8080
		cfg.HTTP.ForwardProxy.Enabled = true
		cfg.HTTP.ForwardProxy.Cache.Enabled = true
		cfg.HTTP.ForwardProxy.Cache.CacheDir = "/tmp/cache"
		cfg.HTTP.ForwardProxy.Cache.CacheTTL = "7d"
		cfg.ProxyCacheCleanup.Interval = "1h"
		return cfg
	}

	tests := map[string]struct {
		mutate      func(*config.Config)
		wantServer  bool
		wantCleaner bool
	}{
		"identical configs":       {mutate: func(*config.Config) {}},
		"http port change":        {mutate: func(c *config.Config) { c.HTTP.Port = 9090 }, wantServer: true},
		"static dir change":       {mutate: func(c *config.Config) { c.HTTP.Static.Enabled = true }, wantServer: true},
		"proxy domains change":    {mutate: func(c *config.Config) { c.HTTP.ForwardProxy.Domains = []string{"a.com"} }, wantServer: true},
		"cleanup interval change": {mutate: func(c *config.Config) { c.ProxyCacheCleanup.Interval = "30m" }, wantCleaner: true},
		"cache ttl change":        {mutate: func(c *config.Config) { c.HTTP.ForwardProxy.Cache.CacheTTL = "3d" }, wantCleaner: true},
		"cache dir change":        {mutate: func(c *config.Config) { c.HTTP.ForwardProxy.Cache.CacheDir = "/tmp/other" }, wantCleaner: true},
		"cache disabled": {mutate: func(c *config.Config) {
			c.HTTP.ForwardProxy.Cache.Enabled = false
			c.HTTP.ForwardProxy.Cache.CacheDir = ""
			c.ProxyCacheCleanup.Interval = ""
		}, wantCleaner: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			oldCfg := base()
			newCfg := base()
			tc.mutate(newCfg)

			gotServer, gotCleaner := CompareConfigs(oldCfg, newCfg)
			if gotServer != tc.wantServer || gotCleaner != tc.wantCleaner {
				t.Errorf("CompareConfigs = (%v, %v), want (%v, %v)",
					gotServer, gotCleaner, tc.wantServer, tc.wantCleaner)
			}
		})
	}
}

func TestCompareConfigsNilForcesRestart(t *testing.T) {
	if s, c := CompareConfigs(nil, &config.Config{}); !s || !c {
		t.Errorf("nil old: got (%v, %v), want (true, true)", s, c)
	}
	if s, c := CompareConfigs(&config.Config{}, nil); !s || !c {
		t.Errorf("nil new: got (%v, %v), want (true, true)", s, c)
	}
}

func TestCompareConfigsCacheDisableStopsCleanerOnly(t *testing.T) {
	oldCfg := &config.Config{}
	oldCfg.HTTP.Enabled = true
	oldCfg.HTTP.ForwardProxy.Enabled = true
	oldCfg.HTTP.ForwardProxy.Cache.Enabled = true
	oldCfg.HTTP.ForwardProxy.Cache.CacheDir = "/tmp/cache"

	// Proxy itself stays enabled; only its cache is switched off.
	newCfg := &config.Config{}
	newCfg.HTTP.Enabled = true
	newCfg.HTTP.ForwardProxy.Enabled = true

	gotServer, gotCleaner := CompareConfigs(oldCfg, newCfg)
	if gotServer {
		t.Error("server restart flagged although HTTP config unchanged")
	}
	if !gotCleaner {
		t.Error("cleaner stop not flagged after cache disable")
	}
}
