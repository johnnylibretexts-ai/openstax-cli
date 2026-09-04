package openstax

import (
	"strings"
	"testing"
)

func TestBookSlug(t *testing.T) {
	tests := map[string]string{
		"books/principles-data-science":                                    "principles-data-science",
		"https://openstax.org/books/principles-data-science/pages/preface": "principles-data-science",
		"https://openstax.org/details/books/principles-data-science":       "principles-data-science",
		"principles-data-science":                                          "principles-data-science",
	}
	for in, want := range tests {
		if got := BookSlug(in); got != want {
			t.Fatalf("BookSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanHTMLTitle(t *testing.T) {
	got := CleanHTMLTitle(`<span class="os-number">1.3</span><span class="os-divider"> </span><span class="os-text">Data and Datasets</span>`)
	if got != "1.3 Data and Datasets" {
		t.Fatalf("unexpected title %q", got)
	}
}

func TestFlattenPagesOnlyReturnsBookContentLeaves(t *testing.T) {
	root := TOCNode{
		Slug:    "whole-book",
		TOCType: "book",
		Contents: []TOCNode{
			{Slug: "preface", TOCType: "book-content"},
			{
				Slug:    "chapter-1",
				TOCType: "chapter",
				Contents: []TOCNode{
					{Slug: "section-1", TOCType: "book-content"},
				},
			},
		},
	}

	pages := FlattenPages(root)
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2: %#v", len(pages), pages)
	}
	if pages[0].Slug != "preface" || pages[1].Slug != "section-1" {
		t.Fatalf("unexpected extractable pages: %#v", pages)
	}
}

func TestExtractPage(t *testing.T) {
	book := ArchiveBook{Title: "Book", Slug: "book", ID: "book@v", ArchiveURL: "https://example.test/archive"}
	page := TOCNode{ID: "page@", Slug: "chapter-1", Title: `<span>Chapter 1</span>`, OpenStaxPageURL: "https://example.test/page"}
	body := []byte(`<html><body><div data-book-content="true"><h2>Chapter 1</h2><p>Hello <strong>world</strong>.</p><img src="../resources/x.png" alt="diagram"/></div></body></html>`)
	pageURL := "https://example.test/apps/archive/release/contents/book@v:page.xhtml"
	got, err := ExtractPage(book, page, pageURL, body, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "Hello world") {
		t.Fatalf("missing extracted text: %q", got.Text)
	}
	wantImageURL := "https://example.test/apps/archive/release/resources/x.png"
	if len(got.Images) != 1 || got.Images[0].Src != wantImageURL {
		t.Fatalf("missing image extraction: %#v", got.Images)
	}
	if !strings.Contains(got.HTML, `src="`+wantImageURL+`"`) {
		t.Fatalf("HTML did not contain absolute image URL: %s", got.HTML)
	}
}
