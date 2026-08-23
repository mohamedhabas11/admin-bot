package forwardproxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestCacheHandler builds a CacheHandler backed by a temp directory and a
// stub fetcher, returning the handler and the cache path for a given URL.
func newTestCacheHandler(t *testing.T, ttl time.Duration, resp *http.Response, body []byte) (*CacheHandler, string) {
	t.Helper()

	cacheDir := t.TempDir()
	fetcher := func(r *http.Request) (*http.Response, []byte, error) {
		return resp, body, nil
	}
	h, err := NewCacheHandler(cacheDir, ttl, fetcher)
	if err != nil {
		t.Fatalf("NewCacheHandler: %v", err)
	}

	u := &url.URL{Scheme: "http", Host: "example.com", Path: "/asset"}
	return h, filepath.Join(cacheDir, generateCacheKey(http.MethodGet, u))
}

func TestCachePreservesResponseMetadata(t *testing.T) {
	body := []byte("cached payload")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}, "X-Custom": []string{"kept"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	h, cachePath := newTestCacheHandler(t, time.Hour, resp, body)

	req := mustRequest(t, "http://example.com/asset")

	gotResp, gotBody, hit, err := h.ServeFromCacheOrFetch(req)
	if err != nil || hit {
		t.Fatalf("first call: want miss, got hit=%v err=%v", hit, err)
	}
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache file not written after first fetch: %v", err)
	}

	gotResp, gotBody, hit, err = h.ServeFromCacheOrFetch(req)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !hit {
		t.Fatal("second call: want cache hit")
	}
	if gotResp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", gotResp.StatusCode)
	}
	if ct := gotResp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want original value", ct)
	}
	if gotResp.Header.Get("X-Custom") != "kept" {
		t.Error("custom header not preserved on cache hit")
	}
	if !bytes.Equal(gotBody, body) {
		t.Errorf("body = %q, want %q", gotBody, body)
	}
}

func TestCacheCorruptFileIsTreatedAsMiss(t *testing.T) {
	body := []byte("fresh payload")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	h, cachePath := newTestCacheHandler(t, time.Hour, resp, body)

	if err := os.WriteFile(cachePath, []byte("not-a-gob-entry"), 0640); err != nil {
		t.Fatalf("seeding corrupt cache file: %v", err)
	}

	_, _, hit, err := h.ServeFromCacheOrFetch(mustRequest(t, "http://example.com/asset"))
	if err != nil {
		t.Fatalf("ServeFromCacheOrFetch: %v", err)
	}
	if hit {
		t.Fatal("corrupt cache file served as hit")
	}

	// Recovery: the corrupt entry was discarded, the refetched response was
	// re-cached, so the next request must be a hit again.
	if _, _, hit, err := h.ServeFromCacheOrFetch(mustRequest(t, "http://example.com/asset")); err != nil || !hit {
		t.Fatalf("follow-up after recovery: want hit, got hit=%v err=%v", hit, err)
	}
}

func TestCacheExpiresByTTL(t *testing.T) {
	body := []byte("payload")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	h, cachePath := newTestCacheHandler(t, time.Hour, resp, body)

	req := mustRequest(t, "http://example.com/asset")
	if _, _, hit, err := h.ServeFromCacheOrFetch(req); err != nil || hit {
		t.Fatalf("initial fill: hit=%v err=%v", hit, err)
	}

	stale := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(cachePath, stale, stale); err != nil {
		t.Fatalf("backdating cache file: %v", err)
	}

	_, _, hit, err := h.ServeFromCacheOrFetch(req)
	if err != nil {
		t.Fatalf("post-expiry fetch: %v", err)
	}
	if hit {
		t.Fatal("expired entry served as hit")
	}
}

func TestNewCacheHandlerRejectsInvalidInput(t *testing.T) {
	fetcher := func(r *http.Request) (*http.Response, []byte, error) { return nil, nil, nil }

	if _, err := NewCacheHandler("", time.Hour, fetcher); err == nil {
		t.Error("empty cache dir: want error")
	}
	if _, err := NewCacheHandler("/tmp/cache", time.Hour, nil); err == nil {
		t.Error("nil fetcher: want error")
	}

	h, err := NewCacheHandler("/tmp/cache", -time.Minute, fetcher)
	if err != nil {
		t.Fatalf("negative TTL should be clamped, got error: %v", err)
	}
	if h.cacheTTL != 0 {
		t.Errorf("negative TTL not clamped to 0: %v", h.cacheTTL)
	}
}

func TestPerformFetchRoundTripThroughSharedClient(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	req := mustRequest(t, srv.URL)
	resp, body, err := PerformFetch(req)
	if err != nil {
		t.Fatalf("PerformFetch: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
	if !bytes.Equal(gotBodyBytes(resp), body) {
		t.Error("response body reader out of sync with returned bytes")
	}

	// Second fetch must go through the same pooled client.
	if _, _, err := PerformFetch(mustRequest(t, srv.URL)); err != nil {
		t.Fatalf("second PerformFetch: %v", err)
	}
	if hits != 2 {
		t.Errorf("origin hit count = %d, want 2", hits)
	}
	if proxyHTTPClient == nil || proxyHTTPClient.Transport == nil {
		t.Error("shared client or transport not initialized")
	}
}

func gotBodyBytes(resp *http.Response) []byte {
	b, _ := io.ReadAll(resp.Body)
	return b
}

func TestIsSelfRequestDetection(t *testing.T) {
	h := &ProxyHandler{listenPort: 8080}

	self := []string{
		"localhost:8080",
		"127.0.0.1:8080",
		"[::1]:8080",
		"LOCALHOST:8080",
	}
	for _, host := range self {
		if !h.isSelfRequest(host) {
			t.Errorf("isSelfRequest(%q) = false, want true", host)
		}
	}

	external := []string{
		"example.com",      // No port, not loopback
		"example.com:8080", // Right port, wrong host
		"localhost:9090",   // Loopback, wrong port
		"github.com:8080",
	}
	for _, host := range external {
		if h.isSelfRequest(host) {
			t.Errorf("isSelfRequest(%q) = true, want false", host)
		}
	}
}

func TestGenerateCacheKeyNormalizesInput(t *testing.T) {
	u1 := &url.URL{Scheme: "HTTP", Host: "Example.com", Path: "/a", RawQuery: "b=2&a=1"}
	u2 := &url.URL{Scheme: "http", Host: "example.com", Path: "/a", RawQuery: "a=1&b=2"}

	k1 := generateCacheKey("get", u1)
	k2 := generateCacheKey("GET", u2)
	if k1 != k2 {
		t.Errorf("keys differ across case/query normalization:\n%s\n%s", k1, k2)
	}
}

func mustRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	return r
}
