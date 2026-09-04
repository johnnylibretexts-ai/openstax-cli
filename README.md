# OpenStax CLI

[![CI](https://github.com/johnnylibretexts/openstax-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/johnnylibretexts/openstax-cli/actions/workflows/ci.yml)

Read OpenStax textbooks from the command line: search the catalog, walk a book's
table of contents, pull the full text of any page, or save an entire book as
text, JSON, HTML, or PDF.

[OpenStax](https://openstax.org) is a nonprofit publisher at Rice University that
produces free, peer-reviewed, openly licensed college textbooks. The catalog
holds 110 books — 76 of them currently in print — across Business, College
Success, Computer Science, Humanities, Math, Nursing, Science, and Social
Sciences. All of it is public, so **this tool needs no account, API key, or
token.**

It exists because that content is awkward to reach programmatically. The catalog
is a single JSON endpoint, but a book's actual text lives in a versioned archive
whose address is only discoverable from the state embedded in the web reader's
HTML. This CLI does that discovery for you and returns readable text instead of
page markup — and ships a compact, bounded JSON contract (`--agent`) built for
LLM tool use.

## Requirements

Go 1.26 or newer, and network access to `openstax.org`. No credentials.

## Install

```bash
go install github.com/johnnylibretexts/openstax-cli/cmd/openstax-pp-cli@latest
```

Or build from a clone:

```bash
make build && ./bin/openstax-pp-cli --version
```

The binary is named `openstax-pp-cli`. `make install` also links it as
`openstax-cli`; both names run the same tool. The `-pp-` marks it as a CLI
Printing Press tool — see [Project notes](#project-notes).

## Try it

Find a book:

```console
$ openstax-pp-cli search "data science"
TITLE                       SLUG                     STATE  SUBJECTS
Principles of Data Science  principles-data-science  live   Computer Science, Business, Math
```

Inspect it — note that `license` is the book's license, not this tool's:

```console
$ openstax-pp-cli info principles-data-science
Principles of Data Science
slug: principles-data-science
state: live
subjects: Computer Science, Business, Math
pdf: https://assets.openstax.org/oscms-prodcms/media/documents/Principles-of-Data-Science-WEB.pdf
archive: https://openstax.org/apps/archive/20260604.144757
license: Creative Commons Attribution-NonCommercial-ShareAlike License
pages: 112
```

List the pages you can extract:

```console
$ openstax-pp-cli toc principles-data-science --flat | head -3
preface	Preface	https://openstax.org/books/principles-data-science/pages/preface
1-introduction	Introduction	https://openstax.org/books/principles-data-science/pages/1-introduction
1-1-what-is-data-science	1.1 What Is Data Science?	https://openstax.org/books/principles-data-science/pages/1-1-what-is-data-science
```

Read one:

```console
$ openstax-pp-cli extract principles-data-science --page 1-3-data-and-datasets | head -6
# 1.3 Data and Datasets
https://openstax.org/books/principles-data-science/pages/1-3-data-and-datasets

1.3

Data and Datasets
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

Retired books are hidden unless you pass `--include-retired`.

The book catalog is cached for 24 hours under your user cache directory, so most
commands skip a 338 KB download. Pass `--no-cache` to force a fresh copy. `doctor`
always ignores the cache, since its job is to prove the catalog is reachable.

Whole-book extraction walks only leaf nodes marked by OpenStax as `book-content`, preserving table, example, exercise, figure-caption, and other readable text without duplicating unit and chapter containers. Extracted resource links are converted to absolute OpenStax URLs so saved JSON and HTML can still access images.

`BOOK` can be a slug such as `principles-data-science`, a catalog path such as `books/principles-data-science`, or a full OpenStax book/page URL.

## Agent Mode

`--json` preserves the complete upstream-shaped JSON intended for scripts and inspection. `--agent` uses a smaller, versioned contract intended for fast, lower-capability tool-using models.

Every agent response is a single line, so a tool harness can read one line and parse it:

```console
$ openstax-pp-cli search "data science" --limit 3 --agent
{"schema_version":"1","ok":true,"data":[{"slug":"principles-data-science","title":"Principles of Data Science","state":"live","subjects":["Computer Science","Business","Math"]}],"meta":{"command":"search","count":1,"offset":0,"limit":3,"total":1,"has_more":false}}
```

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

`SKILL.md` packages all of this as an agent skill.

## Project notes

This is a CLI Printing Press `*-pp-cli` tool: `spec.yaml` is the API spec accepted
by CLI Printing Press, and `tools-manifest.json` is the generated-style tool
manifest. The upstream `openstax/openstax_api` project referenced in the User-Agent
is a Ruby API helper; this CLI names it to keep the API origin clear while reading
the live public catalog and archive surfaces.

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
