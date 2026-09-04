package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnnylibretexts/openstax-cli/internal/openstax"
)

type fakeOpenStaxClient struct {
	catalog      openstax.CatalogResponse
	book         openstax.Book
	archive      openstax.ArchiveBook
	pageHTML     map[string]string
	getBody      []byte
	downloadBody []byte
	downloadErr  error
	downloadURLs []string
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func (f *fakeOpenStaxClient) Catalog(context.Context) (openstax.CatalogResponse, error) {
	return f.catalog, nil
}

func (f *fakeOpenStaxClient) ResolveBook(context.Context, string) (openstax.Book, error) {
	return f.book, nil
}

func (f *fakeOpenStaxClient) ArchiveBook(context.Context, openstax.Book) (openstax.ArchiveBook, error) {
	return f.archive, nil
}

func (f *fakeOpenStaxClient) PageXHTML(_ context.Context, _ openstax.ArchiveBook, page openstax.TOCNode) ([]byte, string, error) {
	body, ok := f.pageHTML[page.Slug]
	if !ok {
		return nil, "", fmt.Errorf("missing fake page %q", page.Slug)
	}
	return []byte(body), page.OpenStaxPageURL, nil
}

func (f *fakeOpenStaxClient) Get(context.Context, string) ([]byte, error) {
	return f.getBody, nil
}

func (f *fakeOpenStaxClient) Download(_ context.Context, rawURL string, w io.Writer) (int64, error) {
	f.downloadURLs = append(f.downloadURLs, rawURL)
	if f.downloadErr != nil {
		// Mimic a transfer that fails partway through, after bytes reached the file.
		if len(f.downloadBody) > 0 {
			_, _ = w.Write(f.downloadBody)
		}
		return 0, f.downloadErr
	}
	n, err := w.Write(f.downloadBody)
	return int64(n), err
}

func executeWithFake(t *testing.T, fake *fakeOpenStaxClient, args ...string) (string, string, error) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := executeArgsWithClient(args, &stdout, &stderr, func(time.Duration) openstaxClient { return fake })
	return stdout.String(), stderr.String(), err
}

func testBookAndArchive(pageCount int) (openstax.Book, openstax.ArchiveBook, map[string]string) {
	book := openstax.Book{
		Slug:           "books/example-book",
		BookState:      "live",
		Title:          "Example Book",
		Subjects:       []string{"Science"},
		PDFURL:         "https://example.test/book.pdf",
		WebviewRexLink: "https://openstax.org/books/example-book/pages/page-01",
	}
	archive := openstax.ArchiveBook{
		Title:      "Example Book",
		Revised:    "2026-01-01T00:00:00Z",
		Slug:       "example-book",
		ID:         "book-id",
		Version:    "version-1",
		Language:   "en",
		License:    openstax.License{Name: "CC BY 4.0", URL: "https://creativecommons.org/licenses/by/4.0/"},
		ArchiveURL: "https://example.test/archive",
		Tree:       openstax.TOCNode{Title: "Example Book", TOCType: "book"},
	}
	pageHTML := make(map[string]string, pageCount)
	for i := 1; i <= pageCount; i++ {
		slug := fmt.Sprintf("page-%02d", i)
		page := openstax.TOCNode{
			ID:              fmt.Sprintf("page-id-%02d@version-1", i),
			Title:           fmt.Sprintf("Page %02d", i),
			TOCType:         "book-content",
			Slug:            slug,
			OpenStaxPageURL: "https://openstax.org/books/example-book/pages/" + slug,
		}
		archive.Tree.Contents = append(archive.Tree.Contents, page)
		pageHTML[slug] = `<html><body><div data-book-content="true"><p>abcdefghij</p></div></body></html>`
	}
	openstax.DecorateTree(&archive.Tree, archive, 0)
	return book, archive, pageHTML
}

func TestSearchAgentUsesCompactDefaultPage(t *testing.T) {
	var books []openstax.Book
	for i := 1; i <= 7; i++ {
		books = append(books, openstax.Book{
			Slug:      fmt.Sprintf("books/book-%02d", i),
			BookState: "live",
			Title:     fmt.Sprintf("Book %02d", i),
			Subjects:  []string{"Science"},
			CoverURL:  "https://example.test/large-unused-field.svg",
			PDFURL:    "https://example.test/large-unused-field.pdf",
		})
	}
	fake := &fakeOpenStaxClient{catalog: openstax.CatalogResponse{Books: books}}

	stdout, stderr, err := executeWithFake(t, fake, "search", "book", "--agent")
	if err != nil {
		t.Fatalf("search --agent: %v; stderr=%s", err, stderr)
	}
	var got struct {
		OK   bool               `json:"ok"`
		Data []agentBookSummary `json:"data"`
		Meta agentMeta          `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || len(got.Data) != 5 {
		t.Fatalf("unexpected result: %#v", got)
	}
	if !got.Meta.HasMore || got.Meta.NextOffset == nil || *got.Meta.NextOffset != 5 || got.Meta.Total != 7 {
		t.Fatalf("unexpected pagination: %#v", got.Meta)
	}
	if strings.Contains(stdout, "cover_url") || strings.Contains(stdout, "pdf_url") {
		t.Fatalf("search response is not compact: %s", stdout)
	}
}

func TestLegacySearchJSONRemainsRawArray(t *testing.T) {
	book, _, _ := testBookAndArchive(1)
	fake := &fakeOpenStaxClient{catalog: openstax.CatalogResponse{Books: []openstax.Book{book}}}

	stdout, stderr, err := executeWithFake(t, fake, "search", "example", "--json")
	if err != nil {
		t.Fatalf("search --json: %v; stderr=%s", err, stderr)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout), "[") {
		t.Fatalf("legacy JSON should remain a raw array: %s", stdout)
	}
	if !strings.Contains(stdout, "pdf_url") {
		t.Fatalf("legacy JSON should retain full catalog records: %s", stdout)
	}
}

func TestInfoAgentOmitsArchiveTree(t *testing.T) {
	book, archive, pageHTML := testBookAndArchive(2)
	fake := &fakeOpenStaxClient{book: book, archive: archive, pageHTML: pageHTML}

	stdout, stderr, err := executeWithFake(t, fake, "info", "example-book", "--agent")
	if err != nil {
		t.Fatalf("info --agent: %v; stderr=%s", err, stderr)
	}
	var got struct {
		Data agentBookDetails `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if got.Data.Slug != "example-book" || got.Data.PageCount != 2 {
		t.Fatalf("unexpected details: %#v", got.Data)
	}
	if strings.Contains(stdout, `"tree"`) || strings.Contains(stdout, `"catalog"`) {
		t.Fatalf("agent info leaked raw upstream objects: %s", stdout)
	}
}

func TestTOCAgentReturnsCompactPaginatedPages(t *testing.T) {
	book, archive, pageHTML := testBookAndArchive(25)
	fake := &fakeOpenStaxClient{book: book, archive: archive, pageHTML: pageHTML}

	stdout, stderr, err := executeWithFake(t, fake, "toc", "example-book", "--agent")
	if err != nil {
		t.Fatalf("toc --agent: %v; stderr=%s", err, stderr)
	}
	var got struct {
		Data []agentPageSummary `json:"data"`
		Meta agentMeta          `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 20 || !got.Meta.HasMore || got.Meta.NextOffset == nil || *got.Meta.NextOffset != 20 {
		t.Fatalf("unexpected result: %#v", got)
	}
	if got.Data[0].Slug != "page-01" || got.Data[0].Title != "Page 01" {
		t.Fatalf("unexpected first page: %#v", got.Data[0])
	}
	if strings.Contains(stdout, `"contents"`) || strings.Contains(stdout, `"archive_page_url"`) {
		t.Fatalf("agent toc leaked raw archive fields: %s", stdout)
	}
}

func TestExtractAgentDefaultsToJSONAndChunksText(t *testing.T) {
	book, archive, pageHTML := testBookAndArchive(1)
	fake := &fakeOpenStaxClient{book: book, archive: archive, pageHTML: pageHTML}

	stdout, stderr, err := executeWithFake(t, fake, "extract", "example-book", "--page", "page-01", "--start-char", "2", "--max-chars", "4", "--agent")
	if err != nil {
		t.Fatalf("extract --agent: %v; stderr=%s", err, stderr)
	}
	var got struct {
		Data []agentExtractedPage `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("agent extract is not JSON: %v; output=%s", err, stdout)
	}
	if len(got.Data) != 1 || got.Data[0].Text != "cdef" {
		t.Fatalf("unexpected extracted chunk: %#v", got.Data)
	}
	if got.Data[0].NextStartChar == nil || *got.Data[0].NextStartChar != 6 {
		t.Fatalf("missing text continuation: %#v", got.Data[0])
	}
}

func TestExtractRawJSONFlagIncludesHTMLWhenRequested(t *testing.T) {
	book, archive, pageHTML := testBookAndArchive(1)
	fake := &fakeOpenStaxClient{book: book, archive: archive, pageHTML: pageHTML}

	stdout, stderr, err := executeWithFake(t, fake, "extract", "example-book", "--page", "page-01", "--include-html", "--json")
	if err != nil {
		t.Fatalf("extract --json --include-html: %v; stderr=%s", err, stderr)
	}
	var got []openstax.ExtractedPage
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("raw extract is not JSON: %v; output=%s", err, stdout)
	}
	if len(got) != 1 || !strings.Contains(got[0].HTML, "data-book-content") {
		t.Fatalf("raw HTML missing from extract: %#v", got)
	}
}

func TestExtractAllAgentDefaultsToOnePage(t *testing.T) {
	book, archive, pageHTML := testBookAndArchive(3)
	fake := &fakeOpenStaxClient{book: book, archive: archive, pageHTML: pageHTML}

	stdout, stderr, err := executeWithFake(t, fake, "extract", "example-book", "--all", "--agent")
	if err != nil {
		t.Fatalf("extract --all --agent: %v; stderr=%s", err, stderr)
	}
	var got struct {
		Data []agentExtractedPage `json:"data"`
		Meta agentMeta            `json:"meta"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 1 || !got.Meta.HasMore || got.Meta.NextOffset == nil || *got.Meta.NextOffset != 1 {
		t.Fatalf("unexpected bounded extraction: %#v", got)
	}
}

func TestDownloadAgentReportsWrittenFile(t *testing.T) {
	book, archive, pageHTML := testBookAndArchive(1)
	fake := &fakeOpenStaxClient{book: book, archive: archive, pageHTML: pageHTML, downloadBody: []byte("%PDF-test")}
	outPath := filepath.Join(t.TempDir(), "book.pdf")

	stdout, stderr, err := executeWithFake(t, fake, "download", "example-book", "--kind", "pdf", "--output", outPath, "--agent")
	if err != nil {
		t.Fatalf("download --agent: %v; stderr=%s", err, stderr)
	}
	var got struct {
		Data agentDownloadResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if got.Data.Path != outPath || got.Data.Kind != "pdf" || got.Data.Bytes != int64(len("%PDF-test")) {
		t.Fatalf("unexpected download result: %#v", got.Data)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "%PDF-test" {
		t.Fatalf("written body = %q", body)
	}
}

func TestTextCommandsPropagateOutputErrors(t *testing.T) {
	writeErr := errors.New("test output failure")
	book, archive, pageHTML := testBookAndArchive(1)
	fake := &fakeOpenStaxClient{
		catalog:  openstax.CatalogResponse{Books: []openstax.Book{book}},
		book:     book,
		archive:  archive,
		pageHTML: pageHTML,
	}
	tests := []struct {
		name string
		args []string
	}{
		{name: "schema", args: []string{"schema"}},
		{name: "doctor", args: []string{"doctor"}},
		{name: "books", args: []string{"books"}},
		{name: "search", args: []string{"search", "example"}},
		{name: "info", args: []string{"info", "example-book"}},
		{name: "toc", args: []string{"toc", "example-book"}},
		{name: "extract", args: []string{"extract", "example-book", "--page", "page-01"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			err := executeArgsWithClient(tt.args, failingWriter{err: writeErr}, &stderr, func(time.Duration) openstaxClient { return fake })
			if !errors.Is(err, writeErr) {
				t.Fatalf("error = %v, want output failure", err)
			}
		})
	}
}
