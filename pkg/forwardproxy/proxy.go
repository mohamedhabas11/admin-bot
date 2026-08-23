package forwardproxy

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mohammedhabas11/admin-bot/pkg/config"
)

// ProxyHandler serves proxied requests for one HTTP listener.
type ProxyHandler struct {
	config     config.ProxyConfig
	listenPort int // Port this proxy's own server listens on
	cache      *CacheHandler
}

// NewHandler creates a proxy handler for the given configuration.
// listenPort is the port of the HTTP server embedding the proxy and is used to
// detect self-referential requests without reaching into global config state.
func NewHandler(cfg config.ProxyConfig, listenPort int) *ProxyHandler {
	var cacheInstance *CacheHandler = nil
	if cfg.Cache.Enabled && cfg.Cache.CacheDir != "" {
		cacheTTL, err := cfg.Cache.GetCacheTTL()
		switch {
		case err != nil:
			slog.Warn("invalid proxy cache TTL; disabling caching", "ttl", cfg.Cache.CacheTTL, "err", err)
		case cacheTTL <= 0:
			slog.Info("proxy caching disabled: TTL zero or negative")
		default:
			fetchDelegate := func(r *http.Request) (*http.Response, []byte, error) {
				// Pass bodyBytes back from PerformFetch, needed by cache handler
				return PerformFetch(r)
			}
			cache, err := NewCacheHandler(cfg.Cache.CacheDir, cacheTTL, fetchDelegate)
			if err != nil {
				slog.Warn("failed to initialize proxy cache; disabling caching", "err", err)
			} else {
				cacheInstance = cache
				slog.Info("proxy caching enabled", "dir", cfg.Cache.CacheDir, "ttl", cacheTTL)
			}
		}
	} else {
		slog.Info("proxy caching disabled: globally off or no cache dir")
	}

	return &ProxyHandler{
		config:     cfg,
		listenPort: listenPort,
		cache:      cacheInstance,
	}
}

// HandleConnect method remains the same
func (h *ProxyHandler) HandleConnect(w http.ResponseWriter, r *http.Request) {
	slog.Debug("CONNECT request received", "target", r.URL.Host)
	targetHost := r.URL.Host // CONNECT request URI is the target host:port
	if targetHost == "" {
		slog.Error("CONNECT requires host:port target", "uri", r.RequestURI)
		http.Error(w, "Bad Request: CONNECT requires host:port target", http.StatusBadRequest)
		return
	}

	slog.Debug("dialing CONNECT target", "target", targetHost)

	destConn, err := net.DialTimeout("tcp", targetHost, 15*time.Second)
	if err != nil {
		slog.Error("failed to dial CONNECT target", "target", targetHost, "err", err)
		http.Error(w, "Failed to connect to target server: "+err.Error(), http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		slog.Error("hijacking not supported by ResponseWriter")
		http.Error(w, "Internal Server Error: Hijacking not supported", http.StatusInternalServerError)
		destConn.Close()
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		slog.Error("failed to hijack client connection", "err", err)
		clientConn.Close()
		destConn.Close()
		return
	}

	_, err = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	if err != nil {
		slog.Error("failed to send connection established to client", "target", targetHost, "err", err)
		clientConn.Close()
		destConn.Close()
		return
	}

	slog.Info("tunnel established", "target", targetHost)

	go transfer(destConn, clientConn, targetHost+" (server->client)")
	go transfer(clientConn, destConn, targetHost+" (client->server)")
}

// HandleHTTP handles standard HTTP GET, POST, etc. requests passed from the top-level handler.
func (h *ProxyHandler) HandleHTTP(w http.ResponseWriter, r *http.Request) {
	// Check for self-request loops (a client asking this proxy to proxy itself).
	if h.isSelfRequest(r.Host) && !r.URL.IsAbs() {
		slog.Warn("possible self-request loop; returning 404", "method", r.Method, "uri", r.RequestURI)
		http.NotFound(w, r) // Return 404 instead of proxying
		return
	}

	// Reconstruct URL if necessary (for explicit proxy requests with relative paths)
	if !r.URL.IsAbs() { // Only reconstruct if it's not already absolute
		if r.Host == "" {
			slog.Error("request missing host information", "uri", r.RequestURI)
			http.Error(w, "Bad Request: Missing host information", http.StatusBadRequest)
			return
		}
		// Assume http scheme if not specified
		r.URL.Scheme = "http"
		r.URL.Host = r.Host
	}

	// Check if caching is enabled and applicable for this domain
	shouldCache := h.cache != nil && h.config.ShouldCacheDomain(r.URL.Host)

	var response *http.Response
	var err error
	var cacheHit bool

	if shouldCache {
		// Assign bodyBytes to the blank identifier '_' to ignore it
		response, _, cacheHit, err = h.cache.ServeFromCacheOrFetch(r) // <-- Use _
		if err != nil {
			http.Error(w, "Proxy Error: "+err.Error(), http.StatusBadGateway)
			return
		}
		if cacheHit {
			w.Header().Set("X-Cache-Status", "HIT")
		} else {
			w.Header().Set("X-Cache-Status", "MISS")
		}
	} else {
		w.Header().Set("X-Cache-Status", "BYPASS")
		// Assign bodyBytes to the blank identifier '_' to ignore it
		response, _, err = PerformFetch(r) // <-- Use _
		if err != nil {
			http.Error(w, "Proxy Error: "+err.Error(), http.StatusBadGateway)
			return
		}
	}

	if response == nil {
		slog.Error("nil response after fetch/cache check", "url", r.URL.String())
		http.Error(w, "Internal Proxy Error: Failed to get response", http.StatusInternalServerError)
		return
	}
	defer response.Body.Close()

	copyHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)

	copiedBytes, err := io.Copy(w, response.Body)
	if err != nil {
		if !isConnectionClosed(err) {
			slog.Warn("error writing response body", "url", r.URL.String(), "bytes", copiedBytes, "err", err)
		}
	}
}

// isSelfRequest reports whether a request's Host header targets this proxy
// itself (localhost or any loopback address on the listener's port). A missing
// port is treated as potentially self-targeted to fail safe.
func (h *ProxyHandler) isSelfRequest(requestHostPort string) bool {
	reqHost, reqPortStr, _ := net.SplitHostPort(requestHostPort)
	if reqHost == "" { // No port in Host header (e.g., "example.com")
		reqHost = requestHostPort
	}
	reqHost = strings.ToLower(reqHost)
	reqPort, _ := strconv.Atoi(reqPortStr)

	isLoopback := net.ParseIP(reqHost) != nil && net.ParseIP(reqHost).IsLoopback()
	return (reqHost == "localhost" || isLoopback) && (reqPort == h.listenPort || reqPort == 0)
}

// Helper functions (transfer, copyHeaders, isConnectionClosed) remain the same
// transfer copies data between two connections and closes them when done.
func transfer(destination io.WriteCloser, source io.ReadCloser, direction string) {
	defer destination.Close()
	defer source.Close()
	_, err := io.Copy(destination, source)
	if err != nil {
		if !isConnectionClosed(err) { // Use helper to avoid logging expected closure errors
			slog.Warn("error during tunnel transfer", "direction", direction, "err", err)
		}
	}
}

// copyHeaders copies headers from source to destination, filtering hop-by-hop headers.
func copyHeaders(dst, src http.Header) {
	hopByHopHeaders := map[string]struct{}{
		"Connection":          {},
		"Keep-Alive":          {},
		"Proxy-Authenticate":  {},
		"Proxy-Authorization": {},
		"Te":                  {}, // canonicalized version
		"Trailers":            {},
		"Transfer-Encoding":   {},
		"Upgrade":             {},
	}

	for k, vv := range src {
		// Use CanonicalHeaderKey to match case-insensitively
		if _, ok := hopByHopHeaders[http.CanonicalHeaderKey(k)]; ok {
			continue
		}
		// Copy other headers
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// isConnectionClosed checks for common network errors indicating expected closure.
func isConnectionClosed(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return false // Timeout is not a closed connection
	}
	if errors.Is(err, net.ErrClosed) { // More explicit check
		return true
	}
	errStr := err.Error()
	if strings.Contains(errStr, "use of closed network connection") {
		return true
	}
	if strings.Contains(errStr, "connection reset by peer") {
		return true
	}
	return false
}
