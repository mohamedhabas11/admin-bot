package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"

	"time"

	"github.com/mohammedhabas11/admin-bot/pkg/config"
	"github.com/mohammedhabas11/admin-bot/pkg/forwardproxy"
	"github.com/mohammedhabas11/admin-bot/pkg/staticfiles"
)

type Server struct {
	initialConfig *config.Config
	server        *http.Server
}

// NewServer creates a new Server instance but doesn't start it yet.
func NewServer(cfg *config.Config) *Server {
	return &Server{
		initialConfig: cfg,
	}
}

// createRootHandler builds the main handler.
// It intercepts CONNECT requests for the proxy.
// All other requests are passed to a ServeMux which handles static files
// and then falls back to the proxy's HTTP handler if enabled.
func (s *Server) createRootHandler(cfg *config.Config) http.Handler {
	// --- Create Handlers ---
	requestMux := http.NewServeMux() // Mux for non-CONNECT requests
	var specificProxyHandler *forwardproxy.ProxyHandler

	// Register Static File Routes if enabled
	if cfg.HTTP.Static.Enabled {
		staticfiles.RegisterStaticRoutes(requestMux, cfg.HTTP.Static) // Register on requestMux
	} else {
		log.Println("Static file serving is disabled.")
	}

	// Initialize Proxy Handler if enabled (needed for both CONNECT and HTTP fallback)
	if cfg.HTTP.ForwardProxy.Enabled {
		log.Println("Forward proxy is enabled.")
		specificProxyHandler = forwardproxy.NewHandler(cfg.HTTP.ForwardProxy)

		// Register the proxy's HTTP handler as the fallback for the mux
		requestMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// This function is called only if no /static/ route matched
			log.Printf("DBG: Mux fallback: Routing to proxy handler for %s", r.URL.Path)
			specificProxyHandler.HandleHTTP(w, r)
		})

	} else {
		log.Println("Forward proxy is disabled.")
		// Add a default 404 handler to requestMux ONLY if proxy is also disabled
		// This handles requests that don't match /static/
		if !cfg.HTTP.Static.Enabled || len(cfg.HTTP.Static.Dirs) == 0 {
			requestMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				log.Printf("No handler configured for path: %s", r.URL.Path)
				http.NotFound(w, r)
			})
		} else {
			// If static is enabled but proxy is disabled, requests not matching /static/
			// should also result in 404. ServeMux handles this by default if "/" isn't explicitly handled.
			// We could add an explicit 404 handler for "/" here too if desired.
			requestMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				log.Printf("Path %s not found (no static match, proxy disabled)", r.URL.Path)
				http.NotFound(w, r)
			})
		}
	}

	// --- Top-Level Handler ---
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Handle CONNECT directly if proxy is enabled
		if cfg.HTTP.ForwardProxy.Enabled && r.Method == http.MethodConnect {
			if specificProxyHandler != nil {
				specificProxyHandler.HandleConnect(w, r)
			} else {
				log.Printf("ERROR: Proxy enabled but handler is nil for CONNECT %s", r.RequestURI)
				http.Error(w, "Proxy configuration error", http.StatusInternalServerError)
			}
			return // CONNECT handled
		}

		// 2. For all other methods, delegate to the requestMux
		requestMux.ServeHTTP(w, r)
	})
}

// Start runs the HTTP server. It takes a context for graceful shutdown.
func (s *Server) Start(ctx context.Context) error {
	cfg := s.initialConfig

	if !cfg.HTTP.Enabled {
		return fmt.Errorf("HTTP server is disabled")
	}

	rootHandler := s.createRootHandler(cfg)

	addr := fmt.Sprintf("%s:%d", cfg.HTTP.Addr, cfg.HTTP.Port)
	s.server = &http.Server{
		Addr:         addr,
		Handler:      rootHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("HTTP server listening on %s", addr)
		if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("ERROR: ListenAndServe failed: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutdown signal received by HTTP server...")
	return s.Stop()
}

// Stop gracefully stops the HTTP server.
func (s *Server) Stop() error {
	if s.server == nil {
		log.Println("Server Stop() called but server was not running or already stopped.")
		return nil
	}

	serverAddr := s.server.Addr // Capture address before server becomes nil
	log.Printf("Attempting to stop server on %s gracefully...", serverAddr)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := s.server.Shutdown(shutdownCtx)
	s.server = nil
	if err != nil {
		return fmt.Errorf("server shutdown failed for %s: %w", serverAddr, err)
	}

	log.Printf("Server on %s stopped gracefully.", serverAddr)
	return nil
}
