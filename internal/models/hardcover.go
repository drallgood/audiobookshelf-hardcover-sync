package models

// HardcoverBook represents a book in the Hardcover API
type HardcoverBook struct {
	ID            string   `json:"id"`
	UserBookID    string   `json:"userBookId,omitempty"`
	EditionID     string   `json:"editionId,omitempty"`
	Title         string   `json:"title"`
	Subtitle      string   `json:"subtitle,omitempty"`
	Authors       []Author `json:"authors,omitempty"`
	Narrators     []Author `json:"narrators,omitempty"`
	CoverImageURL string   `json:"coverImageUrl,omitempty"`
	Description   string   `json:"description,omitempty"`
	PageCount     int      `json:"pageCount,omitempty"`
	ReleaseDate   string   `json:"releaseDate,omitempty"`
	Publisher     string   `json:"publisher,omitempty"`
	ISBN          string   `json:"isbn,omitempty"`
	ASIN          string   `json:"asin,omitempty"`
	// Additional fields from GraphQL response
	BookStatusID int  `json:"book_status_id"`
	CanonicalID  *int `json:"canonical_id,omitempty"`
	// Fields for edition information
	EditionASIN   string `json:"edition_asin,omitempty"`
	EditionISBN13 string `json:"edition_isbn_13,omitempty"`
	EditionISBN10 string `json:"edition_isbn_10,omitempty"`
}

// Author represents an author or narrator in the Hardcover API
type Author struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BookCount int    `json:"bookCount,omitempty"`
}

// ReadingProgress represents a user's reading progress in Hardcover
type ReadingProgress struct {
	BookID    string  `json:"bookId"`
	Progress  float64 `json:"progress"` // 0.0 to 1.0
	Status    string  `json:"status"`   // "WANT_TO_READ", "READING", "FINISHED"
	Owned     bool    `json:"owned"`
	UpdatedAt string  `json:"updatedAt,omitempty"`
}

// SearchResponse represents the response from the Hardcover search API
type SearchResponse struct {
	Results []SearchResult `json:"results"`
	Total   int            `json:"total"`
}

// SearchResult represents a single search result from the Hardcover API
type SearchResult struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"` // "book", "author", etc.
	Image string `json:"image,omitempty"`
}

// Edition represents a book edition in the Hardcover API
type Edition struct {
	ID          string `json:"id"`
	BookID      string `json:"book_id"`
	Title       string `json:"title,omitempty"`
	ISBN10      string `json:"isbn_10,omitempty"`
	ISBN13      string `json:"isbn_13,omitempty"`
	ASIN        string `json:"asin,omitempty"`
	ReleaseDate string `json:"release_date,omitempty"`
}

// Publisher represents a publisher in the Hardcover API
type Publisher struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
