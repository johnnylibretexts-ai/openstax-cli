package openstax

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"

	xhtml "golang.org/x/net/html"
)

var stateRE = regexp.MustCompile(`(?s)window\.__PRELOADED_STATE__\s*=\s*(\{.*?\})\s*</script>`)

func ExtractPreloadedState(page []byte) (map[string]any, error) {
	m := stateRE.FindSubmatch(page)
	if len(m) != 2 {
		return nil, fmt.Errorf("OpenStax preloaded state not found")
	}
	var state map[string]any
	if err := json.Unmarshal(m[1], &state); err != nil {
		return nil, err
	}
	return state, nil
}

func BookSlug(ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, "://") {
		if u, err := url.Parse(ref); err == nil {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			for i := 0; i+1 < len(parts); i++ {
				if parts[i] == "books" {
					return parts[i+1]
				}
				if parts[i] == "details" && i+2 < len(parts) && parts[i+1] == "books" {
					return parts[i+2]
				}
			}
		}
	}
	ref = strings.TrimPrefix(ref, "books/")
	ref = strings.TrimPrefix(ref, "details/books/")
	if idx := strings.Index(ref, "/"); idx >= 0 {
		ref = ref[:idx]
	}
	return strings.ToLower(strings.Trim(ref, "/ "))
}

func PageSlug(ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, "://") {
		if u, err := url.Parse(ref); err == nil {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			for i := 0; i+1 < len(parts); i++ {
				if parts[i] == "pages" {
					return parts[i+1]
				}
			}
		}
	}
	ref = strings.TrimPrefix(ref, "pages/")
	if idx := strings.Index(ref, "/"); idx >= 0 {
		ref = ref[:idx]
	}
	return strings.ToLower(strings.Trim(ref, "/ "))
}

func PageID(id string) string {
	id = strings.TrimSpace(id)
	if idx := strings.Index(id, "@"); idx >= 0 {
		id = id[:idx]
	}
	return id
}

func CleanHTMLTitle(raw string) string {
	node, err := xhtml.Parse(strings.NewReader(raw))
	if err != nil {
		return strings.Join(strings.Fields(html.UnescapeString(raw)), " ")
	}
	return strings.Join(strings.Fields(textOf(node)), " ")
}

func FlattenPages(root TOCNode) []TOCNode {
	var pages []TOCNode
	var walk func(TOCNode, int)
	walk = func(n TOCNode, depth int) {
		n.Depth = depth
		n.TextTitle = CleanHTMLTitle(n.Title)
		if n.Slug != "" && n.TOCType == "book-content" && len(n.Contents) == 0 {
			pages = append(pages, n)
		}
		for _, child := range n.Contents {
			walk(child, depth+1)
		}
	}
	walk(root, 0)
	return pages
}

func DecorateTree(n *TOCNode, book ArchiveBook, depth int) {
	n.Depth = depth
	n.TextTitle = CleanHTMLTitle(n.Title)
	if n.Slug != "" {
		n.OpenStaxPageURL = BaseURL + "/books/" + book.Slug + "/pages/" + n.Slug
		n.ArchivePageURL = fmt.Sprintf("%s/contents/%s@%s:%s.xhtml", book.ArchiveURL, book.ID, url.PathEscape(book.Version), PageID(n.ID))
	}
	for i := range n.Contents {
		DecorateTree(&n.Contents[i], book, depth+1)
	}
}

func FindPage(root TOCNode, ref string) (TOCNode, bool) {
	wantSlug := PageSlug(ref)
	wantID := PageID(ref)
	for _, page := range FlattenPages(root) {
		if strings.EqualFold(page.Slug, wantSlug) || PageID(page.ID) == wantID {
			return page, true
		}
	}
	return TOCNode{}, false
}

func ExtractPage(book ArchiveBook, page TOCNode, pageURL string, body []byte, includeHTML bool) (ExtractedPage, error) {
	doc, err := xhtml.Parse(bytes.NewReader(body))
	if err != nil {
		return ExtractedPage{}, err
	}
	content := findContentNode(doc)
	if content == nil {
		content = doc
	}
	absolutizeResourceURLs(content, pageURL)
	var htmlBody string
	if includeHTML {
		var b bytes.Buffer
		_ = xhtml.Render(&b, content)
		htmlBody = b.String()
	}
	return ExtractedPage{
		BookTitle:       book.Title,
		BookSlug:        book.Slug,
		PageTitle:       CleanHTMLTitle(page.Title),
		PageSlug:        page.Slug,
		PageID:          PageID(page.ID),
		OpenStaxPageURL: page.OpenStaxPageURL,
		ArchivePageURL:  pageURL,
		Text:            NormalizeText(textOf(content)),
		HTML:            htmlBody,
		Images:          collectImages(content),
	}, nil
}

func absolutizeResourceURLs(n *xhtml.Node, pageURL string) {
	base, err := url.Parse(pageURL)
	if err != nil {
		return
	}
	var walk func(*xhtml.Node)
	walk = func(cur *xhtml.Node) {
		if cur.Type == xhtml.ElementNode {
			for i := range cur.Attr {
				a := &cur.Attr[i]
				if a.Key != "src" && a.Key != "href" && a.Key != "poster" {
					continue
				}
				ref, err := url.Parse(a.Val)
				if err == nil {
					a.Val = base.ResolveReference(ref).String()
				}
			}
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
}

func findContentNode(n *xhtml.Node) *xhtml.Node {
	if n.Type == xhtml.ElementNode {
		for _, a := range n.Attr {
			if a.Key == "data-book-content" && a.Val == "true" {
				return n
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findContentNode(c); found != nil {
			return found
		}
	}
	return nil
}

func textOf(n *xhtml.Node) string {
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(cur *xhtml.Node) {
		if cur.Type == xhtml.ElementNode {
			switch cur.Data {
			case "script", "style", "svg":
				return
			case "h1", "h2", "h3", "h4", "h5", "h6", "p", "div", "section", "table", "tr", "ul", "ol", "li", "blockquote", "figure":
				b.WriteString("\n")
			case "br":
				b.WriteString("\n")
			}
		}
		if cur.Type == xhtml.TextNode {
			b.WriteString(cur.Data)
			b.WriteString(" ")
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if cur.Type == xhtml.ElementNode {
			switch cur.Data {
			case "h1", "h2", "h3", "h4", "h5", "h6", "p", "div", "section", "table", "tr", "ul", "ol", "li", "blockquote", "figure":
				b.WriteString("\n")
			}
		}
	}
	walk(n)
	return b.String()
}

func NormalizeText(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(html.UnescapeString(line)), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n\n")
}

func collectImages(n *xhtml.Node) []Image {
	seen := map[string]bool{}
	var images []Image
	var walk func(*xhtml.Node)
	walk = func(cur *xhtml.Node) {
		if cur.Type == xhtml.ElementNode && cur.Data == "img" {
			var img Image
			for _, a := range cur.Attr {
				switch a.Key {
				case "src":
					img.Src = a.Val
				case "alt":
					img.Alt = a.Val
				}
			}
			if img.Src != "" && !seen[img.Src] {
				seen[img.Src] = true
				images = append(images, img)
			}
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return images
}

func SearchBooks(books []Book, query, subject string, includeRetired bool) []Book {
	query = strings.ToLower(strings.TrimSpace(query))
	subject = strings.ToLower(strings.TrimSpace(subject))
	var out []Book
	for _, b := range books {
		if !includeRetired && b.BookState == "retired" {
			continue
		}
		if subject != "" && !containsFold(append(append([]string{}, b.Subjects...), b.SubjectCategories...), subject) {
			continue
		}
		hay := strings.ToLower(strings.Join(append([]string{b.Title, b.Slug, b.BookState}, append(append([]string{}, b.Subjects...), b.SubjectCategories...)...), " "))
		if query == "" || strings.Contains(hay, query) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

func containsFold(values []string, needle string) bool {
	for _, v := range values {
		if strings.Contains(strings.ToLower(v), needle) {
			return true
		}
	}
	return false
}

func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
