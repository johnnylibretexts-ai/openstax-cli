# OpenStax CLI

Search, access, download, and extract OpenStax textbook content from the terminal.

This is a CLI Printing Press `*-pp-cli` tool. Its API spec is accepted by CLI Printing Press and the project includes the generated-style build and tool manifests. It uses the public OpenStax catalog endpoint and the same versioned archive data consumed by the web reader. The linked `openstax/openstax_api` project is a Ruby API helper; this CLI keeps that API origin clear while using the live public catalog/archive surfaces exposed by OpenStax.

## Install

```bash
make build
./bin/openstax-pp-cli --version
```

Install the released binary directly:

```bash
go install github.com/johnnylibretexts/openstax-cli/cmd/openstax-pp-cli@latest
```

## Quick Start

```bash
openstax-pp-cli doctor
openstax-pp-cli search biology
openstax-pp-cli info principles-data-science
openstax-pp-cli toc principles-data-science --flat
openstax-pp-cli extract principles-data-science --page 1-3-data-and-datasets
openstax-pp-cli extract principles-data-science --all --format json --include-html > principles-data-science.json
openstax-pp-cli download principles-data-science --kind pdf -o principles-data-science.pdf
openstax-pp-cli download principles-data-science --kind text --all -o principles-data-science.txt
openstax-pp-cli download principles-data-science --kind text --page 1-3-data-and-datasets -o one-page.txt
```

`download` saves the whole book by default. Passing `--page` narrows it to a single
page; passing both `--page` and an explicit `--all` is an error. PDFs are streamed
straight to disk rather than buffered, so a 260 MB textbook does not have to fit in
memory, and `--timeout` bounds the response headers rather than the transfer itself.
A download that fails partway through removes its partial file.

## Commands

- `schema` describes the compact, versioned agent contract without a network request.
- `doctor` checks live catalog connectivity.
- `books` lists catalog textbooks.
- `search QUERY` searches titles, slugs, subjects, and categories.
- `info BOOK` shows catalog metadata and archive metadata.
- `toc BOOK` prints the table of contents; add `--flat` for only extractable pages.
- `extract BOOK` extracts one page or the whole book as `text`, `json`, or `html`.
- `download BOOK` saves a catalog PDF or extracted textbook content.

Whole-book extraction walks only leaf nodes marked by OpenStax as `book-content`, preserving table, example, exercise, figure-caption, and other readable text without duplicating unit and chapter containers. Extracted resource links are converted to absolute OpenStax URLs so saved JSON and HTML can still access images.

`BOOK` can be a slug such as `principles-data-science`, a catalog path such as `books/principles-data-science`, or a full OpenStax book/page URL.

## Agent Mode

`--json` preserves the complete upstream-shaped JSON intended for scripts and inspection. `--agent` uses a smaller, versioned contract intended for fast, lower-capability tool-using models.

Discover the contract without making a network request:

```bash
openstax-pp-cli schema --agent
```

Agent responses are one compact JSON object with `schema_version`, `ok`, and either `data` or `error`. Lists include pagination metadata. Follow `next_offset` when `has_more` is true:

```bash
openstax-pp-cli search "data science" --limit 5 --agent
openstax-pp-cli toc principles-data-science --limit 20 --agent
openstax-pp-cli toc principles-data-science --limit 20 --offset 20 --agent
```

Page text is bounded to 12,000 characters by default. Follow `next_start_char` to continue the same page:

```bash
openstax-pp-cli extract principles-data-science --page 1-3-data-and-datasets --agent
openstax-pp-cli extract principles-data-science --page 1-3-data-and-datasets --start-char 12000 --agent
```

`next_start_char` is a cursor into one page, so `--start-char` cannot be combined
with `--all`; paginate whole-book extraction with `--offset` instead.

`extract --all --agent` returns one page by default and supports `--limit` up to 5. Agent mode intentionally excludes raw HTML; use `--json --include-html` when complete HTML is required.

Agent errors are written to stdout alongside successful responses, so a harness that captures only stdout still receives a structured failure; the exit status is still nonzero. Errors use stable codes such as `book_not_found`, `page_not_found`, and `invalid_arguments`, plus a suggested recovery action. Because `download` writes to the filesystem, agent mode requires an explicit `--output` path and returns the path, kind, and byte count after writing.

## Source Notes

- Catalog: `https://openstax.org/apps/cms/api/books/?format=json`
- Web reader pages: `https://openstax.org/books/{book}/pages/{page}`
- Archive: discovered from the reader's `window.__PRELOADED_STATE__`, then fetched from `/apps/archive/{archiveVersion}/contents/{bookID}@{contentVersion}.json` and versioned page `.xhtml` files.
- Import behavior was cross-checked against the OpenStax importer in [`johnnylibretexts/libretexts-reader`](https://github.com/johnnylibretexts/libretexts-reader), while this CLI deliberately retains tables, examples, exercises, and other source content that reader streamlines for narration.

## Contributing

CI runs `gofmt`, `go vet`, `go build`, `go test -race`, and `golangci-lint` on every
push and pull request. Reproduce it locally with `make test` and `make lint`.

## License

This CLI is released under the [MIT License](LICENSE).

OpenStax textbook content retrieved by this tool is **not** MIT licensed. Each book
carries its own Creative Commons license — for example, College Physics 2e is
CC BY-NC-SA. `info` returns the applicable license name and URL for every book;
preserve that license and its attribution when you reuse extracted content.
