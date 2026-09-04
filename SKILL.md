---
name: pp-openstax
description: Search OpenStax textbooks and retrieve a book's metadata, table of contents, PDF, page content, or complete extracted text, JSON, and HTML using openstax-pp-cli or openstax-cli.
---

# OpenStax Printing Press CLI

Use `openstax-pp-cli` (or `openstax-cli`) for read-only access to the public OpenStax catalog and textbook archive. It requires no account or API key.

## Setup

Verify the binary before use:

```bash
openstax-pp-cli --version
openstax-pp-cli schema --agent
openstax-pp-cli doctor --json
```

From this repository, build with `make build` or install with `make install`.

## Common Workflows

Search by title, slug, subject, or category:

```bash
openstax-pp-cli search "data science" --limit 5 --agent
openstax-pp-cli books --subject biology --limit 10 --agent
```

Inspect a book and its extractable pages:

```bash
openstax-pp-cli info principles-data-science --agent
openstax-pp-cli toc principles-data-science --limit 20 --agent
```

Extract one page or every content page:

```bash
openstax-pp-cli extract principles-data-science --page 1-3-data-and-datasets --agent
openstax-pp-cli extract principles-data-science --page 1-3-data-and-datasets --start-char 12000 --agent
openstax-pp-cli extract principles-data-science --all --limit 1 --offset 0 --agent
```

Save a complete book or its publisher PDF:

```bash
openstax-pp-cli download principles-data-science --kind text -o principles-data-science.txt --agent
openstax-pp-cli download principles-data-science --kind html -o principles-data-science.html --agent
openstax-pp-cli download principles-data-science --kind pdf -o principles-data-science.pdf --agent
```

`BOOK` accepts a slug, an OpenStax catalog path, or a full book/page URL. Extracted image and link targets are absolute OpenStax URLs. Preserve the license and attribution returned by `info` when reusing textbook content.

## Agent Contract

Use `--agent` for a single compact JSON envelope. Check `ok` before reading `data`. When list metadata contains `has_more: true`, repeat the command with `next_offset`. When extracted text contains `next_start_char`, repeat the same page request with that value as `--start-char`.

Agent defaults are intentionally bounded: search returns 5 books, books returns 10, TOC returns 20 pages, whole-book extraction returns 1 page, and each extracted page returns at most 12,000 characters. Reuse returned book and page slugs exactly.

Do not combine `--start-char` with `--all`; it is a cursor into a single page, so paginate whole-book extraction with `--offset` and continue individual pages with `--page PAGE --start-char N`. Do not combine `--agent` with `--include-html`. Use raw `--json` when complete upstream records or HTML are required. `download --agent` requires an explicit `--output` because it writes a file.

Errors are compact JSON on stderr, return a nonzero exit status, and include `code`, `message`, `suggestion`, and `retryable`.
