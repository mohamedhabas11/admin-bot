package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mohammedhabas11/admin-bot/pkg/config"
)

func TestHealthz(t *testing.T) {
	cfg := &config.Config{}
	s := NewServer(cfg, "vtest-1.2.3")

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.createRootHandler(cfg).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding body %q: %v", rec.Body.String(), err)
	}
	if body["status"] != "ok" {
		t.Errorf("status field = %q, want ok", body["status"])
	}
	if body["version"] != "vtest-1.2.3" {
		t.Errorf("version field = %q, want vtest-1.2.3", body["version"])
	}

	// Empty version falls back to "dev".
	s2 := NewServer(cfg, "")
	rec2 := httptest.NewRecorder()
	s2.createRootHandler(cfg).ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	_ = json.Unmarshal(rec2.Body.Bytes(), &body)
	if body["version"] != "dev" {
		t.Errorf("default version = %q, want dev", body["version"])
	}
}

// handlerFor builds a Server around cfg and returns its root handler.
func handlerFor(t *testing.T, cfg *config.Config) http.Handler {
	t.Helper()
	return NewServer(cfg, "test").createRootHandler(cfg)
}

func TestRootHandlerRouting(t *testing.T) {
	staticRoot := t.TempDir()
	sub := filepath.Join(staticRoot, "files")
	if err := os.MkdirAll(sub, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "hello.txt"), []byte("hi"), 0640); err != nil {
		t.Fatal(err)
	}

	staticOnly := &config.Config{}
	staticOnly.HTTP.Port = 8080
	staticOnly.HTTP.Static.Enabled = true
	staticOnly.HTTP.Static.Dirs = map[string]config.StaticDirConfig{
		"files": {Path: sub},
	}

	t.Run("static route serves file", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handlerFor(t, staticOnly).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/files/hello.txt", nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "hi" {
			t.Fatalf("got %d %q, want 200 %q", rec.Code, rec.Body.String(), "hi")
		}
	})

	t.Run("unknown path is 404 when proxy disabled", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handlerFor(t, staticOnly).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("got %d, want 404", rec.Code)
		}
	})

	t.Run("healthz wins over proxy fallback", func(t *testing.T) {
		proxied := &config.Config{}
		proxied.HTTP.Port = 8080
		proxied.HTTP.ForwardProxy.Enabled = true

		rec := httptest.NewRecorder()
		handlerFor(t, proxied).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("healthz through proxy-enabled server got %d, want 200", rec.Code)
		}
	})

	t.Run("self-targeted request is not proxied", func(t *testing.T) {
		proxied := &config.Config{}
		proxied.HTTP.Port = 8080
		proxied.HTTP.ForwardProxy.Enabled = true

		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/loop", nil)
		rec := httptest.NewRecorder()
		handlerFor(t, proxied).ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("self-request got %d, want 404 (loop guard)", rec.Code)
		}
	})
}
