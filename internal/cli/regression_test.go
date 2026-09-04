package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// download --page used to be silently ignored, because --all defaults to true so
// that a bare download saves the whole book.
func TestDownloadPageSelectsOnlyThatPage(t *testing.T) {
	book, archive, pageHTML := testBookAndArchive(3)
	pageHTML["page-02"] = `<html><body><div data-book-content="true"><p>only-me</p></div></body></html>`
	fake := &fakeOpenStaxClient{book: book, archive: archive, pageHTML: pageHTML}
	outPath := filepath.Join(t.TempDir(), "page.txt")

	if _, stderr, err := executeWithFake(t, fake, "download", "example-book", "--kind", "text", "--output", outPath, "--page", "page-02"); err != nil {
		t.Fatalf("download --page: %v; stderr=%s", err, stderr)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "only-me") {
		t.Fatalf("requested page missing from %q", body)
	}
	if strings.Contains(string(body), "Page 01") || strings.Contains(string(body), "Page 03") {
		t.Fatalf("--page downloaded the whole book: %q", body)
	}
}

func TestDownloadWithoutPageStillSavesWholeBook(t *testing.T) {
	book, archive, pageHTML := testBookAndArchive(3)
	fake := &fakeOpenStaxClient{book: book, archive: archive, pageHTML: pageHTML}
	outPath := filepath.Join(t.TempDir(), "book.txt")

	if _, stderr, err := executeWithFake(t, fake, "download", "example-book", "--kind", "text", "--output", outPath); err != nil {
		t.Fatalf("download: %v; stderr=%s", err, stderr)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Page 01", "Page 02", "Page 03"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("whole-book download missing %s: %q", want, body)
		}
	}
}

func TestDownloadRejectsPageWithExplicitAll(t *testing.T) {
	book, archive, pageHTML := testBookAndArchive(2)
	fake := &fakeOpenStaxClient{book: book, archive: archive, pageHTML: pageHTML}
	outPath := filepath.Join(t.TempDir(), "book.txt")

	stdout, _, err := executeWithFake(t, fake, "download", "example-book", "--kind", "text", "--output", outPath, "--page", "page-01", "--all", "--agent")
	if err == nil {
		t.Fatal("expected --page with explicit --all to fail")
	}
	if code := agentErrorCode(t, stdout); code != "conflicting_page_selection" {
		t.Fatalf("error code = %q", code)
	}
}

// PDFs are streamed rather than buffered, so the command must never read the
// whole body into memory via Get.
func TestPDFDownloadStreamsToFile(t *testing.T) {
	book, archive, pageHTML := testBookAndArchive(1)
	fake := &fakeOpenStaxClient{book: book, archive: archive, pageHTML: pageHTML, downloadBody: []byte("%PDF-stream")}
	fake.book.HighResolutionPDFURL = "https://example.test/high-res.pdf"
	outPath := filepath.Join(t.TempDir(), "nested", "book.pdf")

	stdout, stderr, err := executeWithFake(t, fake, "download", "example-book", "--kind", "pdf", "--output", outPath, "--agent")
	if err != nil {
		t.Fatalf("download pdf: %v; stderr=%s", err, stderr)
	}
	if len(fake.downloadURLs) != 1 || fake.downloadURLs[0] != "https://example.test/high-res.pdf" {
		t.Fatalf("pdf was not fetched through the streaming path: %v", fake.downloadURLs)
	}
	var got struct {
		Data agentDownloadResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if got.Data.Bytes != int64(len("%PDF-stream")) {
		t.Fatalf("reported byte count = %d", got.Data.Bytes)
	}
	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "%PDF-stream" {
		t.Fatalf("written body = %q", body)
	}
}

func TestFailedPDFDownloadRemovesPartialFile(t *testing.T) {
	book, archive, pageHTML := testBookAndArchive(1)
	fake := &fakeOpenStaxClient{
		book:         book,
		archive:      archive,
		pageHTML:     pageHTML,
		downloadBody: []byte("partial"),
		downloadErr:  errors.New("connection reset"),
	}
	outPath := filepath.Join(t.TempDir(), "book.pdf")

	if _, _, err := executeWithFake(t, fake, "download", "example-book", "--kind", "pdf", "--output", outPath, "--agent"); err == nil {
		t.Fatal("expected a failed download to return an error")
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Fatalf("partial download was left behind: %v", err)
	}
}

// pflag accepts every strconv boolean spelling, so --agent=1 is agent mode and
// its errors must still be reported as a JSON envelope.
func TestTruthyAgentSpellingsStillReportJSONErrors(t *testing.T) {
	for _, spelling := range []string{"--agent", "--agent=true", "--agent=1", "--agent=t", "--agent=TRUE"} {
		t.Run(spelling, func(t *testing.T) {
			fake := &fakeOpenStaxClient{}
			stdout, stderr, err := executeWithFake(t, fake, "extract", "example-book", "--all", "--page", "page-01", spelling)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !ErrorAlreadyReported(err) {
				t.Fatalf("%s did not report through the agent envelope", spelling)
			}
			if stderr != "" {
				t.Fatalf("%s wrote to stderr: %s", spelling, stderr)
			}
			if code := agentErrorCode(t, stdout); code != "conflicting_page_selection" {
				t.Fatalf("error code = %q", code)
			}
		})
	}
}

func TestFalsyAgentSpellingsKeepPlainTextErrors(t *testing.T) {
	fake := &fakeOpenStaxClient{}
	stdout, stderr, err := executeWithFake(t, fake, "extract", "example-book", "--all", "--page", "page-01", "--agent=false")
	if err == nil {
		t.Fatal("expected an error")
	}
	if ErrorAlreadyReported(err) || strings.Contains(stdout+stderr, "schema_version") {
		t.Fatalf("--agent=false should not emit an agent envelope: %s%s", stdout, stderr)
	}
}

// A single --start-char cannot be meaningful across a window of pages, and used
// to abort the whole command when any page in the window was shorter than it.
func TestExtractRejectsStartCharWithAll(t *testing.T) {
	book, archive, pageHTML := testBookAndArchive(3)
	pageHTML["page-02"] = `<html><body><div data-book-content="true"><p>hi</p></div></body></html>`
	fake := &fakeOpenStaxClient{book: book, archive: archive, pageHTML: pageHTML}

	stdout, _, err := executeWithFake(t, fake, "extract", "example-book", "--all", "--limit", "3", "--start-char", "5", "--agent")
	if err == nil {
		t.Fatal("expected --start-char with --all to fail")
	}
	if code := agentErrorCode(t, stdout); code != "conflicting_text_offset" {
		t.Fatalf("error code = %q", code)
	}
}

func TestExtractStartCharStillWorksForOnePage(t *testing.T) {
	book, archive, pageHTML := testBookAndArchive(1)
	fake := &fakeOpenStaxClient{book: book, archive: archive, pageHTML: pageHTML}

	stdout, stderr, err := executeWithFake(t, fake, "extract", "example-book", "--page", "page-01", "--start-char", "4", "--max-chars", "3", "--agent")
	if err != nil {
		t.Fatalf("extract --start-char: %v; stderr=%s", err, stderr)
	}
	var got struct {
		Data []agentExtractedPage `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 1 || got.Data[0].CharStart != 4 || got.Data[0].CharEnd != 7 {
		t.Fatalf("unexpected window: %#v", got.Data)
	}
}

// Agent errors go to stdout so harnesses that capture only stdout still see a
// structured failure, but an unusable stdout must not swallow the error.
func TestAgentErrorFallsBackToStderrWhenStdoutFails(t *testing.T) {
	var stderr bytes.Buffer
	writeErr := errors.New("stdout is closed")
	fake := &fakeOpenStaxClient{}

	err := executeArgsWithClient(
		[]string{"extract", "example-book", "--all", "--page", "page-01", "--agent"},
		failingWriter{err: writeErr}, &stderr,
		func(time.Duration) openstaxClient { return fake },
	)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !ErrorAlreadyReported(err) {
		t.Fatalf("fallback envelope should still count as reported: %v", err)
	}
	if code := agentErrorCode(t, stderr.String()); code != "conflicting_page_selection" {
		t.Fatalf("error code = %q", code)
	}
}

func agentErrorCode(t *testing.T, output string) string {
	t.Helper()
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("output is not an agent envelope (%v): %s", err, output)
	}
	return got.Error.Code
}
