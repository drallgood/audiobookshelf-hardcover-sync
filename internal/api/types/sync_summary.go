package types

import (
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
)

// SyncSummaryResponse represents the sync summary data returned by the API
type SyncSummaryResponse struct {
	TotalBooksProcessed int32                `json:"total_books_processed"`
	BooksNotFound       []BookNotFoundInfo   `json:"books_not_found"`
	Mismatches          []mismatch.BookMismatch `json:"mismatches"`
}

// BookNotFoundInfo represents a book that couldn't be found in Hardcover
type BookNotFoundInfo struct {
	BookID string `json:"book_id"`
	Title  string `json:"title"`
	Author string `json:"author"`
	ASIN   string `json:"asin,omitempty"`
	ISBN   string `json:"isbn,omitempty"`
	Error  string `json:"error"`
}
