package mismatch

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// EditionCreatorInput represents the input format expected by the edition import tool
type EditionCreatorInput struct {
	// Core book information
	BookID   int    `json:"book_id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`

	// Identifiers
	ASIN   string `json:"asin,omitempty"`
	ISBN10 string `json:"isbn_10,omitempty"`
	ISBN13 string `json:"isbn_13,omitempty"`

	// Media information
	ImageURL    string `json:"image_url,omitempty"`
	AudioLength int    `json:"audio_length,omitempty"`
	LanguageID  int    `json:"language_id,omitempty"`

	// Relationships
	AuthorIDs   []int `json:"author_ids,omitempty"`
	NarratorIDs []int `json:"narrator_ids,omitempty"`
	PublisherID int   `json:"publisher_id,omitempty"`
	CountryID   int   `json:"country_id,omitempty"`

	// Edition information
	ReleaseDate   string `json:"release_date"`
	EditionFormat string `json:"edition_format,omitempty"`
	EditionInfo   string `json:"edition_information,omitempty"`

	// User notes (not imported, for reference only)
	UserNotes string `json:"user_notes,omitempty"`
}

// ToEditionExport converts a BookMismatch to an EditionExport for the edition import tool
// Note: This function should be called with a context that has a Hardcover client available
func (b *BookMismatch) ToEditionExport(ctx context.Context, hc hardcover.HardcoverClientInterface) *EditionExport {
	// Get logger from context
	logger := logger.FromContext(ctx)

	// Parse Hardcover book ID (use 0 if not available)
	// We intentionally do NOT fall back to BookID here to avoid importing
	// with incorrect IDs when no Hardcover book ID is known.
	bookID := 0
	if b.HardcoverBookID != "" {
		if id, err := strconv.Atoi(b.HardcoverBookID); err == nil {
			bookID = id
		}
	}

	// Log the IDs for debugging
	logger.Debug("Converting BookMismatch to EditionExport", map[string]interface{}{
		"hardcover_book_id": b.HardcoverBookID,
		"fallback_book_id":  b.BookID,
		"parsed_book_id":    bookID,
		"title":             b.Title,
	})

	// Set edition format based on publisher if possible
	editionFormat := b.EditionFormat
	if editionFormat == "" || editionFormat == "Audiobook" {
		// Try to determine a more specific format based on publisher
		if b.Publisher != "" {
			publisher := strings.ToLower(b.Publisher)

			switch {
			case strings.Contains(publisher, "audible") ||
				strings.Contains(publisher, "brilliance") ||
				strings.Contains(publisher, "amazon"):
				editionFormat = "Audible Audio"
			case strings.Contains(publisher, "libro"):
				editionFormat = "libro.fm"
			case strings.Contains(publisher, "audiobook"):
				editionFormat = "Audiobook"
			default:
				// If we can't determine, use "Audible Audio" as default for audiobooks
				editionFormat = "Audible Audio"
			}
		} else {
			// Default to Audible Audio if no publisher info
			editionFormat = "Audible Audio"
		}
	}

	// Set default language and country if not specified
	languageID := b.LanguageID
	if languageID == 0 {
		languageID = 1 // Default to English
	}

	countryID := b.CountryID
	if countryID == 0 {
		countryID = 1 // Default to US
	}

	// Use the provided publisher ID or default to 0
	publisherID := b.PublisherID

	// Prefer Hardcover cover if available for the exported image_url,
	// then fall back to explicit ImageURL, then CoverURL
	imageURL := b.HardcoverCoverURL
	if imageURL == "" {
		imageURL = b.ImageURL
	}
	if imageURL == "" {
		imageURL = b.CoverURL
	}

	// Set edition information to describe the edition (e.g., "Unabridged")
	// Default to empty string if we don't know
	editionInfo := ""

	// If EditionInfo is already set in the mismatch, check if it's valid
	if b.EditionInfo != "" &&
		!strings.Contains(b.EditionInfo, "error") &&
		!strings.Contains(b.EditionInfo, "Reason:") &&
		!strings.Contains(b.EditionInfo, "mismatch") &&
		!strings.Contains(b.EditionInfo, "Audiobookshelf") {
		// Use existing value if it appears valid
		editionInfo = strings.TrimSpace(b.EditionInfo)
	} else {
		// Otherwise, use "Unabridged" for audiobooks as a reasonable default
		editionInfo = "Unabridged"
	}

	// Look up author IDs if we have an author name and a Hardcover client
	authorIDs := []int{} // Initialize as empty slice
	if len(b.AuthorIDs) > 0 {
		authorIDs = b.AuthorIDs
	} else if b.Author != "" {
		if hc != nil {
			// Try to look up author IDs from Hardcover
			if ids, err := LookupAuthorIDs(ctx, hc, b.Author); err == nil && len(ids) > 0 {
				authorIDs = ids
				logger.Debug(fmt.Sprintf("Looked up author IDs: %v", authorIDs))
			} else if err != nil {
				logger.Error("Failed to look up author IDs", map[string]interface{}{"error": err.Error()})
			}
		}
	}

	// Look up narrator IDs if we have a narrator name and a Hardcover client
	narratorIDs := []int{} // Initialize as empty slice
	if len(b.NarratorIDs) > 0 {
		narratorIDs = b.NarratorIDs
	} else if b.Narrator != "" {
		if hc != nil {
			// Split narrator string by commas and trim whitespace
			narratorNames := strings.Split(b.Narrator, ",")
			for i, name := range narratorNames {
				narratorNames[i] = strings.TrimSpace(name)
			}

			// Try to look up narrator IDs from Hardcover for all names
			if ids, err := LookupNarratorIDs(ctx, hc, narratorNames...); err == nil && len(ids) > 0 {
				narratorIDs = ids
				logger.Debug(fmt.Sprintf("Looked up narrator IDs: %v", narratorIDs))
			} else if err != nil {
				logger.Error("Failed to look up narrator IDs", map[string]interface{}{
					"error": err.Error(),
					"names": narratorNames,
				})
			}
		}
	}

	// Look up publisher ID if we have a publisher name and a Hardcover client
	if b.PublisherID == 0 && b.Publisher != "" {
		if hc != nil {
			// Try to look up publisher ID from Hardcover
			if id, err := LookupPublisherID(ctx, hc, b.Publisher); err == nil && id > 0 {
				b.PublisherID = id
				logger.Debug(fmt.Sprintf("Looked up publisher ID: %d", b.PublisherID))
			} else if err != nil {
				logger.Error("Failed to look up publisher ID", map[string]interface{}{
					"publisher": b.Publisher,
					"error":     err.Error(),
				})
			}
		}
	}

	logger.Debug(fmt.Sprintf("Final AuthorIDs: %v, NarratorIDs: %v", authorIDs, narratorIDs))

	// Initialize all fields with zero values to ensure they appear in the JSON output
	result := &EditionExport{
		// Core book information (used for import)
		BookID:        bookID,
		Title:         b.Title,
		Subtitle:      b.Subtitle,
		ImageURL:      imageURL,
		ASIN:          b.ASIN,
		ISBN10:        b.ISBN10,
		ISBN13:        b.ISBN13,
		AuthorIDs:     authorIDs,
		NarratorIDs:   narratorIDs,
		PublisherID:   publisherID,
		ReleaseDate:   b.ReleaseDate,
		AudioSeconds:  b.DurationSeconds,
		EditionFormat: editionFormat,
		EditionInfo:   editionInfo,
		LanguageID:    languageID,
		CountryID:     countryID,

		// Additional informational fields (not used during import)
		Info: &EditionExportInfo{
			AuthorName:        b.Author,
			NarratorName:      b.Narrator,
			PublisherName:     b.Publisher,
			PublishedYear:     b.PublishedYear,
			CoverURL:          b.CoverURL,
			HardcoverCoverURL: b.HardcoverCoverURL,
			Timestamp:         b.Timestamp,
			CreatedAt:         b.CreatedAt.Format(time.RFC3339),
			Reason:            b.Reason,
			Attempts:          b.Attempts,
		},
	}

	// If info.cover_url duplicates the main image_url, drop it to reduce redundancy
	if result.Info != nil && result.Info.CoverURL == result.ImageURL {
		result.Info.CoverURL = ""
	}

	// Log final result for debugging
	logger.Debug(fmt.Sprintf("Final EditionExport - BookID: %d, Title: %s, EditionInfo: %s, AuthorIDs: %v, PublisherID: %d",
		result.BookID, result.Title, result.EditionInfo, result.AuthorIDs, result.PublisherID))

	return result
}

// ToEditionInput converts a BookMismatch to an EditionCreatorInput
// This creates a best-effort conversion to the format expected by the edition import tool
// Note: This function should be called with a context that has a Hardcover client available
func (b *BookMismatch) ToEditionInput(ctx context.Context, hc hardcover.HardcoverClientInterface) (EditionCreatorInput, error) {
	// Get logger from context
	logger := logger.FromContext(ctx)

	// Log the book ID for debugging
	logger.Debug("Converting BookMismatch to EditionInput", map[string]interface{}{
		"book_id": b.BookID,
		"title":   b.Title,
	})

	// Parse book ID (use 0 if not available)
	bookID := 0
	if b.BookID != "" {
		// First try to parse as integer
		if id, err := strconv.Atoi(b.BookID); err == nil {
			bookID = id
		} else {
			// If it's not a number, try to extract a number from the string
			re := regexp.MustCompile(`(\d+)`)
			if matches := re.FindStringSubmatch(b.BookID); len(matches) > 0 {
				if id, err := strconv.Atoi(matches[0]); err == nil {
					bookID = id
				}
			}
		}
	}

	// If we still don't have a book ID, log a warning
	if bookID == 0 {
		logger.Warn("Could not determine book ID from BookMismatch", map[string]interface{}{
			"book_id": b.BookID,
			"title":   b.Title,
		})
	}

	// Handle ISBN (split into ISBN10/ISBN13 if possible)
	var isbn10, isbn13 string
	if b.ISBN != "" {
		// Simple heuristic: ISBN10 is 10 chars, ISBN13 is 13 chars
		if len(b.ISBN) == 10 {
			isbn10 = b.ISBN
		} else if len(b.ISBN) == 13 {
			isbn13 = b.ISBN
		}
	}

	// Format release date (required field)
	releaseDate := b.ReleaseDate
	if releaseDate == "" && b.PublishedYear != "" {
		releaseDate = b.PublishedYear + "-01-01" // Use Jan 1st if only year is known
	} else if releaseDate == "" {
		releaseDate = time.Now().Format("2006-01-02") // Default to today if no date
	}

	// Perform author and narrator lookups
	authorIDs, err := LookupAuthorIDs(ctx, hc, b.Author)
	if err != nil {
		authorIDs = []int{}
	}

	narratorIDs, err := LookupNarratorIDs(ctx, hc, b.Narrator)
	if err != nil {
		narratorIDs = []int{}
	}

	// Create user notes with additional metadata
	userNotes := ""
	if b.BookID != "" {
		userNotes += fmt.Sprintf("Original Book ID: %s\n", b.BookID)
	}
	if b.LibraryID != "" {
		userNotes += fmt.Sprintf("Library ID: %s\n", b.LibraryID)
	}
	if b.FolderID != "" {
		userNotes += fmt.Sprintf("Folder ID: %s\n", b.FolderID)
	}

	// Create the edition input
	edition := EditionCreatorInput{
		// Core book information
		BookID:   bookID,
		Title:    b.Title,
		Subtitle: b.Subtitle,

		// Identifiers
		ASIN:   b.ASIN,
		ISBN10: isbn10,
		ISBN13: isbn13,

		// Media information
		ImageURL:    b.ImageURL, // Prefer ImageURL over CoverURL
		AudioLength: b.DurationSeconds,
		LanguageID:  1, // Default to English (would need to be looked up)

		// Relationships
		AuthorIDs:   authorIDs,
		NarratorIDs: narratorIDs,
		PublisherID: 0, // Would need to be looked up
		CountryID:   1, // Default to US (would need to be looked up)

		// Edition information
		ReleaseDate:   releaseDate,
		EditionFormat: "Audiobook",
		EditionInfo:   "Imported from Audiobookshelf",

		// User notes
		UserNotes: strings.TrimSpace(userNotes),
	}

	return edition, nil
}

// BookMismatch represents a book that couldn't be matched/synced with Hardcover
// and needs manual review
//
// This is the internal representation used throughout the application
type BookMismatch struct {
	// Core book information
	BookID      string `json:"book_id"`
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle,omitempty"`
	Author      string `json:"author"`
	AuthorIDs   []int  `json:"author_ids,omitempty"`
	Narrator    string `json:"narrator,omitempty"`
	NarratorIDs []int  `json:"narrator_ids,omitempty"`

	// Identifiers
	ASIN      string `json:"asin,omitempty"`
	ISBN      string `json:"isbn,omitempty"`
	ISBN10    string `json:"isbn_10,omitempty"`
	ISBN13    string `json:"isbn_13,omitempty"`
	LibraryID string `json:"library_id,omitempty"`
	FolderID  string `json:"folder_id,omitempty"`

	// Metadata
	ReleaseDate     string `json:"release_date,omitempty"`
	PublishedYear   string `json:"published_year,omitempty"`
	DurationSeconds int    `json:"duration_seconds"`
	CoverURL        string `json:"cover_url,omitempty"`
	ImageURL        string `json:"image_url,omitempty"`

	// Hardcover-specific fields
	EditionFormat string `json:"edition_format,omitempty"`
	EditionInfo   string `json:"edition_information,omitempty"`
	LanguageID    int    `json:"language_id,omitempty"`
	CountryID     int    `json:"country_id,omitempty"`
	PublisherID   int    `json:"publisher_id,omitempty"`
	Publisher     string `json:"publisher,omitempty"`

	// Hardcover book details (for mismatch comparison)
	HardcoverBookID        string `json:"hardcover_book_id,omitempty"`
	HardcoverTitle         string `json:"hardcover_title,omitempty"`
	HardcoverAuthor        string `json:"hardcover_author,omitempty"`
	HardcoverPublishedYear string `json:"hardcover_published_year,omitempty"`
	HardcoverCoverURL      string `json:"hardcover_cover_url"`
	HardcoverPublisher     string `json:"hardcover_publisher,omitempty"`
	HardcoverASIN          string `json:"hardcover_asin,omitempty"`
	HardcoverISBN          string `json:"hardcover_isbn,omitempty"`
	HardcoverSlug          string `json:"hardcover_slug,omitempty"`

	// Tracking
	Reason    string    `json:"reason"`
	Timestamp int64     `json:"timestamp"`
	Attempts  int       `json:"attempts,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// EditionExportInfo contains additional informational fields that are not used during import
// but provide context about the book and the export process
type EditionExportInfo struct {
	// Original author/narrator/publisher names (from source)
	AuthorName    string `json:"author,omitempty"`
	NarratorName  string `json:"narrator,omitempty"`
	PublisherName string `json:"publisher,omitempty"`

	// Additional metadata from source
	PublishedYear     string `json:"published_year,omitempty"`
	CoverURL          string `json:"cover_url,omitempty"`
	HardcoverCoverURL string `json:"hardcover_cover_url,omitempty"`

	// Export process metadata
	Timestamp int64  `json:"timestamp,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Attempts  int    `json:"attempts,omitempty"`
}

// EditionExport represents the format expected by the Hardcover edition import tool
type EditionExport struct {
	// Core book information (used for import)
	BookID        int    `json:"book_id"`
	Title         string `json:"title"`
	Subtitle      string `json:"subtitle"`
	ImageURL      string `json:"image_url"`
	ASIN          string `json:"asin"`
	ISBN10        string `json:"isbn_10"`
	ISBN13        string `json:"isbn_13"`
	AuthorIDs     []int  `json:"author_ids"`
	NarratorIDs   []int  `json:"narrator_ids"`
	PublisherID   int    `json:"publisher_id"`
	ReleaseDate   string `json:"release_date"`
	AudioSeconds  int    `json:"audio_seconds"`
	EditionFormat string `json:"edition_format"`
	EditionInfo   string `json:"edition_information"`
	LanguageID    int    `json:"language_id"`
	CountryID     int    `json:"country_id"`

	// Additional informational fields (not used during import)
	Info *EditionExportInfo `json:"info,omitempty"`
}
