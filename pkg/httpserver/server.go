package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
		slog.Info("static file serving disabled")
	}

	// Initialize Proxy Handler if enabled (needed for both CONNECT and HTTP fallback)
	if cfg.HTTP.ForwardProxy.Enabled {
		slog.Info("forward proxy enabled")
		specificProxyHandler = forwardproxy.NewHandler(cfg.HTTP.ForwardProxy, cfg.HTTP.Port)

		// Register the proxy's HTTP handler as the fallback for the mux
		requestMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// This function is called only if no /static/ route matched
			slog.Debug("mux fallback routing to proxy handler", "path", r.URL.Path)
			specificProxyHandler.HandleHTTP(w, r)
		})

	} else {
		slog.Info("forward proxy disabled")
		// Requests not matching a static route get the default 404.
		requestMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			slog.Debug("no handler for path", "path", r.URL.Path)
			http.NotFound(w, r)
		})
	}

	// --- Top-Level Handler ---
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Handle CONNECT directly if proxy is enabled
		if cfg.HTTP.ForwardProxy.Enabled && r.Method == http.MethodConnect {
			if specificProxyHandler != nil {
				specificProxyHandler.HandleConnect(w, r)
			} else {
				slog.Error("proxy enabled but handler is nil for CONNECT", "uri", r.RequestURI)
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
		slog.Info("http server listening", "addr", addr)
		if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("listen and serve failed", "err", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutdown signal received by http server")
	return s.Stop()
}

// Stop gracefully stops the HTTP server.
func (s *Server) Stop() error {
	if s.server == nil {
		slog.Info("server stop called but server was not running")
		return nil
	}

	serverAddr := s.server.Addr // Capture address before server becomes nil
	slog.Info("stopping server gracefully", "addr", serverAddr)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	err := s.server.Shutdown(shutdownCtx)
	s.server = nil
	if err != nil {
		return fmt.Errorf("server shutdown failed for %s: %w", serverAddr, err)
	}

	slog.Info("server stopped gracefully", "addr", serverAddr)
	return nil
}
