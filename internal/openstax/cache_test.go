package openstax

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// redirectCache points the catalog cache at a temp directory for one test.
func redirectCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	original := userCacheDir
	userCacheDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userCacheDir = original })
	return filepath.Join(dir, "openstax-pp-cli", cacheFileName)
}

func catalogServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, `{"title":"Catalog","books":[{"slug":"a-book","title":"A Book","book_state":"live"}]}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// catalogVia exercises the real Client.Catalog against a local server.
func catalogVia(t *testing.T, c *Client, url string) (CatalogResponse, error) {
	t.Helper()
	c.catalogURL = url
	return c.Catalog(context.Background())
}

func TestCatalogCacheAvoidsRefetching(t *testing.T) {
	path := redirectCache(t)
	var hits atomic.Int32
	server := catalogServer(t, &hits)
	client := New(0)

	for i := 0; i < 3; i++ {
		got, err := catalogVia(t, client, server.URL)
		if err != nil {
			t.Fatalf("catalog fetch %d: %v", i, err)
		}
		if len(got.Books) != 1 || got.Books[0].Slug != "a-book" {
			t.Fatalf("fetch %d returned %#v", i, got.Books)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("catalog was fetched %d times, want 1", hits.Load())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cache file was not written: %v", err)
	}
}

func TestCatalogCacheExpires(t *testing.T) {
	path := redirectCache(t)
	var hits atomic.Int32
	server := catalogServer(t, &hits)
	client := New(0)

	if _, err := catalogVia(t, client, server.URL); err != nil {
		t.Fatal(err)
	}
	// Backdate the entry past its TTL.
	stale := time.Now().Add(-2 * DefaultCacheTTL)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	if _, err := catalogVia(t, client, server.URL); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("expired cache was reused: %d fetches", hits.Load())
	}
}

func TestSetCacheTTLZeroBypassesCache(t *testing.T) {
	redirectCache(t)
	var hits atomic.Int32
	server := catalogServer(t, &hits)
	client := New(0)

	if _, err := catalogVia(t, client, server.URL); err != nil {
		t.Fatal(err)
	}
	client.SetCacheTTL(0)
	if _, err := catalogVia(t, client, server.URL); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("SetCacheTTL(0) still served from cache: %d fetches", hits.Load())
	}
}

func TestCorruptCacheFallsBackToNetwork(t *testing.T) {
	path := redirectCache(t)
	var hits atomic.Int32
	server := catalogServer(t, &hits)
	client := New(0)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := catalogVia(t, client, server.URL)
	if err != nil {
		t.Fatalf("corrupt cache should not be fatal: %v", err)
	}
	if len(got.Books) != 1 {
		t.Fatalf("unexpected catalog: %#v", got.Books)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected one live fetch, got %d", hits.Load())
	}
}

// A machine with no writable cache directory must still work.
func TestUnwritableCacheIsNotFatal(t *testing.T) {
	original := userCacheDir
	userCacheDir = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { userCacheDir = original })

	var hits atomic.Int32
	server := catalogServer(t, &hits)
	client := New(0)

	for i := 0; i < 2; i++ {
		if _, err := catalogVia(t, client, server.URL); err != nil {
			t.Fatalf("fetch %d without a cache dir: %v", i, err)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("expected every call to hit the network, got %d", hits.Load())
	}
}
