package openstax

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	BaseURL    = "https://openstax.org"
	CatalogURL = BaseURL + "/apps/cms/api/books/?format=json"
	UserAgent  = "openstax-pp-cli/0.1 (+https://github.com/openstax/openstax_api; OpenStax public catalog/archive client)"
)

type Client struct {
	httpClient *http.Client
	// downloadClient has no whole-request deadline. Book PDFs run to hundreds of
	// megabytes, and http.Client.Timeout covers the body read, so a shared client
	// would abort large downloads partway through. A response-header timeout still
	// fails fast when the server itself is unresponsive.
	downloadClient *http.Client
	cacheTTL       time.Duration
	// catalogURL is a field so tests can point Catalog at a local server.
	catalogURL string
}

func New(timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = timeout
	return &Client{
		httpClient:     &http.Client{Timeout: timeout},
		downloadClient: &http.Client{Transport: transport},
		cacheTTL:       DefaultCacheTTL,
		catalogURL:     CatalogURL,
	}
}

// SetCacheTTL bounds how long a cached catalog may be reused. Zero disables
// reading from the cache, which is what --no-cache and doctor need: doctor exists
// to prove the catalog is reachable, so answering it from disk would be a lie.
func (c *Client) SetCacheTTL(ttl time.Duration) {
	c.cacheTTL = ttl
}

func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json, application/xhtml+xml, text/html;q=0.9, */*;q=0.8")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("GET %s: HTTP %d: %s", rawURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(resp.Body)
}

// Download streams rawURL into w instead of buffering it, so large book PDFs do
// not have to fit in memory. It returns the number of bytes written.
func (c *Client) Download(ctx context.Context, rawURL string, w io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/pdf, */*;q=0.8")
	resp, err := c.downloadClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return 0, fmt.Errorf("GET %s: HTTP %d: %s", rawURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.Copy(w, resp.Body)
}

func (c *Client) Catalog(ctx context.Context) (CatalogResponse, error) {
	var out CatalogResponse
	if cached, ok := readCatalogCache(c.cacheTTL); ok {
		if err := json.Unmarshal(cached, &out); err == nil {
			return out, nil
		}
		// A corrupt cache entry is not fatal; fall through to a live fetch.
	}
	body, err := c.Get(ctx, c.catalogURL)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, err
	}
	writeCatalogCache(body)
	return out, nil
}

func (c *Client) ResolveBook(ctx context.Context, ref string) (Book, error) {
	cat, err := c.Catalog(ctx)
	if err != nil {
		return Book{}, err
	}
	refSlug := BookSlug(ref)
	var fallback *Book
	for i := range cat.Books {
		b := cat.Books[i]
		if BookSlug(b.Slug) == refSlug || BookSlug(b.WebviewRexLink) == refSlug {
			return b, nil
		}
		if strings.EqualFold(b.Title, ref) {
			fallback = &b
		}
	}
	if fallback != nil {
		return *fallback, nil
	}
	return Book{}, fmt.Errorf("book %q not found; run 'openstax-pp-cli search %q'", ref, ref)
}

func (c *Client) ArchiveBook(ctx context.Context, book Book) (ArchiveBook, error) {
	seedURL := book.WebviewRexLink
	if seedURL == "" {
		return ArchiveBook{}, fmt.Errorf("%q has no OpenStax webview_rex_link", book.Title)
	}
	page, err := c.Get(ctx, seedURL)
	if err != nil {
		return ArchiveBook{}, err
	}
	state, err := ExtractPreloadedState(page)
	if err != nil {
		return ArchiveBook{}, err
	}
	content, ok := state["content"].(map[string]any)
	if !ok {
		return ArchiveBook{}, fmt.Errorf("preloaded state has no content object")
	}
	bookState, ok := content["book"].(map[string]any)
	if !ok {
		return ArchiveBook{}, fmt.Errorf("preloaded state has no content.book object")
	}
	archiveVersion, _ := bookState["archiveVersion"].(string)
	archiveURL := BaseURL + "/apps/archive/" + archiveVersion
	id, _ := bookState["id"].(string)
	contentVersion, _ := bookState["contentVersion"].(string)
	if archiveVersion == "" || id == "" || contentVersion == "" {
		return ArchiveBook{}, fmt.Errorf("missing archive metadata in OpenStax page state")
	}
	archiveJSON := fmt.Sprintf("%s/contents/%s@%s.json", archiveURL, id, contentVersion)
	body, err := c.Get(ctx, archiveJSON)
	if err != nil {
		return ArchiveBook{}, err
	}
	var out ArchiveBook
	if err := json.Unmarshal(body, &out); err != nil {
		return ArchiveBook{}, err
	}
	out.ArchiveVersion = archiveVersion
	out.ArchiveURL = archiveURL
	DecorateTree(&out.Tree, out, 0)
	return out, nil
}

func (c *Client) PageXHTML(ctx context.Context, archive ArchiveBook, page TOCNode) ([]byte, string, error) {
	pageID := PageID(page.ID)
	if pageID == "" {
		return nil, "", fmt.Errorf("TOC node %q has no page id", page.Title)
	}
	u := fmt.Sprintf("%s/contents/%s@%s:%s.xhtml", archive.ArchiveURL, archive.ID, url.PathEscape(archive.Version), url.PathEscape(pageID))
	body, err := c.Get(ctx, u)
	return body, u, err
}
