package forwardproxy

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// FetchFunc defines the function signature for fetching the resource when cache misses.
type FetchFunc func(r *http.Request) (resp *http.Response, bodyBytes []byte, err error)

// CacheHandler implements caching logic for the forward proxy.
type CacheHandler struct {
	cacheDir    string
	cacheTTL    time.Duration
	fetchOrigin FetchFunc // Function to call on cache miss
}

// NewCacheHandler creates a new caching layer.
func NewCacheHandler(cacheDir string, cacheTTL time.Duration, fetcher FetchFunc) *CacheHandler {
	if cacheDir == "" {
		log.Println("WARN: Cache directory is empty, caching will be disabled.")
		// Return nil or a handler that always fetches? For now, allow but log.
		// Or return error: return nil, errors.New("cache directory cannot be empty")
	}
	if fetcher == nil {
		log.Fatal("Fetcher function cannot be nil for CacheHandler") // Or return error
	}
	// Ensure cache TTL is non-negative.
	if cacheTTL < 0 {
		log.Println("WARN: Negative cache TTL provided, setting to 0 (disabled).")
		cacheTTL = 0 // Effectively disable caching if TTL is negative
	}
	return &CacheHandler{
		cacheDir:    cacheDir,
		cacheTTL:    cacheTTL,
		fetchOrigin: fetcher,
	}
}

// ServeFromCacheOrFetch tries to serve from cache, otherwise calls the fetcher.
// Returns the http.Response, body bytes, a bool indicating cache hit, and error.
func (h *CacheHandler) ServeFromCacheOrFetch(r *http.Request) (*http.Response, []byte, bool, error) {
	// Check if caching is effectively disabled
	if h.cacheTTL <= 0 || h.cacheDir == "" {
		// log.Printf("DBG: Cache Check: Caching disabled (TTL=%s, Dir='%s')", h.cacheTTL, h.cacheDir) // Optional Debug
		resp, body, err := h.fetchOrigin(r)
		return resp, body, false, err
	}

	cacheKey := generateCacheKey(r.Method, r.URL)
	cachePath := filepath.Join(h.cacheDir, cacheKey)
	// log.Printf("DBG: Cache Check: URL=%s, Key=%s, Path=%s", r.URL.String(), cacheKey, cachePath) // Optional Debug

	// Try to serve from cache first
	resp, body, found, err := h.serveFromCacheFile(cachePath)
	if err != nil {
		// Log error reading cache but proceed to fetch
		log.Printf("WARN: Error reading cache file %s: %v. Attempting fetch.", cachePath, err)
	}
	if found {
		// log.Printf("DBG: Cache Check: Found in cache file %s", cachePath) // Optional Debug
		return resp, body, true, nil // Cache Hit!
	}
	// log.Printf("DBG: Cache Check: Not found or expired in cache file %s", cachePath) // Optional Debug

	// Cache Miss: Fetch from origin
	originResp, originBody, fetchErr := h.fetchOrigin(r)
	if fetchErr != nil {
		return nil, nil, false, fmt.Errorf("failed to fetch origin for %s: %w", r.URL.String(), fetchErr)
	}
	// We need to be careful with the originResp.Body.
	// If we cache, we consume it. If we don't cache, the caller needs it.

	// Cache successful responses (e.g., 2xx)
	if originResp.StatusCode >= 200 && originResp.StatusCode < 300 {
		// Save response headers and body to cache
		// For simplicity now, just cache the body. A better cache would store headers too.
		h.saveToCache(cachePath, originBody) // Save the fetched body
		// Since we cached, the original body is no longer needed by the caller in this path
		originResp.Body.Close()
	} else {
		log.Printf("Not caching response for %s due to status code: %d", r.URL.String(), originResp.StatusCode)
		// IMPORTANT: Do not close originResp.Body here, the caller (HandleHTTP) needs it.
	}

	// Return the response fetched from origin (body might be closed if cached, or open if not)
	return originResp, originBody, false, nil
}

// serveFromCacheFile tries to read response body from a cache file.
// Returns dummy response, body bytes, bool found, error.
// A real implementation would store/retrieve headers as well.
func (h *CacheHandler) serveFromCacheFile(path string) (*http.Response, []byte, bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			// log.Printf("DBG: serveFromCacheFile: File not found: %s", path) // Optional Debug
			return nil, nil, false, nil // Not found, not an error
		}
		log.Printf("WARN: serveFromCacheFile: Stat error for %s: %v", path, err) // Log as warning
		return nil, nil, false, err                                              // Other stat error
	}

	// Check TTL
	if time.Since(fi.ModTime()) > h.cacheTTL {
		log.Printf("Cache EXPIRED for %s (ModTime: %s, TTL: %s)", path, fi.ModTime(), h.cacheTTL)
		// Attempt removal (best effort)
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			log.Printf("WARN: Failed to remove expired cache file %s: %v", path, rmErr)
		}
		return nil, nil, false, nil // Expired, treat as not found
	}
	// log.Printf("DBG: serveFromCacheFile: Cache valid for %s", path) // Optional Debug

	// Read the file content (body)
	bodyBytes, err := os.ReadFile(path)
	if err != nil {
		// Log error but treat as cache miss
		log.Printf("WARN: Failed to read cache file %s: %v", path, err)
		// Attempt to remove potentially corrupt file
		_ = os.Remove(path)
		return nil, nil, false, nil // Treat as miss if read fails
	}

	// --- Construct a dummy response ---
	// Ideally, we'd load saved headers here. For now, create minimal headers.
	resp := &http.Response{
		StatusCode: http.StatusOK, // Assume OK for cached item
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(bodyBytes)), // Create a readable body
	}
	resp.Header.Set("Content-Length", strconv.Itoa(len(bodyBytes)))
	resp.Header.Set("Last-Modified", fi.ModTime().UTC().Format(http.TimeFormat))
	// Set Content-Type based on extension (of the original URL if stored, or cache key?)
	// This is limited without stored headers.
	ctype := mime.TypeByExtension(filepath.Ext(path)) // Guess from cache key extension (limited)
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	resp.Header.Set("Content-Type", ctype)

	return resp, bodyBytes, true, nil
}

// saveToCache saves the response body to the cache file.
func (h *CacheHandler) saveToCache(path string, data []byte) {
	dir := filepath.Dir(path)
	// Ensure cache directory exists
	if err := os.MkdirAll(dir, 0750); err != nil {
		log.Printf("ERROR: Failed to create cache directory %s: %v", dir, err)
		return
	}

	// Write the file
	// Use a temporary file and rename for atomicity? More robust but complex.
	// For now, direct write.
	if err := os.WriteFile(path, data, 0640); err != nil {
		log.Printf("ERROR: Failed to write cache file %s: %v", path, err)
		// Attempt to remove potentially corrupt file
		_ = os.Remove(path)
		return
	}
	log.Printf("Cache SAVED %d bytes to %s", len(data), path)
}

// generateCacheKey creates a filesystem-safe cache key from method and URL.
func generateCacheKey(method string, u *url.URL) string {
	// Normalize: Use scheme, host, path, sorted query params
	query := u.Query()
	sortedQuery := query.Encode() // Sorts keys automatically

	keyData := fmt.Sprintf("%s:%s://%s%s?%s",
		strings.ToUpper(method), // Ensure method is uppercase
		strings.ToLower(u.Scheme),
		strings.ToLower(u.Host),
		u.Path,
		sortedQuery,
	)

	// Hash the key data
	hasher := sha256.New()
	hasher.Write([]byte(keyData))
	hashBytes := hasher.Sum(nil)

	// Encode the hash to a filesystem-safe string (Base64 URL encoding)
	// Add a prefix/extension for easier identification if needed
	encoded := base64.URLEncoding.EncodeToString(hashBytes)

	// Optional: Create subdirectories based on first few chars of hash?
	// Improves performance with very large numbers of cache files.
	// Example: return filepath.Join(encoded[:2], encoded[2:]) + ".cache"
	return encoded + ".cache" // Simple flat structure for now
}
