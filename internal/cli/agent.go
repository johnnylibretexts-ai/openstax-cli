package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/johnnylibretexts/openstax-cli/internal/openstax"
)

const agentSchemaVersion = "1"

type agentEnvelope struct {
	SchemaVersion string             `json:"schema_version"`
	OK            bool               `json:"ok"`
	Data          any                `json:"data,omitempty"`
	Meta          *agentMeta         `json:"meta,omitempty"`
	Error         *agentErrorPayload `json:"error,omitempty"`
}

type agentMeta struct {
	Command    string `json:"command,omitempty"`
	Count      int    `json:"count"`
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
	Total      int    `json:"total"`
	HasMore    bool   `json:"has_more"`
	NextOffset *int   `json:"next_offset,omitempty"`
}

type agentErrorPayload struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
	Retryable  bool   `json:"retryable"`
}

type agentInputError struct {
	Code       string
	Message    string
	Suggestion string
}

func (e *agentInputError) Error() string { return e.Message }

type reportedError struct{ err error }

func (e *reportedError) Error() string { return e.err.Error() }
func (e *reportedError) Unwrap() error { return e.err }

func ErrorAlreadyReported(err error) bool {
	var reported *reportedError
	return errors.As(err, &reported)
}

type agentBookSummary struct {
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	State    string   `json:"state"`
	Subjects []string `json:"subjects,omitempty"`
}

type agentLicense struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type agentBookDetails struct {
	Slug       string       `json:"slug"`
	Title      string       `json:"title"`
	State      string       `json:"state"`
	Subjects   []string     `json:"subjects,omitempty"`
	WebURL     string       `json:"web_url,omitempty"`
	PDFURL     string       `json:"pdf_url,omitempty"`
	Revised    string       `json:"revised,omitempty"`
	Language   string       `json:"language,omitempty"`
	PageCount  int          `json:"page_count"`
	License    agentLicense `json:"license"`
	ArchiveID  string       `json:"archive_id,omitempty"`
	Version    string       `json:"version,omitempty"`
	ArchiveURL string       `json:"archive_url,omitempty"`
}

type agentPageSummary struct {
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	PageID    string `json:"page_id"`
	Depth     int    `json:"depth"`
	SourceURL string `json:"source_url"`
}

type agentExtractedPage struct {
	BookSlug      string `json:"book_slug"`
	PageSlug      string `json:"page_slug"`
	PageTitle     string `json:"page_title"`
	PageID        string `json:"page_id"`
	SourceURL     string `json:"source_url"`
	Text          string `json:"text"`
	CharStart     int    `json:"char_start"`
	CharEnd       int    `json:"char_end"`
	TotalChars    int    `json:"total_chars"`
	Truncated     bool   `json:"truncated"`
	NextStartChar *int   `json:"next_start_char,omitempty"`
}

type agentDownloadResult struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Bytes int64  `json:"bytes"`
}

type agentCapability struct {
	Name       string   `json:"name"`
	Purpose    string   `json:"purpose"`
	Usage      string   `json:"usage"`
	Returns    string   `json:"returns"`
	SideEffect string   `json:"side_effect,omitempty"`
	Defaults   []string `json:"defaults,omitempty"`
}

type agentSchema struct {
	AgentFlag string            `json:"agent_flag"`
	Rules     []string          `json:"rules"`
	Commands  []agentCapability `json:"commands"`
}

func currentAgentSchema() agentSchema {
	return agentSchema{
		AgentFlag: "--agent",
		Rules: []string{
			"Every response is one compact JSON object.",
			"Follow next_offset or next_start_char when present.",
			"Use download only when a file is explicitly requested.",
		},
		Commands: []agentCapability{
			{Name: "doctor", Purpose: "Check OpenStax connectivity.", Usage: "doctor --agent", Returns: "status and catalog book count"},
			{Name: "books", Purpose: "List books, optionally by subject.", Usage: "books [--subject SUBJECT] [--limit N] [--offset N] --agent", Returns: "compact book summaries", Defaults: []string{"limit=10"}},
			{Name: "search", Purpose: "Find books by title, slug, or subject.", Usage: "search QUERY [--subject SUBJECT] [--limit N] [--offset N] --agent", Returns: "compact book summaries", Defaults: []string{"limit=5"}},
			{Name: "info", Purpose: "Get one book's metadata without its full table of contents.", Usage: "info BOOK --agent", Returns: "compact book metadata, license, and page count"},
			{Name: "toc", Purpose: "List extractable pages for a book.", Usage: "toc BOOK [--limit N] [--offset N] --agent", Returns: "compact page summaries", Defaults: []string{"limit=20"}},
			{Name: "extract", Purpose: "Read one page or a bounded page range.", Usage: "extract BOOK [--page PAGE | --all] [--limit N] [--offset N] [--max-chars N] [--start-char N] --agent", Returns: "page text with continuation metadata", Defaults: []string{"limit=1", "max-chars=12000"}},
			{Name: "download", Purpose: "Write a PDF, text, JSON, or HTML file.", Usage: "download BOOK --output PATH [--kind KIND] [--page PAGE] --agent", Returns: "written path, kind, and byte count", SideEffect: "writes_file", Defaults: []string{"kind=pdf", "whole book unless --page"}},
		},
	}
}

func writeCompactJSON(w io.Writer, value any) error {
	return json.NewEncoder(w).Encode(value)
}

func writeAgentSuccess(w io.Writer, command string, data any, meta *agentMeta) error {
	if meta != nil {
		meta.Command = command
	}
	return writeCompactJSON(w, agentEnvelope{
		SchemaVersion: agentSchemaVersion,
		OK:            true,
		Data:          data,
		Meta:          meta,
	})
}

func writeAgentError(w io.Writer, err error) error {
	payload := classifyAgentError(err)
	return writeCompactJSON(w, agentEnvelope{
		SchemaVersion: agentSchemaVersion,
		OK:            false,
		Error:         &payload,
	})
}

func classifyAgentError(err error) agentErrorPayload {
	var inputErr *agentInputError
	if errors.As(err, &inputErr) {
		return agentErrorPayload{Code: inputErr.Code, Message: inputErr.Message, Suggestion: inputErr.Suggestion}
	}

	message := err.Error()
	lower := strings.ToLower(message)
	payload := agentErrorPayload{Code: "operation_failed", Message: message}
	switch {
	case strings.Contains(lower, "unknown command"):
		payload.Code = "unknown_command"
		payload.Suggestion = "Run 'openstax-pp-cli schema --agent' and choose one listed command."
	case strings.Contains(lower, "unknown flag"), strings.Contains(lower, "accepts ") && strings.Contains(lower, "arg"):
		payload.Code = "invalid_arguments"
		payload.Suggestion = "Run 'openstax-pp-cli schema --agent' for the compact command contract."
	case strings.Contains(lower, "book ") && strings.Contains(lower, "not found"):
		payload.Code = "book_not_found"
		payload.Suggestion = "Run search with the same title or subject, then reuse the returned slug."
	case strings.Contains(lower, "page ") && strings.Contains(lower, "not found"):
		payload.Code = "page_not_found"
		payload.Suggestion = "Run toc for the book, then reuse a returned page slug."
	case errors.Is(err, context.DeadlineExceeded):
		payload.Code = "timeout"
		payload.Retryable = true
		payload.Suggestion = "Retry once or increase --timeout."
	case strings.Contains(lower, "http 429"):
		payload.Code = "rate_limited"
		payload.Retryable = true
		payload.Suggestion = "Wait before retrying."
	case strings.Contains(lower, "http 5"):
		payload.Code = "upstream_error"
		payload.Retryable = true
		payload.Suggestion = "Retry later."
	default:
		var netErr net.Error
		var pathErr *os.PathError
		if errors.As(err, &netErr) {
			payload.Code = "network_error"
			payload.Retryable = true
			payload.Suggestion = "Check connectivity with doctor, then retry."
		} else if errors.As(err, &pathErr) {
			payload.Code = "file_error"
			payload.Suggestion = "Use an explicit writable path with --output."
		}
	}
	return payload
}

func summarizeBooks(books []openstax.Book) []agentBookSummary {
	out := make([]agentBookSummary, 0, len(books))
	for _, book := range books {
		out = append(out, agentBookSummary{
			Slug:     openstax.BookSlug(book.Slug),
			Title:    book.Title,
			State:    book.BookState,
			Subjects: book.Subjects,
		})
	}
	return out
}

func summarizeBook(book openstax.Book, archive openstax.ArchiveBook) agentBookDetails {
	pdfURL := book.HighResolutionPDFURL
	if pdfURL == "" {
		pdfURL = book.PDFURL
	}
	return agentBookDetails{
		Slug:       openstax.BookSlug(book.Slug),
		Title:      book.Title,
		State:      book.BookState,
		Subjects:   book.Subjects,
		WebURL:     book.WebviewRexLink,
		PDFURL:     pdfURL,
		Revised:    archive.Revised,
		Language:   archive.Language,
		PageCount:  len(openstax.FlattenPages(archive.Tree)),
		License:    agentLicense{Name: archive.License.Name, URL: archive.License.URL},
		ArchiveID:  archive.ID,
		Version:    archive.Version,
		ArchiveURL: archive.ArchiveURL,
	}
}

func summarizePages(pages []openstax.TOCNode) []agentPageSummary {
	out := make([]agentPageSummary, 0, len(pages))
	for _, page := range pages {
		out = append(out, agentPageSummary{
			Slug:      page.Slug,
			Title:     page.TextTitle,
			PageID:    openstax.PageID(page.ID),
			Depth:     page.Depth,
			SourceURL: page.OpenStaxPageURL,
		})
	}
	return out
}

func agentWindow(total, offset, limit, defaultLimit, maxLimit int) (int, int, agentMeta, error) {
	if offset < 0 {
		return 0, 0, agentMeta{}, &agentInputError{Code: "invalid_offset", Message: "offset must be zero or greater", Suggestion: "Use --offset 0 for the first result page."}
	}
	if limit < 0 {
		return 0, 0, agentMeta{}, &agentInputError{Code: "invalid_limit", Message: "limit must be zero or greater", Suggestion: fmt.Sprintf("Omit --limit to use the default of %d.", defaultLimit)}
	}
	if limit == 0 {
		limit = defaultLimit
	}
	if maxLimit > 0 && limit > maxLimit {
		return 0, 0, agentMeta{}, &agentInputError{Code: "limit_too_large", Message: fmt.Sprintf("limit %d exceeds the agent maximum of %d", limit, maxLimit), Suggestion: fmt.Sprintf("Use --limit %d or less and follow next_offset.", maxLimit)}
	}
	start := offset
	if start > total {
		start = total
	}
	end := total
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	meta := agentMeta{Count: end - start, Offset: start, Limit: limit, Total: total, HasMore: end < total}
	if meta.HasMore {
		next := end
		meta.NextOffset = &next
	}
	return start, end, meta, nil
}

func compactExtractedPage(page openstax.ExtractedPage, startChar, maxChars int) (agentExtractedPage, error) {
	if startChar < 0 {
		return agentExtractedPage{}, &agentInputError{Code: "invalid_start_char", Message: "start-char must be zero or greater", Suggestion: "Use --start-char 0 for the beginning of the page."}
	}
	if maxChars < 1 {
		return agentExtractedPage{}, &agentInputError{Code: "invalid_max_chars", Message: "max-chars must be at least 1", Suggestion: "Omit --max-chars to use 12000."}
	}
	runes := []rune(page.Text)
	if startChar > len(runes) {
		return agentExtractedPage{}, &agentInputError{Code: "start_char_out_of_range", Message: fmt.Sprintf("start-char %d exceeds page length %d", startChar, len(runes)), Suggestion: "Use the total_chars value from the previous response."}
	}
	end := startChar + maxChars
	if end > len(runes) {
		end = len(runes)
	}
	result := agentExtractedPage{
		BookSlug:   openstax.BookSlug(page.BookSlug),
		PageSlug:   page.PageSlug,
		PageTitle:  page.PageTitle,
		PageID:     page.PageID,
		SourceURL:  page.OpenStaxPageURL,
		Text:       string(runes[startChar:end]),
		CharStart:  startChar,
		CharEnd:    end,
		TotalChars: len(runes),
		Truncated:  startChar > 0 || end < len(runes),
	}
	if end < len(runes) {
		next := end
		result.NextStartChar = &next
	}
	return result, nil
}

func compactExtractedPages(pages []openstax.ExtractedPage, startChar, maxChars int) ([]agentExtractedPage, error) {
	out := make([]agentExtractedPage, 0, len(pages))
	for _, page := range pages {
		compact, err := compactExtractedPage(page, startChar, maxChars)
		if err != nil {
			return nil, err
		}
		out = append(out, compact)
	}
	return out, nil
}

// argsRequestAgent is a fallback for the case where flag parsing itself failed
// and the parsed flag value is therefore unset. pflag accepts every strconv
// boolean spelling, so match on the parsed value rather than on "--agent=true".
func argsRequestAgent(args []string) bool {
	for _, arg := range args {
		if arg == "--agent" {
			return true
		}
		if value, ok := strings.CutPrefix(arg, "--agent="); ok {
			if enabled, err := strconv.ParseBool(value); err == nil && enabled {
				return true
			}
		}
	}
	return false
}

func executeArgs(args []string, stdout, stderr io.Writer) error {
	return executeArgsWithClient(args, stdout, stderr, func(timeout time.Duration) openstaxClient {
		return openstax.New(timeout)
	})
}

func executeArgsWithClient(args []string, stdout, stderr io.Writer, factory clientFactory) error {
	root, f := rootCmdWithClient(factory)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	if err == nil || (!f.agent && !argsRequestAgent(args)) {
		return err
	}
	// The envelope goes to stdout, alongside successful responses, so that a
	// harness capturing only stdout still receives a structured failure instead
	// of nothing. Fall back to stderr if stdout is unusable, rather than losing
	// the error entirely.
	if writeErr := writeAgentError(stdout, err); writeErr != nil {
		if fallbackErr := writeAgentError(stderr, err); fallbackErr != nil {
			return fmt.Errorf("%w; report agent error: %v", err, writeErr)
		}
	}
	return &reportedError{err: err}
}
