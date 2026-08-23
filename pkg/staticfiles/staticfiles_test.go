package staticfiles

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mohammedhabas11/admin-bot/pkg/config"
)

func TestRegisterStaticRoutes(t *testing.T) {
	root := t.TempDir()
	pub := filepath.Join(root, "pub")
	if err := os.MkdirAll(pub, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pub, "hello.txt"), []byte("hello"), 0640); err != nil {
		t.Fatal(err)
	}

	cfg := config.StaticConfig{
		Enabled: true,
		Dirs: map[string]config.StaticDirConfig{
			"assets":  {Path: pub},
			"empty":   {Path: ""},  // skipped: no filesystem path
			"/weird/": {Path: pub}, // key normalized via Trim("/")
		},
	}

	mux := http.NewServeMux()
	RegisterStaticRoutes(mux, cfg)

	t.Run("serves files under route prefix", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/assets/hello.txt", nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
			t.Fatalf("got %d %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("traversal outside root is blocked", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/assets/..%2f..%2fetc%2fpasswd", nil))
		if rec.Code == http.StatusOK {
			t.Fatalf("traversal served with status %d", rec.Code)
		}
	})

	t.Run("normalized key serves same tree", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/weird/hello.txt", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d for trimmed-key route", rec.Code)
		}
	})

	t.Run("unregistered empty-path key is 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/empty/hello.txt", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("got %d, want 404", rec.Code)
		}
	})
}

func TestRegisterStaticRoutesDisabledOrEmptyIsNoop(t *testing.T) {
	mux := http.NewServeMux()

	disabled := config.StaticConfig{Enabled: false}
	RegisterStaticRoutes(mux, disabled)

	enabledButEmpty := config.StaticConfig{Enabled: true}
	RegisterStaticRoutes(mux, enabledButEmpty)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/assets/x", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("no-op registration should leave mux 404ing, got %d", rec.Code)
	}
}
