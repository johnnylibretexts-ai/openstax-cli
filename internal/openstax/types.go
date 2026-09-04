package openstax

type CatalogResponse struct {
	Title string `json:"title"`
	Books []Book `json:"books"`
}

type Book struct {
	ID                   int      `json:"id"`
	Slug                 string   `json:"slug"`
	BookState            string   `json:"book_state"`
	Title                string   `json:"title"`
	Subjects             []string `json:"subjects"`
	SubjectCategories    []string `json:"subject_categories"`
	K12Subject           []string `json:"k12subject"`
	CoverURL             string   `json:"cover_url"`
	PDFURL               string   `json:"pdf_url"`
	HighResolutionPDFURL string   `json:"high_resolution_pdf_url"`
	WebviewLink          string   `json:"webview_link"`
	WebviewRexLink       string   `json:"webview_rex_link"`
	BookshareLink        string   `json:"bookshare_link"`
	AmazonLink           string   `json:"amazon_link"`
	LastUpdatedPDF       string   `json:"last_updated_pdf"`
}

type ArchiveBook struct {
	Title          string  `json:"title"`
	Revised        string  `json:"revised"`
	Tree           TOCNode `json:"tree"`
	Slug           string  `json:"slug"`
	ID             string  `json:"id"`
	Version        string  `json:"version"`
	License        License `json:"license"`
	Language       string  `json:"language"`
	Content        string  `json:"content"`
	RepoSchema     int     `json:"repo_schema_version"`
	StyleName      string  `json:"style_name"`
	StyleHref      string  `json:"style_href"`
	ArchiveVersion string  `json:"archive_version,omitempty"`
	ArchiveURL     string  `json:"archive_url,omitempty"`
}

type License struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

type TOCNode struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	TOCType         string    `json:"toc_type,omitempty"`
	TOCTargetType   string    `json:"toc_target_type,omitempty"`
	Slug            string    `json:"slug,omitempty"`
	Contents        []TOCNode `json:"contents,omitempty"`
	DocumentType    string    `json:"documentType,omitempty"`
	ArchivePageURL  string    `json:"archive_page_url,omitempty"`
	OpenStaxPageURL string    `json:"openstax_page_url,omitempty"`
	TextTitle       string    `json:"text_title,omitempty"`
	Depth           int       `json:"depth,omitempty"`
}

type ExtractedPage struct {
	BookTitle       string  `json:"book_title"`
	BookSlug        string  `json:"book_slug"`
	PageTitle       string  `json:"page_title"`
	PageSlug        string  `json:"page_slug"`
	PageID          string  `json:"page_id"`
	OpenStaxPageURL string  `json:"openstax_page_url"`
	ArchivePageURL  string  `json:"archive_page_url"`
	Text            string  `json:"text"`
	HTML            string  `json:"html,omitempty"`
	Images          []Image `json:"images,omitempty"`
}

type Image struct {
	Src string `json:"src"`
	Alt string `json:"alt,omitempty"`
}
