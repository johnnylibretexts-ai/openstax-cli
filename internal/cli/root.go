package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/johnnylibretexts/openstax-cli/internal/openstax"
	"github.com/spf13/cobra"
)

const version = "0.1.0"

type flags struct {
	asJSON        bool
	agent         bool
	noCache       bool
	timeout       time.Duration
	clientFactory clientFactory
}

type openstaxClient interface {
	Catalog(context.Context) (openstax.CatalogResponse, error)
	ResolveBook(context.Context, string) (openstax.Book, error)
	ArchiveBook(context.Context, openstax.Book) (openstax.ArchiveBook, error)
	PageXHTML(context.Context, openstax.ArchiveBook, openstax.TOCNode) ([]byte, string, error)
	Get(context.Context, string) ([]byte, error)
	Download(context.Context, string, io.Writer) (int64, error)
}

type clientFactory func(time.Duration) openstaxClient

func Execute() error {
	return executeArgs(os.Args[1:], os.Stdout, os.Stderr)
}

func RootCmd() *cobra.Command {
	root, _ := rootCmdWithClient(func(timeout time.Duration) openstaxClient {
		return openstax.New(timeout)
	})
	return root
}

// rootCmdWithClient also returns the parsed flag set so that callers can tell
// whether agent mode was requested after Execute reports an error.
func rootCmdWithClient(factory clientFactory) (*cobra.Command, *flags) {
	f := &flags{clientFactory: factory}
	root := &cobra.Command{
		Use:           "openstax-pp-cli",
		Short:         "Search and extract OpenStax textbook content from the public catalog and archive.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.PersistentFlags().BoolVar(&f.asJSON, "json", false, "Output complete JSON")
	root.PersistentFlags().BoolVar(&f.agent, "agent", false, "Use the compact, versioned agent JSON contract")
	root.PersistentFlags().BoolVar(&f.noCache, "no-cache", false, "Ignore the cached catalog and fetch a fresh copy")
	root.PersistentFlags().DurationVar(&f.timeout, "timeout", 60*time.Second, "HTTP request timeout; for PDF downloads this bounds the response headers, not the transfer")
	root.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if f.agent {
			f.asJSON = true
		}
	}
	root.AddCommand(schemaCmd(f), doctorCmd(f), booksCmd(f), searchCmd(f), infoCmd(f), tocCmd(f), extractCmd(f), downloadCmd(f))
	return root, f
}

func schemaCmd(f *flags) *cobra.Command {
	return &cobra.Command{
		Use:     "schema",
		Aliases: []string{"capabilities"},
		Short:   "Describe the compact agent command contract.",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			schema := currentAgentSchema()
			if f.agent {
				return writeAgentSuccess(cmd.OutOrStdout(), "schema", schema, nil)
			}
			if f.asJSON {
				return openstax.WriteJSON(cmd.OutOrStdout(), schema)
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Use --agent for one-line JSON. Commands:"); err != nil {
				return err
			}
			for _, capability := range schema.Commands {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "  %s\t%s\n", capability.Name, capability.Purpose); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	return 1
}

// cacheConfigurable is implemented by the real client. Test doubles need not be.
type cacheConfigurable interface{ SetCacheTTL(time.Duration) }

func disableCache(c openstaxClient) {
	if configurable, ok := c.(cacheConfigurable); ok {
		configurable.SetCacheTTL(0)
	}
}

func clientAndContext(f *flags) (openstaxClient, context.Context) {
	c := f.clientFactory(f.timeout)
	if f.noCache {
		disableCache(c)
	}
	return c, context.Background()
}

func doctorCmd(f *flags) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check OpenStax catalog connectivity.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx := clientAndContext(f)
			// doctor reports whether the catalog is reachable, so it must never be
			// answered from the cache.
			disableCache(c)
			cat, err := c.Catalog(ctx)
			if err != nil {
				return err
			}
			result := map[string]any{
				"ok":          true,
				"catalog_url": openstax.CatalogURL,
				"books":       len(cat.Books),
			}
			if f.agent {
				return writeAgentSuccess(cmd.OutOrStdout(), "doctor", result, nil)
			}
			if f.asJSON {
				return openstax.WriteJSON(cmd.OutOrStdout(), result)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "ok: catalog reachable (%d books)\n", len(cat.Books))
			return err
		},
	}
}

func booksCmd(f *flags) *cobra.Command {
	var subject string
	var includeRetired bool
	var limit int
	var offset int
	cmd := &cobra.Command{
		Use:   "books",
		Short: "List OpenStax textbooks.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx := clientAndContext(f)
			cat, err := c.Catalog(ctx)
			if err != nil {
				return err
			}
			books := openstax.SearchBooks(cat.Books, "", subject, includeRetired)
			if f.agent {
				start, end, meta, err := agentWindow(len(books), offset, limit, 10, 100)
				if err != nil {
					return err
				}
				return writeAgentSuccess(cmd.OutOrStdout(), "books", summarizeBooks(books[start:end]), &meta)
			}
			if f.asJSON {
				return openstax.WriteJSON(cmd.OutOrStdout(), books)
			}
			return writeBooksTable(cmd.OutOrStdout(), books)
		},
	}
	cmd.Flags().StringVar(&subject, "subject", "", "Filter by subject or subject category")
	cmd.Flags().BoolVar(&includeRetired, "include-retired", false, "Include retired books")
	cmd.Flags().IntVar(&limit, "limit", 0, "Agent result limit (default 10, maximum 100)")
	cmd.Flags().IntVar(&offset, "offset", 0, "Agent result offset")
	return cmd
}

func searchCmd(f *flags) *cobra.Command {
	var subject string
	var includeRetired bool
	var limit int
	var offset int
	cmd := &cobra.Command{
		Use:   "search QUERY",
		Short: "Search OpenStax textbooks by title, slug, and subject.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx := clientAndContext(f)
			cat, err := c.Catalog(ctx)
			if err != nil {
				return err
			}
			books := openstax.SearchBooks(cat.Books, args[0], subject, includeRetired)
			if f.agent {
				start, end, meta, err := agentWindow(len(books), offset, limit, 5, 100)
				if err != nil {
					return err
				}
				return writeAgentSuccess(cmd.OutOrStdout(), "search", summarizeBooks(books[start:end]), &meta)
			}
			if f.asJSON {
				return openstax.WriteJSON(cmd.OutOrStdout(), books)
			}
			return writeBooksTable(cmd.OutOrStdout(), books)
		},
	}
	cmd.Flags().StringVar(&subject, "subject", "", "Filter by subject or subject category")
	cmd.Flags().BoolVar(&includeRetired, "include-retired", false, "Include retired books")
	cmd.Flags().IntVar(&limit, "limit", 0, "Agent result limit (default 5, maximum 100)")
	cmd.Flags().IntVar(&offset, "offset", 0, "Agent result offset")
	return cmd
}

func infoCmd(f *flags) *cobra.Command {
	return &cobra.Command{
		Use:   "info BOOK",
		Short: "Show catalog and archive metadata for a textbook.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx := clientAndContext(f)
			book, err := c.ResolveBook(ctx, args[0])
			if err != nil {
				return err
			}
			archive, err := c.ArchiveBook(ctx, book)
			if err != nil {
				return err
			}
			out := map[string]any{"catalog": book, "archive": archive}
			if f.agent {
				return writeAgentSuccess(cmd.OutOrStdout(), "info", summarizeBook(book, archive), nil)
			}
			if f.asJSON {
				return openstax.WriteJSON(cmd.OutOrStdout(), out)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\nslug: %s\nstate: %s\nsubjects: %s\npdf: %s\narchive: %s\nlicense: %s\npages: %d\n",
				book.Title, openstax.BookSlug(book.Slug), book.BookState, strings.Join(book.Subjects, ", "), book.PDFURL, archive.ArchiveURL, archive.License.Name, len(openstax.FlattenPages(archive.Tree)))
			return err
		},
	}
}

func tocCmd(f *flags) *cobra.Command {
	var flat bool
	var limit int
	var offset int
	cmd := &cobra.Command{
		Use:   "toc BOOK",
		Short: "Print a textbook table of contents.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ctx := clientAndContext(f)
			book, err := c.ResolveBook(ctx, args[0])
			if err != nil {
				return err
			}
			archive, err := c.ArchiveBook(ctx, book)
			if err != nil {
				return err
			}
			if f.agent {
				pages := openstax.FlattenPages(archive.Tree)
				start, end, meta, err := agentWindow(len(pages), offset, limit, 20, 100)
				if err != nil {
					return err
				}
				return writeAgentSuccess(cmd.OutOrStdout(), "toc", summarizePages(pages[start:end]), &meta)
			}
			if f.asJSON {
				if flat {
					return openstax.WriteJSON(cmd.OutOrStdout(), openstax.FlattenPages(archive.Tree))
				}
				return openstax.WriteJSON(cmd.OutOrStdout(), archive.Tree)
			}
			if flat {
				for _, page := range openstax.FlattenPages(archive.Tree) {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", page.Slug, page.TextTitle, page.OpenStaxPageURL); err != nil {
						return err
					}
				}
				return nil
			}
			return writeTOC(cmd.OutOrStdout(), archive.Tree)
		},
	}
	cmd.Flags().BoolVar(&flat, "flat", false, "Print only extractable pages")
	cmd.Flags().IntVar(&limit, "limit", 0, "Agent page limit (default 20, maximum 100)")
	cmd.Flags().IntVar(&offset, "offset", 0, "Agent page offset")
	return cmd
}

func extractCmd(f *flags) *cobra.Command {
	var pageRef string
	var all bool
	var includeHTML bool
	var format string
	var limit int
	var offset int
	var maxChars int
	var startChar int
	cmd := &cobra.Command{
		Use:   "extract BOOK",
		Short: "Extract a page or an entire textbook as text, JSON, or HTML.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && pageRef != "" {
				return &agentInputError{Code: "conflicting_page_selection", Message: "--page and --all cannot be used together", Suggestion: "Choose one page with --page, or paginate all pages with --all."}
			}
			if all && startChar != 0 {
				return &agentInputError{Code: "conflicting_text_offset", Message: "--start-char and --all cannot be used together", Suggestion: "Continue one page at a time with --page PAGE --start-char N."}
			}
			if f.agent && includeHTML {
				return &agentInputError{Code: "unsupported_agent_option", Message: "--include-html is not available in agent mode", Suggestion: "Use bounded text in agent mode, or use --json without --agent for raw HTML."}
			}
			if f.agent && maxChars == 0 {
				maxChars = 12000
			}
			if f.agent && maxChars > 50000 {
				return &agentInputError{Code: "max_chars_too_large", Message: fmt.Sprintf("max-chars %d exceeds the agent maximum of 50000", maxChars), Suggestion: "Use --max-chars 50000 or less and follow next_start_char."}
			}
			c, ctx := clientAndContext(f)
			book, err := c.ResolveBook(ctx, args[0])
			if err != nil {
				return err
			}
			archive, err := c.ArchiveBook(ctx, book)
			if err != nil {
				return err
			}
			format = strings.ToLower(format)
			if f.asJSON && !f.agent && !cmd.Flags().Changed("format") {
				format = "json"
			}
			pages := openstax.FlattenPages(archive.Tree)
			var meta *agentMeta
			if all && f.agent {
				start, end, pageMeta, err := agentWindow(len(pages), offset, limit, 1, 5)
				if err != nil {
					return err
				}
				pages = pages[start:end]
				meta = &pageMeta
			} else if !all {
				if pageRef == "" {
					if len(pages) == 0 {
						return fmt.Errorf("book has no extractable pages")
					}
					pageRef = pages[0].Slug
				}
				page, ok := openstax.FindPage(archive.Tree, pageRef)
				if !ok {
					return fmt.Errorf("page %q not found in %s", pageRef, archive.Title)
				}
				pages = []openstax.TOCNode{page}
				if f.agent {
					_, _, pageMeta, _ := agentWindow(1, 0, 1, 1, 1)
					meta = &pageMeta
				}
			}
			extracted, err := extractPages(ctx, c, archive, pages, !f.agent && (includeHTML || format == "html"))
			if err != nil {
				return err
			}
			if f.agent {
				compact, err := compactExtractedPages(extracted, startChar, maxChars)
				if err != nil {
					return err
				}
				return writeAgentSuccess(cmd.OutOrStdout(), "extract", compact, meta)
			}
			return writeExtract(cmd.OutOrStdout(), extracted, format)
		},
	}
	cmd.Flags().StringVar(&pageRef, "page", "", "Page slug or page id; defaults to the first page unless --all is set")
	cmd.Flags().BoolVar(&all, "all", false, "Extract every page in table-of-contents order")
	cmd.Flags().BoolVar(&includeHTML, "include-html", false, "Include raw content HTML in JSON output")
	cmd.Flags().StringVar(&format, "format", "text", "Output format: text, json, html")
	cmd.Flags().IntVar(&limit, "limit", 0, "Agent page limit with --all (default 1, maximum 5)")
	cmd.Flags().IntVar(&offset, "offset", 0, "Agent page offset with --all")
	cmd.Flags().IntVar(&maxChars, "max-chars", 0, "Agent text limit per page (default 12000, maximum 50000)")
	cmd.Flags().IntVar(&startChar, "start-char", 0, "Agent text offset for continuation")
	return cmd
}

func downloadCmd(f *flags) *cobra.Command {
	var outPath string
	var kind string
	var pageRef string
	var all bool
	cmd := &cobra.Command{
		Use:   "download BOOK",
		Short: "Download a textbook PDF or save extracted content to a file.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.agent && outPath == "" {
				return &agentInputError{
					Code:       "output_required",
					Message:    "agent downloads require an explicit --output path",
					Suggestion: "Choose a writable file path and pass it with --output.",
				}
			}
			kind = strings.ToLower(kind)
			if kind != "pdf" && kind != "text" && kind != "json" && kind != "html" {
				return &agentInputError{Code: "invalid_download_kind", Message: fmt.Sprintf("unsupported download kind %q", kind), Suggestion: "Use pdf, text, json, or html."}
			}
			// --all defaults to true so that a bare download saves the whole book.
			// Honor --page when the caller did not ask for --all explicitly, rather
			// than silently downloading everything.
			if pageRef != "" {
				if cmd.Flags().Changed("all") && all {
					return &agentInputError{Code: "conflicting_page_selection", Message: "--page and --all cannot be used together", Suggestion: "Choose one page with --page, or omit --page to save the whole book."}
				}
				all = false
			}
			c, ctx := clientAndContext(f)
			book, err := c.ResolveBook(ctx, args[0])
			if err != nil {
				return err
			}
			slug := openstax.BookSlug(book.Slug)
			if outPath == "" {
				ext := map[string]string{"pdf": ".pdf", "text": ".txt", "json": ".json", "html": ".html"}[kind]
				if ext == "" {
					ext = ".txt"
				}
				outPath = slug + ext
			}
			if err := ensureParentDir(outPath); err != nil {
				return err
			}
			var written int64
			if kind == "pdf" {
				pdf := book.HighResolutionPDFURL
				if pdf == "" {
					pdf = book.PDFURL
				}
				if pdf == "" {
					return fmt.Errorf("%q has no PDF URL in the catalog", book.Title)
				}
				n, err := downloadToFile(ctx, c, pdf, outPath)
				if err != nil {
					return err
				}
				written = n
			} else {
				archive, err := c.ArchiveBook(ctx, book)
				if err != nil {
					return err
				}
				pages := openstax.FlattenPages(archive.Tree)
				if len(pages) == 0 {
					return fmt.Errorf("%q has no extractable pages", archive.Title)
				}
				if !all {
					if pageRef == "" {
						pageRef = pages[0].Slug
					}
					page, ok := openstax.FindPage(archive.Tree, pageRef)
					if !ok {
						return fmt.Errorf("page %q not found in %s", pageRef, archive.Title)
					}
					pages = []openstax.TOCNode{page}
				}
				extracted, err := extractPages(ctx, c, archive, pages, kind == "html")
				if err != nil {
					return err
				}
				var b strings.Builder
				if err := writeExtract(&b, extracted, kind); err != nil {
					return err
				}
				content := []byte(b.String())
				if err := os.WriteFile(outPath, content, 0644); err != nil {
					return err
				}
				written = int64(len(content))
			}
			absolutePath, err := filepath.Abs(outPath)
			if err != nil {
				return err
			}
			result := agentDownloadResult{Path: absolutePath, Kind: kind, Bytes: written}
			if f.agent {
				return writeAgentSuccess(cmd.OutOrStdout(), "download", result, nil)
			}
			if f.asJSON {
				return openstax.WriteJSON(cmd.OutOrStdout(), result)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d bytes)\n", outPath, written)
			return err
		},
	}
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "Output file path")
	cmd.Flags().StringVar(&kind, "kind", "pdf", "Download kind: pdf, text, json, html")
	cmd.Flags().StringVar(&pageRef, "page", "", "Page slug/id for extracted text/html/json")
	cmd.Flags().BoolVar(&all, "all", true, "Extract all pages for text/html/json")
	return cmd
}

func ensureParentDir(outPath string) error {
	dir := filepath.Dir(filepath.Clean(outPath))
	if dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}

// downloadToFile streams rawURL straight to outPath and removes the partial file
// if the transfer fails, so a timed-out download never leaves a truncated book.
func downloadToFile(ctx context.Context, c openstaxClient, rawURL, outPath string) (int64, error) {
	file, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	written, err := c.Download(ctx, rawURL, file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(outPath)
		return 0, err
	}
	return written, nil
}

func extractPages(ctx context.Context, c openstaxClient, archive openstax.ArchiveBook, pages []openstax.TOCNode, includeHTML bool) ([]openstax.ExtractedPage, error) {
	var out []openstax.ExtractedPage
	for _, page := range pages {
		body, pageURL, err := c.PageXHTML(ctx, archive, page)
		if err != nil {
			return nil, err
		}
		extracted, err := openstax.ExtractPage(archive, page, pageURL, body, includeHTML)
		if err != nil {
			return nil, err
		}
		out = append(out, extracted)
	}
	return out, nil
}

func writeExtract(w io.Writer, pages []openstax.ExtractedPage, format string) error {
	switch strings.ToLower(format) {
	case "json":
		return openstax.WriteJSON(w, pages)
	case "html":
		for _, p := range pages {
			if _, err := fmt.Fprintf(w, "<!-- %s | %s -->\n%s\n\n", p.PageTitle, p.OpenStaxPageURL, p.HTML); err != nil {
				return err
			}
		}
	case "text", "":
		for i, p := range pages {
			if i > 0 {
				if _, err := fmt.Fprintln(w, "\n\n---"); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(w, "# %s\n%s\n\n%s\n", p.PageTitle, p.OpenStaxPageURL, p.Text); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported format %q (use text, json, html)", format)
	}
	return nil
}

func writeBooksTable(w io.Writer, books []openstax.Book) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "TITLE\tSLUG\tSTATE\tSUBJECTS"); err != nil {
		return err
	}
	for _, b := range books {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", b.Title, openstax.BookSlug(b.Slug), b.BookState, strings.Join(b.Subjects, ", ")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeTOC(w io.Writer, node openstax.TOCNode) error {
	if node.TextTitle != "" {
		if _, err := fmt.Fprintf(w, "%s%s", strings.Repeat("  ", node.Depth), node.TextTitle); err != nil {
			return err
		}
		if node.Slug != "" {
			if _, err := fmt.Fprintf(w, "  [%s]", node.Slug); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	for _, child := range node.Contents {
		if err := writeTOC(w, child); err != nil {
			return err
		}
	}
	return nil
}
