package forwardproxy

import (
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mohammedhabas11/admin-bot/pkg/config"
)

// ProxyHandler struct definition remains the same
type ProxyHandler struct {
	config config.ProxyConfig
	cache  *CacheHandler
}

// NewHandler function remains the same
func NewHandler(cfg config.ProxyConfig) *ProxyHandler {
	var cacheInstance *CacheHandler = nil
	if cfg.Cache.Enabled && cfg.Cache.CacheDir != "" {
		cacheTTL, err := cfg.Cache.GetCacheTTL()
		if err != nil {
			log.Printf("WARNING: Invalid proxy cache TTL ('%s'), disabling caching: %v", cfg.Cache.CacheTTL, err)
		} else if cacheTTL <= 0 {
			log.Printf("Proxy caching disabled due to TTL being zero or negative.")
		} else {
			fetchDelegate := func(r *http.Request) (*http.Response, []byte, error) {
				// Pass bodyBytes back from PerformFetch, needed by cache handler
				resp, body, err := PerformFetch(r)
				return resp, body, err
			}
			cacheInstance = NewCacheHandler(cfg.Cache.CacheDir, cacheTTL, fetchDelegate)
			log.Printf("Proxy caching enabled: Dir=%s, TTL=%s", cfg.Cache.CacheDir, cacheTTL)
		}
	} else {
		log.Println("Proxy caching is disabled (globally, or no cache dir specified).")
	}

	return &ProxyHandler{
		config: cfg,
		cache:  cacheInstance,
	}
}

// HandleConnect method remains the same
func (h *ProxyHandler) HandleConnect(w http.ResponseWriter, r *http.Request) {
	log.Printf(">>> HandleConnect: Entered for target %s", r.URL.Host)
	targetHost := r.URL.Host // CONNECT request URI is the target host:port
	if targetHost == "" {
		log.Printf("ERROR: HandleConnect: Bad Request: CONNECT requires host:port target (URI: %s)", r.RequestURI)
		http.Error(w, "Bad Request: CONNECT requires host:port target", http.StatusBadRequest)
		return
	}

	log.Printf("CONNECT request to %s", targetHost)

	destConn, err := net.DialTimeout("tcp", targetHost, 15*time.Second)
	if err != nil {
		log.Printf("ERROR: HandleConnect: Failed to dial target %s: %v", targetHost, err)
		http.Error(w, "Failed to connect to target server: "+err.Error(), http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		log.Println("ERROR: HandleConnect: Hijacking not supported by ResponseWriter")
		http.Error(w, "Internal Server Error: Hijacking not supported", http.StatusInternalServerError)
		destConn.Close()
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		log.Printf("ERROR: HandleConnect: Failed to hijack client connection: %v", err)
		clientConn.Close()
		destConn.Close()
		return
	}

	_, err = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	if err != nil {
		log.Printf("ERROR: HandleConnect: Failed to send 200 OK to client for %s: %v", targetHost, err)
		clientConn.Close()
		destConn.Close()
		return
	}

	log.Printf("Tunnel established for %s", targetHost)

	go transfer(destConn, clientConn, targetHost+" (server->client)")
	go transfer(clientConn, destConn, targetHost+" (client->server)")
}

// HandleHTTP handles standard HTTP GET, POST, etc. requests passed from the top-level handler.
func (h *ProxyHandler) HandleHTTP(w http.ResponseWriter, r *http.Request) {
	// log.Printf(">>> HandleHTTP: Entered for %s %s", r.Method, r.RequestURI) // Optional Debug

	// --- Check for self-request loop ---
	// Get the server's listening address (this requires access to config, maybe pass it?)
	// Or approximate by checking common loopback addresses.
	// A more robust way is needed if Addr can be different from 0.0.0.0 or ::
	serverHost := "localhost"                  // Approximation
	serverPort := config.GetConfig().HTTP.Port // Get configured port
	requestHostPort := r.Host                  // e.g., "localhost:8080" or "example.com"

	// Split host and port from request
	reqHost, reqPortStr, _ := net.SplitHostPort(requestHostPort)
	if reqHost == "" { // Handle cases where port is missing (e.g., Host: example.com)
		reqHost = requestHostPort
		// Assume default port 80 for comparison if needed, but host match is often enough
	}
	reqPort, _ := strconv.Atoi(reqPortStr)

	// Check if the request target appears to be the proxy itself
	isLoopback := net.ParseIP(reqHost) != nil && net.ParseIP(reqHost).IsLoopback()
	isSelfRequest := (reqHost == serverHost || isLoopback) && (reqPort == serverPort || reqPort == 0) // Port 0 means unspecified

	if isSelfRequest && !r.URL.IsAbs() { // Check if it's a relative request to self
		log.Printf("WARN: HandleHTTP: Detected potential self-request loop for %s %s. Returning 404.", r.Method, r.RequestURI)
		http.NotFound(w, r) // Return 404 instead of proxying
		return
	}
	// --- End self-request check ---

	// Reconstruct URL if necessary (for explicit proxy requests with relative paths)
	if !r.URL.IsAbs() { // Only reconstruct if it's not already absolute
		if r.Host == "" {
			log.Printf("ERROR: HandleHTTP: Bad Request: Missing host information (URI: %s)", r.RequestURI)
			http.Error(w, "Bad Request: Missing host information", http.StatusBadRequest)
			return
		}
		// Assume http scheme if not specified
		r.URL.Scheme = "http"
		r.URL.Host = r.Host
		// log.Printf("DBG: HandleHTTP: Reconstructed relative URL for request: %s", r.URL.String()) // Optional Debug
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
		log.Printf("ERROR: HandleHTTP: Response is nil after fetch/cache check for %s", r.URL.String())
		http.Error(w, "Internal Proxy Error: Failed to get response", http.StatusInternalServerError)
		return
	}
	defer response.Body.Close()

	copyHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)

	copiedBytes, err := io.Copy(w, response.Body)
	if err != nil {
		if !isConnectionClosed(err) {
			log.Printf("WARN: HandleHTTP: Error writing response body for %s after %d bytes: %v", r.URL.String(), copiedBytes, err)
		}
	}
}

// Helper functions (transfer, copyHeaders, isConnectionClosed, dumpRequest) remain the same
// transfer copies data between two connections and closes them when done.
func transfer(destination io.WriteCloser, source io.ReadCloser, direction string) {
	defer destination.Close()
	defer source.Close()
	// log.Printf("DBG: Starting transfer %s", direction) // Optional Debug
	_, err := io.Copy(destination, source)
	// log.Printf("DBG: Finished transfer %s (err: %v)", direction, err) // Optional Debug
	if err != nil {
		if !isConnectionClosed(err) { // Use helper to avoid logging expected closure errors
			log.Printf("WARN: Error during transfer %s: %v", direction, err)
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

// // dumpRequest is a helper for debugging (optional)
// func dumpRequest(r *http.Request) {
// 	dump, err := httputil.DumpRequest(r, true) // Dump body as well
// 	if err != nil {
// 		log.Printf("Error dumping request: %v", err)
// 		return
// 	}
// 	log.Printf("Request Dump:\n%s\n--------------------\n", string(dump))
// }
