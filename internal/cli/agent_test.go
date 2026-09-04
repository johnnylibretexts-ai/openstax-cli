package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/johnnylibretexts/openstax-cli/internal/openstax"
)

func TestAgentSchemaCommandEmitsCompactEnvelope(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := executeArgs([]string{"schema", "--agent"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("schema --agent: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	if strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("agent JSON should be one compact line, got %q", stdout.String())
	}

	var got struct {
		SchemaVersion string `json:"schema_version"`
		OK            bool   `json:"ok"`
		Data          struct {
			Commands []struct {
				Name string `json:"name"`
			} `json:"commands"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode agent envelope: %v", err)
	}
	if got.SchemaVersion != agentSchemaVersion || !got.OK {
		t.Fatalf("unexpected envelope: %#v", got)
	}
	if len(got.Data.Commands) != 7 {
		t.Fatalf("got %d commands, want 7", len(got.Data.Commands))
	}
}

func TestAgentErrorsAreStructuredAndReportedOnce(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := executeArgs([]string{"does-not-exist", "--agent"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected command error")
	}
	if !ErrorAlreadyReported(err) {
		t.Fatalf("agent error should be marked reported: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("agent errors belong on stdout, but stderr had: %s", stderr.String())
	}

	var got struct {
		OK    bool `json:"ok"`
		Error struct {
			Code       string `json:"code"`
			Suggestion string `json:"suggestion"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode agent error: %v; output=%q", err, stdout.String())
	}
	if got.OK {
		t.Fatal("error envelope reported ok=true")
	}
	if got.Error.Code != "unknown_command" {
		t.Fatalf("error code = %q, want unknown_command", got.Error.Code)
	}
	if got.Error.Suggestion == "" {
		t.Fatal("expected recovery suggestion")
	}
}

func TestAgentBookSummariesOmitLargeCatalogFields(t *testing.T) {
	books := []openstax.Book{{
		ID:                42,
		Slug:              "books/example-book",
		BookState:         "live",
		Title:             "Example Book",
		Subjects:          []string{"Math"},
		CoverURL:          "https://example.test/cover.svg",
		PDFURL:            "https://example.test/book.pdf",
		WebviewRexLink:    "https://openstax.org/books/example-book/pages/preface",
		BookshareLink:     "https://example.test/bookshare",
		AmazonLink:        "https://example.test/store",
		LastUpdatedPDF:    "2026-01-01",
		K12Subject:        []string{"Algebra"},
		WebviewLink:       "https://example.test/legacy",
		SubjectCategories: []string{"Mathematics"},
	}}

	got := summarizeBooks(books)
	if len(got) != 1 {
		t.Fatalf("got %d summaries, want 1", len(got))
	}
	if got[0].Slug != "example-book" || got[0].Title != "Example Book" {
		t.Fatalf("unexpected summary: %#v", got[0])
	}

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"cover_url", "bookshare_link", "amazon_link", "webview_link"} {
		if bytes.Contains(body, []byte(forbidden)) {
			t.Fatalf("compact summary contains %q: %s", forbidden, body)
		}
	}
}

func TestAgentWindowAppliesDefaultsAndReportsContinuation(t *testing.T) {
	start, end, meta, err := agentWindow(53, 20, 0, 20, 100)
	if err != nil {
		t.Fatal(err)
	}
	if start != 20 || end != 40 {
		t.Fatalf("window = %d:%d, want 20:40", start, end)
	}
	if !meta.HasMore || meta.NextOffset == nil || *meta.NextOffset != 40 {
		t.Fatalf("unexpected continuation metadata: %#v", meta)
	}
}

func TestAgentWindowRejectsOversizedRequests(t *testing.T) {
	_, _, _, err := agentWindow(53, 0, 101, 20, 100)
	if err == nil {
		t.Fatal("expected oversized limit error")
	}
	var inputErr *agentInputError
	if !errors.As(err, &inputErr) || inputErr.Code != "limit_too_large" {
		t.Fatalf("unexpected error: %T %v", err, err)
	}
}

func TestCompactExtractedPageChunksUnicodeText(t *testing.T) {
	page := openstax.ExtractedPage{
		BookSlug:        "books/example-book",
		PageTitle:       "Example",
		PageSlug:        "example-page",
		PageID:          "page-id",
		OpenStaxPageURL: "https://openstax.org/books/example-book/pages/example-page",
		Text:            "abçdef",
	}

	got, err := compactExtractedPage(page, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "çde" {
		t.Fatalf("text = %q, want %q", got.Text, "çde")
	}
	if got.CharStart != 2 || got.CharEnd != 5 || got.TotalChars != 6 {
		t.Fatalf("unexpected range: %#v", got)
	}
	if !got.Truncated || got.NextStartChar == nil || *got.NextStartChar != 5 {
		t.Fatalf("unexpected continuation: %#v", got)
	}
}

func TestAgentDownloadRequiresExplicitOutputBeforeNetworkAccess(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := executeArgs([]string{"download", "example-book", "--agent"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected output requirement error")
	}

	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if decodeErr := json.Unmarshal(stdout.Bytes(), &got); decodeErr != nil {
		t.Fatalf("decode agent error: %v; output=%q", decodeErr, stdout.String())
	}
	if got.Error.Code != "output_required" {
		t.Fatalf("error code = %q, want output_required", got.Error.Code)
	}
}
