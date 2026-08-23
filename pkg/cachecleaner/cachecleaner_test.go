package cachecleaner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunCleanup(t *testing.T) {
	dir := t.TempDir()
	ttl := time.Hour
	cutoff := time.Now().Add(-ttl)

	mkFile := func(name string, modTime time.Time) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, modTime, modTime); err != nil {
			t.Fatal(err)
		}
		return p
	}

	expired1 := mkFile("expired-a.cache", cutoff.Add(-2*time.Hour))
	expired2 := mkFile("expired-b.cache", cutoff.Add(-time.Minute))
	fresh := mkFile("fresh.cache", time.Now())
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0750); err != nil {
		t.Fatal(err)
	}
	expiredNested := filepath.Join(nested, "expired-nested.cache")
	if err := os.WriteFile(expiredNested, []byte("x"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(expiredNested, cutoff.Add(-time.Hour), cutoff.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	deleted, err := runCleanup(dir, ttl)
	if err != nil {
		t.Fatalf("runCleanup: %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3 (two flat + one nested)", deleted)
	}
	for _, gone := range []string{expired1, expired2, expiredNested} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s still exists after cleanup", gone)
		}
	}
	for _, kept := range []string{fresh, dir} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s should have survived cleanup: %v", kept, err)
		}
	}
}

func TestStartCleanerNoopForInvalidParams(t *testing.T) {
	cases := map[string]struct {
		interval time.Duration
		dir      string
		ttl      time.Duration
	}{
		"zero interval": {interval: 0, dir: t.TempDir(), ttl: time.Hour},
		"negative ttl":  {interval: time.Minute, dir: t.TempDir(), ttl: -time.Second},
		"empty dir":     {interval: time.Minute, dir: "", ttl: time.Hour},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			stop := StartCleaner(context.Background(), tc.interval, tc.dir, tc.ttl)
			if stop == nil {
				t.Fatal("StartCleaner returned nil stop func")
			}
			stop() // must be safe to call immediately and not panic
		})
	}
}

func TestStartCleanerRunsAndStops(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "stale.cache")
	if err := os.WriteFile(stale, []byte("x"), 0640); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatal(err)
	}

	stop := StartCleaner(context.Background(), 10*time.Millisecond, dir, time.Minute)
	defer stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(stale); os.IsNotExist(err) {
			return // worker swept it
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("cleaner did not delete stale entry within deadline")
}
