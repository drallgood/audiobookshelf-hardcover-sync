package mismatch

import "time"

// BookMismatch represents a book that couldn't be properly synced with Hardcover
type BookMismatch struct {
	Title           string    `json:"title"`
	Subtitle       string    `json:"subtitle,omitempty"`
	Author         string    `json:"author"`
	Narrator       string    `json:"narrator,omitempty"`
	Publisher      string    `json:"publisher,omitempty"`
	PublishedYear  string    `json:"published_year,omitempty"`
	ReleaseDate    string    `json:"release_date,omitempty"`
	Duration       float64   `json:"duration"`           // Duration in hours for display
	DurationSeconds int      `json:"duration_seconds"`   // Duration in seconds for calculations
	ISBN           string    `json:"isbn,omitempty"`
	ASIN           string    `json:"asin,omitempty"`
	BookID         string    `json:"book_id,omitempty"`
	EditionID      string    `json:"edition_id,omitempty"`
	AudiobookShelfID string  `json:"audiobookshelf_id,omitempty"`
	Reason         string    `json:"reason"`
	Timestamp      int64     `json:"timestamp"`
	Attempts       int       `json:"attempts,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}
