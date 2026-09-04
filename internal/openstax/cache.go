package openstax

import (
	"os"
	"path/filepath"
	"time"
)

// DefaultCacheTTL bounds how long a cached catalog is reused. The catalog only
// changes when OpenStax publishes or retires a book, so reusing a day-old copy
// saves a 338 KB download on every single command.
const DefaultCacheTTL = 24 * time.Hour

const cacheFileName = "catalog.json"

// userCacheDir is a variable so tests can redirect the cache to a temp directory.
var userCacheDir = os.UserCacheDir

func catalogCachePath() (string, error) {
	dir, err := userCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "openstax-pp-cli", cacheFileName), nil
}

// readCatalogCache returns the cached catalog when it is younger than ttl. Every
// failure is reported as a cache miss: caching must never break a command.
func readCatalogCache(ttl time.Duration) ([]byte, bool) {
	if ttl <= 0 {
		return nil, false
	}
	path, err := catalogCachePath()
	if err != nil {
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil || time.Since(info.ModTime()) > ttl {
		return nil, false
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) == 0 {
		return nil, false
	}
	return body, true
}

// writeCatalogCache stores body via a temp file and a rename, so a concurrent
// reader never sees a half-written catalog. Errors are ignored: a machine with no
// writable cache directory should still work, just without caching.
func writeCatalogCache(body []byte) {
	path, err := catalogCachePath()
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, cacheFileName+".*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
	}
}
