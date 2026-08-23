package forwardproxy

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

// cacheEntry is the gob-encoded on-disk representation of a cached HTTP response.
// Storing status code and headers alongside the body lets cached responses be
// served with their original content type instead of a guessed fallback.
type cacheEntry struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	StoredAt   time.Time
}

// CacheHandler implements caching logic for the forward proxy.
type CacheHandler struct {
	cacheDir    string
	cacheTTL    time.Duration
	fetchOrigin FetchFunc // Function to call on cache miss
}

// NewCacheHandler creates a new caching layer.
// A negative cacheTTL is treated as zero, which disables serving from cache.
func NewCacheHandler(cacheDir string, cacheTTL time.Duration, fetcher FetchFunc) (*CacheHandler, error) {
	if fetcher == nil {
		return nil, errors.New("fetcher function cannot be nil")
	}
	if cacheDir == "" {
		return nil, errors.New("cache directory cannot be empty")
	}
	if cacheTTL < 0 {
		cacheTTL = 0
	}
	return &CacheHandler{
		cacheDir:    cacheDir,
		cacheTTL:    cacheTTL,
		fetchOrigin: fetcher,
	}, nil
}

// ServeFromCacheOrFetch tries to serve from cache, otherwise calls the fetcher.
// Returns the http.Response, body bytes, a bool indicating cache hit, and error.
func (h *CacheHandler) ServeFromCacheOrFetch(r *http.Request) (*http.Response, []byte, bool, error) {
	// Check if caching is effectively disabled
	if h.cacheTTL <= 0 || h.cacheDir == "" {
		resp, body, err := h.fetchOrigin(r)
		return resp, body, false, err
	}

	cacheKey := generateCacheKey(r.Method, r.URL)
	cachePath := filepath.Join(h.cacheDir, cacheKey)

	// Try to serve from cache first
	resp, body, found, err := h.serveFromCacheFile(cachePath)
	if err != nil {
		// Log error reading cache but proceed to fetch
		slog.Warn("error reading cache file; fetching origin", "path", cachePath, "err", err)
	}
	if found {
		return resp, body, true, nil // Cache Hit!
	}

	// Cache Miss: Fetch from origin
	originResp, originBody, fetchErr := h.fetchOrigin(r)
	if fetchErr != nil {
		return nil, nil, false, fmt.Errorf("failed to fetch origin for %s: %w", r.URL.String(), fetchErr)
	}
	// We need to be careful with the originResp.Body.
	// If we cache, we consume it. If we don't cache, the caller needs it.

	// Cache successful responses (e.g., 2xx)
	if originResp.StatusCode >= 200 && originResp.StatusCode < 300 {
		// Save status code, headers, and body so the cached response can be
		// reconstructed faithfully on a hit.
		h.saveToCache(cachePath, originResp, originBody)
		// Since we cached, the original body is no longer needed by the caller in this path
		originResp.Body.Close()
	} else {
		slog.Info("response not cached due to status code", "url", r.URL.String(), "status", originResp.StatusCode)
		// IMPORTANT: Do not close originResp.Body here, the caller (HandleHTTP) needs it.
	}

	// Return the response fetched from origin (body might be closed if cached, or open if not)
	return originResp, originBody, false, nil
}

// serveFromCacheFile tries to read a cached response from disk.
// Returns the reconstructed response, body bytes, bool found, error.
func (h *CacheHandler) serveFromCacheFile(path string) (*http.Response, []byte, bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, false, nil // Not found, not an error
		}
		slog.Warn("cache stat failed", "path", path, "err", err)
		return nil, nil, false, err // Other stat error
	}

	// Check TTL
	if time.Since(fi.ModTime()) > h.cacheTTL {
		slog.Debug("cache entry expired", "path", path, "ttl", h.cacheTTL)
		// Attempt removal (best effort)
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			slog.Warn("failed to remove expired cache file", "path", path, "err", rmErr)
		}
		return nil, nil, false, nil // Expired, treat as not found
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		// Log error but treat as cache miss
		slog.Warn("failed to read cache file; treating as miss", "path", path, "err", err)
		_ = os.Remove(path)
		return nil, nil, false, nil // Treat as miss if read fails
	}

	var entry cacheEntry
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&entry); err != nil {
		// Unreadable entry (e.g., leftover from an older cache format): treat as miss.
		slog.Warn("failed to decode cache file; discarding", "path", path, "err", err)
		_ = os.Remove(path)
		return nil, nil, false, nil
	}

	bodyBytes := entry.Body
	resp := &http.Response{
		StatusCode: entry.StatusCode,
		Header:     entry.Header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(bodyBytes)),
	}
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}
	resp.Header.Set("Content-Length", strconv.Itoa(len(bodyBytes)))

	return resp, bodyBytes, true, nil
}

// saveToCache atomically persists a response's status code, headers, and body.
// It writes to a temporary file in the same directory and renames it into place,
// so readers never observe a partially written entry.
func (h *CacheHandler) saveToCache(path string, resp *http.Response, data []byte) {
	dir := filepath.Dir(path)
	// Ensure cache directory exists
	if err := os.MkdirAll(dir, 0750); err != nil {
		slog.Error("failed to create cache directory", "dir", dir, "err", err)
		return
	}

	entry := cacheEntry{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       data,
		StoredAt:   time.Now().UTC(),
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(entry); err != nil {
		slog.Error("failed to encode cache entry", "path", path, "err", err)
		return
	}

	tmp, err := os.CreateTemp(dir, ".tmp-cache-*")
	if err != nil {
		slog.Error("failed to create temp cache file", "dir", dir, "err", err)
		return
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		slog.Error("failed to write cache file", "path", tmpPath, "err", err)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		slog.Error("failed to close cache file", "path", tmpPath, "err", err)
		return
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		slog.Error("failed to finalize cache file", "path", path, "err", err)
		return
	}
	slog.Debug("cache entry saved", "status", entry.StatusCode, "bytes", len(data), "path", path)
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
