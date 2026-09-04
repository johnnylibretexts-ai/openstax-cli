package openstax

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPageXHTMLIncludesBookVersion(t *testing.T) {
	const expectedPath = "/contents/book-id@version-7:page-id.xhtml"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "<html><body>page content</body></html>")
	}))
	defer server.Close()

	client := New(0)
	archive := ArchiveBook{ID: "book-id", Version: "version-7", ArchiveURL: server.URL}
	page := TOCNode{ID: "page-id@version-7", Title: "Page"}

	body, pageURL, err := client.PageXHTML(context.Background(), archive, page)
	if err != nil {
		t.Fatalf("fetch versioned page: %v", err)
	}
	if pageURL != server.URL+expectedPath {
		t.Fatalf("page URL = %q, want %q", pageURL, server.URL+expectedPath)
	}
	if string(body) != "<html><body>page content</body></html>" {
		t.Fatalf("unexpected body %q", body)
	}
}

// A book PDF is far larger than the request timeout allows for a buffered read,
// so Download must stream and must not inherit httpClient's whole-request deadline.
func TestDownloadStreamsBodyPastTheRequestTimeout(t *testing.T) {
	const chunks = 8
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("test server cannot flush")
			return
		}
		for i := 0; i < chunks; i++ {
			_, _ = io.WriteString(w, "0123456789")
			flusher.Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer server.Close()

	// Shorter than the total transfer time, which would abort a buffered read.
	client := New(50 * time.Millisecond)
	var got bytes.Buffer

	written, err := client.Download(context.Background(), server.URL, &got)
	if err != nil {
		t.Fatalf("stream download: %v", err)
	}
	if written != int64(chunks*10) || got.Len() != chunks*10 {
		t.Fatalf("wrote %d bytes, buffered %d, want %d", written, got.Len(), chunks*10)
	}
}

func TestDownloadReportsHTTPStatusErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "no such pdf")
	}))
	defer server.Close()

	var got bytes.Buffer
	written, err := New(0).Download(context.Background(), server.URL, &got)
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
	if written != 0 || got.Len() != 0 {
		t.Fatalf("error response leaked %d bytes into the output", got.Len())
	}
	if !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "no such pdf") {
		t.Fatalf("unhelpful error: %v", err)
	}
}
