package cachecleaner

import (
	"context"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"
)

// StartCleaner begins the background cache cleaning process.
// It returns a function that can be called to stop the cleaner.
func StartCleaner(ctx context.Context, interval time.Duration, cacheDir string, cacheTTL time.Duration) (stopFunc func()) {
	if interval <= 0 || cacheDir == "" || cacheTTL <= 0 {
		log.Println("Cache cleaner not started: interval or TTL is zero/negative, or cacheDir is empty.")
		return func() {} // Return no-op stop function
	}

	log.Printf("Starting cache cleaner: Interval=%v, Dir=%s, TTL=%v", interval, cacheDir, cacheTTL)
	ticker := time.NewTicker(interval)
	stopChan := make(chan struct{}) // Channel to signal stop

	// Run initial cleanup immediately? Optional.
	// go runCleanup(cacheDir, cacheTTL)

	go func() {
		for {
			select {
			case <-ticker.C:
				log.Println("Running cache cleanup...")
				deletedCount, err := runCleanup(cacheDir, cacheTTL)
				if err != nil {
					log.Printf("ERROR during cache cleanup: %v", err)
				} else {
					log.Printf("Cache cleanup finished. Deleted %d expired files.", deletedCount)
				}
			case <-stopChan:
				log.Println("Stopping cache cleaner ticker.")
				ticker.Stop()
				return
			case <-ctx.Done(): // Listen for global context cancellation
				log.Println("Stopping cache cleaner due to context cancellation.")
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
			log.Printf("Error accessing path %s during cleanup walk: %v", path, err)
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
			log.Printf("Error getting info for %s: %v", path, err)
			return nil // Continue
		}

		// Check if file modification time is before the minimum allowed time
		if info.ModTime().Before(minModTime) {
			log.Printf("Deleting expired cache file: %s (ModTime: %s)", path, info.ModTime())
			err := os.Remove(path)
			if err != nil {
				log.Printf("Error deleting file %s: %v", path, err)
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
