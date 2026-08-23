package cachecleaner

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// StartCleaner begins the background cache cleaning process.
// It returns a function that can be called to stop the cleaner.
func StartCleaner(ctx context.Context, interval time.Duration, cacheDir string, cacheTTL time.Duration) (stopFunc func()) {
	if interval <= 0 || cacheDir == "" || cacheTTL <= 0 {
		slog.Info("cache cleaner not started: interval or TTL non-positive, or cache dir empty")
		return func() {} // Return no-op stop function
	}

	slog.Info("cache cleaner started", "interval", interval, "dir", cacheDir, "ttl", cacheTTL)
	ticker := time.NewTicker(interval)
	stopChan := make(chan struct{}) // Channel to signal stop

	// Run initial cleanup immediately? Optional.
	// go runCleanup(cacheDir, cacheTTL)

	go func() {
		for {
			select {
			case <-ticker.C:
				slog.Info("running cache cleanup")
				deletedCount, err := runCleanup(cacheDir, cacheTTL)
				if err != nil {
					slog.Error("cache cleanup failed", "err", err)
				} else {
					slog.Info("cache cleanup finished", "deleted", deletedCount)
				}
			case <-stopChan:
				slog.Info("stopping cache cleaner ticker")
				ticker.Stop()
				return
			case <-ctx.Done(): // Listen for global context cancellation
				slog.Info("stopping cache cleaner due to context cancellation")
				ticker.Stop()
				return
			}
		}
	}()

	// Return the function to stop the cleaner
	stopFunc = func() {
		close(stopChan)
	}
	return stopFunc
}

// runCleanup walks the cache directory and removes expired files.
// Returns the number of files deleted and any error encountered during the walk.
func runCleanup(cacheDir string, cacheTTL time.Duration) (int, error) {
	deletedCount := 0
	now := time.Now()
	minModTime := now.Add(-cacheTTL) // Files older than this will be deleted

	walkFunc := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Log error accessing path but continue walking if possible
			slog.Warn("cleanup walk cannot access path", "path", path, "err", err)
			return nil // Continue walking other parts
		}

		// Skip directories, only process files
		if d.IsDir() {
			// Don't delete the root cache directory itself
			if path == cacheDir {
				return nil
			}
			// Optional: Delete empty subdirectories? More complex. For now, skip.
			return nil
		}

		// Get file info for modification time
		info, err := d.Info() // Use DirEntry.Info() - more efficient
		if err != nil {
			slog.Warn("cleanup walk cannot stat file", "path", path, "err", err)
			return nil // Continue
		}

		// Check if file modification time is before the minimum allowed time
		if info.ModTime().Before(minModTime) {
			slog.Debug("deleting expired cache file", "path", path, "modTime", info.ModTime())
			err := os.Remove(path)
			if err != nil {
				slog.Warn("failed to delete expired cache file", "path", path, "err", err)
				// Log error but continue cleanup
			} else {
				deletedCount++
			}
		}
		return nil // Continue walking
	}

	err := filepath.WalkDir(cacheDir, walkFunc)
	if err != nil {
		// This error is from WalkDir itself, e.g., root dir doesn't exist
		return deletedCount, err
	}

	return deletedCount, nil
}
