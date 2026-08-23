// pkg/staticfiles/static.go
package staticfiles

import (
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/mohammedhabas11/admin-bot/pkg/config"
)

// StaticBaseUrlPath is the root path under which all static directories are served.
const StaticBaseUrlPath = "/static/"

// Simple logging middleware
func loggingMiddleware(h http.Handler, routePrefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		slog.Debug("static request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr, "route", routePrefix)
		// Consider using a ResponseWriter wrapper to capture status code later
		h.ServeHTTP(w, r) // Call the original handler (StripPrefix -> FileServer)
		slog.Debug("static request complete", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

// RegisterStaticRoutes sets up handlers for serving static files based on config.
func RegisterStaticRoutes(mux *http.ServeMux, cfg config.StaticConfig) {
	if !cfg.Enabled {
		return
	}

	slog.Info("registering static file routes")
	if len(cfg.Dirs) == 0 {
		slog.Info("no static directories configured")
		return
	}

	for key, dirCfg := range cfg.Dirs {
		routeKey := strings.Trim(key, "/")
		if routeKey == "" {
			slog.Warn("skipping static route: invalid route key")
			continue
		}
		if dirCfg.Path == "" {
			slog.Warn("skipping static route: filesystem path empty", "route", "/static/"+routeKey+"/")
			continue
		}

		urlPathPrefix := path.Join(StaticBaseUrlPath, routeKey) + "/"

		fsHandler := http.FileServer(http.Dir(dirCfg.Path))
		strippedHandler := http.StripPrefix(urlPathPrefix, fsHandler)

		// Wrap the stripped handler with logging
		loggedHandler := loggingMiddleware(strippedHandler, urlPathPrefix)

		mux.Handle(urlPathPrefix, loggedHandler) // Register the logged handler

		slog.Info("static route registered", "route", urlPathPrefix, "dir", dirCfg.Path)
	}
}
