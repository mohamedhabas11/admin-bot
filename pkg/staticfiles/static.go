// pkg/staticfiles/static.go
package staticfiles

import (
	"log"
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
		log.Printf("STATIC REQ: [%s] %s %s (Route: %s)", r.Method, r.URL.Path, r.RemoteAddr, routePrefix)
		// Consider using a ResponseWriter wrapper to capture status code later
		h.ServeHTTP(w, r) // Call the original handler (StripPrefix -> FileServer)
		log.Printf("STATIC RSP: [%s] %s completed in %v", r.Method, r.URL.Path, time.Since(start))
	})
}

// RegisterStaticRoutes sets up handlers for serving static files based on config.
func RegisterStaticRoutes(mux *http.ServeMux, cfg config.StaticConfig) {
	if !cfg.Enabled {
		return
	}

	log.Println("Registering static file routes...")
	if len(cfg.Dirs) == 0 {
		log.Println("  No static directories configured.")
		return
	}

	for key, dirCfg := range cfg.Dirs {
		routeKey := strings.Trim(key, "/")
		if routeKey == "" {
			log.Printf("  Skipping static route: Invalid key.")
			continue
		}
		if dirCfg.Path == "" {
			log.Printf("  Skipping static route '/static/%s/': Filesystem path is empty.", routeKey)
			continue
		}

		urlPathPrefix := path.Join(StaticBaseUrlPath, routeKey) + "/"

		fsHandler := http.FileServer(http.Dir(dirCfg.Path))
		strippedHandler := http.StripPrefix(urlPathPrefix, fsHandler)

		// Wrap the stripped handler with logging
		loggedHandler := loggingMiddleware(strippedHandler, urlPathPrefix)

		mux.Handle(urlPathPrefix, loggedHandler) // Register the logged handler

		log.Printf("  Route '%s' -> Serves files from '%s'", urlPathPrefix, dirCfg.Path)
	}
}
