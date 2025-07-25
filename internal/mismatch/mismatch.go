package mismatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audnex"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

var (
	mismatches   []BookMismatch
	mismatchLock sync.Mutex
)

// Add adds a new book mismatch to the collection
func Add(book BookMismatch) {
	mismatchLock.Lock()
	defer mismatchLock.Unlock()

	// Set timestamp if not already set
	if book.Timestamp == 0 {
		book.Timestamp = time.Now().Unix()
	}
	if book.Attempts == 0 {
		book.Attempts = 1
	}
	if book.CreatedAt.IsZero() {
		book.CreatedAt = time.Now()
	}

	mismatches = append(mismatches, book)

	// Log the mismatch
	log := logger.Get()
	if log != nil {
		log.Info("Mismatch recorded", map[string]interface{}{
			"title":  book.Title,
			"reason": book.Reason,
		})
	}
}

// RecordMismatch records a new book mismatch
func RecordMismatch(book *BookMismatch) error {
	mismatchLock.Lock()
	defer mismatchLock.Unlock()

	// Check if we already have this mismatch
	key := book.BookID
	for i, existing := range mismatches {
		if existing.BookID == key {
			existing.Attempts++
			existing.Timestamp = time.Now().Unix()
			existing.Reason = book.Reason
			mismatches[i] = existing
			return nil
		}
	}

	// Add timestamp and initialize attempts
	book.Timestamp = time.Now().Unix()
	book.CreatedAt = time.Now()
	book.Attempts = 1

	mismatches = append(mismatches, *book)
	return nil
}

// AddWithMetadata creates and adds a new book mismatch with enhanced metadata
// If hc is provided, it will be used to look up publisher and other metadata
func AddWithMetadata(metadata MediaMetadata, bookID, editionID, reason string, duration float64, audiobookShelfID string, hc hardcover.HardcoverClientInterface) {
	// Create a logger
	log := logger.Get()

	// Extract book ID from reason if it's in the format "... for book 12345"
	extractBookIDFromReason := func() string {
		re := regexp.MustCompile(`(?:for book(?: ID)?\s+)(\d+)`)
		matches := re.FindStringSubmatch(reason)
		if len(matches) > 1 {
			return matches[1]
		}
		return ""
	}

	// If we don't have a bookID but can extract one from the reason, use it
	if bookID == "" || bookID == "0" {
		if extractedID := extractBookIDFromReason(); extractedID != "" {
			log.Debug("Extracted book ID from reason", map[string]interface{}{
				"original_book_id": bookID,
				"extracted_id":     extractedID,
				"reason":           reason,
			})
			bookID = extractedID
		}
	}
	// Log the mismatch being added
	log.Debug("Adding mismatch with metadata", map[string]interface{}{
		"book_id":           bookID,
		"edition_id":        editionID,
		"title":             metadata.Title,
		"author":            metadata.AuthorName,
		"reason":            reason,
		"has_cover":         metadata.CoverURL != "",
		"has_hardcover_api": hc != nil,
	})

	// Enhanced release date lookup - try to get accurate date from Audnex API for ASIN
	releaseDate := ""
	audnexReleaseDate := ""
	
	// If we have an ASIN, try to look up the book details from Audnex API
	if metadata.ASIN != "" {
		log.Info("Attempting Audnex enrichment for mismatch with ASIN", map[string]interface{}{
			"asin":     metadata.ASIN,
			"title":    metadata.Title,
			"book_id":  bookID,
			"mismatch": true,
		})
		
		// Create a context with timeout for the ASIN lookup
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		
		// Create Audnex client and log the creation
		audnexClient := audnex.NewClient(logger.Get())
		log.Debug("Created Audnex client for ASIN lookup", map[string]interface{}{
			"asin":       metadata.ASIN,
			"client_nil": audnexClient == nil,
		})
		
		// Log details just before the API call
		log.Info("Calling Audnex API for book details by ASIN", map[string]interface{}{
			"asin":    metadata.ASIN,
			"title":   metadata.Title,
			"context": "mismatch_enrichment",
		})
		
		// Try to get book details from Audnex API
		book, err := audnexClient.GetBookByASIN(ctx, metadata.ASIN, "")
		
		// Enhanced logging based on response
		if err == nil && book != nil {
			// Successfully retrieved book details from Audnex
			log.Info("Audnex API lookup succeeded", map[string]interface{}{
				"asin":           metadata.ASIN,
				"audnex_title":   book.Title,
				"has_release":    book.ReleaseDate != "",
				"authors_type":   fmt.Sprintf("%T", book.Authors),
				"narrators_type": fmt.Sprintf("%T", book.Narrators),
			})
			
			// Get authors and narrators as strings for logging
			authors := book.GetAuthorsAsStrings()
			narrators := book.GetNarratorsAsStrings()
			
			log.Debug("Audnex parsed author/narrator data", map[string]interface{}{
				"asin":                metadata.ASIN,
				"authors_count":       len(authors),
				"narrators_count":     len(narrators),
				"authors_as_strings":  strings.Join(authors, ", "),
				"narrators_as_string": strings.Join(narrators, ", "),
			})
			
			if book.ReleaseDate != "" {
				audnexReleaseDate = book.ReleaseDate
				log.Info("Using release date from Audnex API for mismatch enrichment", map[string]interface{}{
					"asin":         metadata.ASIN,
					"release_date": audnexReleaseDate,
					"title":        book.Title,
				})
			}
		} else if err != nil {
			log.Warn("Failed to lookup book by ASIN from Audnex API", map[string]interface{}{
				"asin":  metadata.ASIN,
				"error": err.Error(),
			})
		} else {
			log.Warn("Audnex API returned nil book without error", map[string]interface{}{
				"asin": metadata.ASIN,
			})
		}
	} else {
		log.Debug("Skipping Audnex enrichment, no ASIN available", nil)
	}
	
	// Format release date - prefer Audnex API date, then publishedDate, fallback to publishedYear
	// Always ensure the date is in YYYY-MM-DD format without time component
	formatDateToYYYYMMDD := func(dateStr string) string {
		// If empty, return empty
		if dateStr == "" {
			return ""
		}
		
		// Check if it's already in YYYY-MM-DD format
		if len(dateStr) == 10 && dateStr[4] == '-' && dateStr[7] == '-' {
			return dateStr
		}
		
		// Try to parse ISO-8601 format (including with time component)
		// RFC3339 ("2006-01-02T15:04:05Z07:00") and variations
		formats := []string{
			"2006-01-02T15:04:05Z07:00", // RFC3339
			"2006-01-02T15:04:05.000Z07:00", // RFC3339 with milliseconds
			"2006-01-02", // Just the date part
			"2006-01-02 15:04:05", // MySQL datetime format
			"2006/01/02", // Slash format
			"01/02/2006", // US format
			"02/01/2006", // EU format
			"Jan 2, 2006", // Month name format
			"2 Jan 2006", // Day first format
		}
		
		for _, format := range formats {
			if parsed, err := time.Parse(format, dateStr); err == nil {
				return parsed.Format("2006-01-02") // Return in YYYY-MM-DD format
			}
		}
		
		// If it's just a year, append -01-01
		if len(dateStr) == 4 && regexp.MustCompile(`^\d{4}$`).MatchString(dateStr) {
			return dateStr + "-01-01" // Use Jan 1st if only year is known
		}
		
		// Could not parse, return as is
		log.Warn("Could not parse date format", map[string]interface{}{
			"date": dateStr,
		})
		return dateStr
	}
	
	// Process and format the release date
	if audnexReleaseDate != "" {
		releaseDate = formatDateToYYYYMMDD(audnexReleaseDate)
		log.Debug("Formatted Audnex release date", map[string]interface{}{
			"original": audnexReleaseDate,
			"formatted": releaseDate,
		})
	} else if metadata.PublishedDate != "" {
		releaseDate = formatDateToYYYYMMDD(metadata.PublishedDate)
	} else if metadata.PublishedYear != "" {
		releaseDate = formatDateToYYYYMMDD(metadata.PublishedYear)
	}

	// Extract ISBN10 and ISBN13 from metadata.ISBN if it's set
	isbn10, isbn13 := "", ""
	if metadata.ISBN != "" {
		// Simple heuristic: ISBN10 is 10 chars, ISBN13 is 13 chars
		if len(metadata.ISBN) == 10 {
			isbn10 = metadata.ISBN
		} else if len(metadata.ISBN) == 13 {
			isbn13 = metadata.ISBN
		}
	}

	// Default publisher values
	publisherID := 1 // Default publisher ID
	publisherName := metadata.Publisher

	// If we have a Hardcover client and a publisher name, try to look up the publisher ID
	if hc != nil && publisherName != "" {
		// Create a context with timeout for the publisher lookup
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Look up the publisher ID
		if id, err := LookupPublisherID(ctx, hc, publisherName); err == nil && id > 0 {
			publisherID = id
			logger.Get().Debug("Found publisher ID", map[string]interface{}{
				"name": publisherName,
				"id":   publisherID,
			})
		} else if err != nil {
			logger.Get().Warn("Failed to look up publisher", map[string]interface{}{
				"name":  publisherName,
				"error": err.Error(),
			})
		} else {
			logger.Get().Debug("Publisher not found, using default ID", map[string]interface{}{
				"name": publisherName,
			})
		}
	}

	// Ensure we have a valid book ID - if not, use the audiobookShelfID as a fallback
	if bookID == "" || bookID == "0" {
		bookID = audiobookShelfID
		log.Debug("Using audiobookShelfID as book ID", map[string]interface{}{
			"audiobook_shelf_id": audiobookShelfID,
		})
	}

	// Note: We no longer automatically use ASIN as bookID to prevent incorrect book identification
	// ASIN is stored in its dedicated field below

	// Create the mismatch with all available metadata
	mismatch := BookMismatch{
		// Core book information
		BookID:          bookID,
		Title:           metadata.Title,
		Subtitle:        metadata.Subtitle,
		Author:          metadata.AuthorName,
		Narrator:        metadata.NarratorName,
		PublishedYear:   metadata.PublishedYear,
		ReleaseDate:     releaseDate,
		DurationSeconds: int(duration + 0.5), // Round to nearest second

		// Identifiers
		ISBN:   metadata.ISBN, // Keep original ISBN for backward compatibility
		ISBN10: isbn10,
		ISBN13: isbn13,
		ASIN:   metadata.ASIN,

		// Media URLs
		CoverURL: metadata.CoverURL,
		ImageURL: metadata.CoverURL, // Use CoverURL as ImageURL by default

		// Edition information
		EditionFormat: "Audiobook",
		EditionInfo:   "Audiobookshelf", // Only include platform info, no debug/error details
		LanguageID:    1, // Default to English
		CountryID:     1, // Default to US

		// Publisher information
		PublisherID: publisherID, // Use looked up or default publisher ID
		Publisher:   publisherName,

		// AudiobookShelf specific
		LibraryID: "", // Will be set later if available
		FolderID:  "", // Will be set later if available

		// Tracking information
		Reason:    reason,
		Timestamp: time.Now().Unix(),
		CreatedAt: time.Now(),
	}

	Add(mismatch)
}

// GetAll returns a copy of all collected mismatches
func GetAll() []BookMismatch {
	mismatchLock.Lock()
	defer mismatchLock.Unlock()

	// Return a copy to avoid race conditions
	result := make([]BookMismatch, len(mismatches))
	copy(result, mismatches)
	return result
}

// Clear removes all collected mismatches
func Clear() {
	mismatchLock.Lock()
	defer mismatchLock.Unlock()
	mismatches = []BookMismatch{}
}

// ExportJSON returns all mismatches as a JSON string
func ExportJSON() (string, error) {
	mismatchLock.Lock()
	defer mismatchLock.Unlock()

	// Create a struct that matches the expected JSON structure
	type exportStruct struct {
		Mismatches []BookMismatch `json:"mismatches"`
		Count      int            `json:"count"`
		Timestamp  int64          `json:"timestamp"`
	}

	exportData := exportStruct{
		Mismatches: mismatches,
		Count:      len(mismatches),
		Timestamp:  time.Now().Unix(),
	}

	jsonData, err := json.MarshalIndent(exportData, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal mismatches to JSON: %w", err)
	}

	return string(jsonData), nil
}

// SaveToFile saves all mismatches as individual JSON files in the specified directory
// in a format compatible with the edition import tool. If outputDir is empty, it will
// use the directory from the provided config.
// Note: This function should be called with a context that has a Hardcover client available
// for proper author/narrator lookups.
func SaveToFile(ctx context.Context, hc hardcover.HardcoverClientInterface, outputDir string, cfg *config.Config) error {
	// Get logger instance
	log := logger.Get()

	// Determine the output directory
	if outputDir == "" {
		if cfg == nil || cfg.Paths.MismatchOutputDir == "" {
			err := fmt.Errorf("no output directory specified and no default in config")
			log.Error("Failed to determine output directory in mismatch.SaveToFile", map[string]interface{}{
				"error": err.Error(),
			})
			return err
		}
		outputDir = cfg.Paths.MismatchOutputDir
	}

	// Create the output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		err := fmt.Errorf("failed to create output directory: %w", err)
		log.Error("Failed to create output directory in mismatch.SaveToFile", map[string]interface{}{
			"directory": outputDir,
			"error":     err.Error(),
		})
		return err
	}

	// Clean up old files first
	if err := cleanupOldFiles(outputDir); err != nil {
		log.Warn("Failed to clean up old mismatch files", map[string]interface{}{
			"directory": outputDir,
			"error":     err.Error(),
		})
		// Continue anyway, this isn't a fatal error
	}

	// Get all mismatches
	mismatches := GetAll()
	if len(mismatches) == 0 {
		log.Info("No mismatches to save")
		return nil
	}

	// Track errors
	var saveErrors []error
	successCount := 0

	// Save each mismatch to a separate file
	for i, mismatch := range mismatches {
		// Generate a filename based on the book title
		safeTitle := SanitizeFilename(mismatch.Title)
		if safeTitle == "" {
			safeTitle = fmt.Sprintf("untitled_%d", i+1)
		}

		// Create a filename with a sequence number and the book title
		filename := fmt.Sprintf("edition_%03d_%s.json", i+1, safeTitle)
		filePath := filepath.Join(outputDir, filename)

		// Convert to export format
		// Use the provided context and Hardcover client for author/narrator lookups
		export := mismatch.ToEditionExport(ctx, hc)

		// Set edition information if not already set - only include platform info, not debug/error details
		if export.EditionInfo == "" {
			export.EditionInfo = "Audiobookshelf"
		}

		// Convert to JSON with indentation for readability
		jsonData, err := json.MarshalIndent(export, "", "  ")
		if err != nil {
			err = fmt.Errorf("failed to marshal edition export for '%s': %w", mismatch.Title, err)
			log.Error("Failed to marshal edition export to JSON", map[string]interface{}{
				"error": err.Error(),
				"title": mismatch.Title,
			})
			saveErrors = append(saveErrors, err)
			continue
		}

		// Add trailing newline for better file handling
		jsonData = append(jsonData, '\n')

		// Write to file
		if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
			err = fmt.Errorf("failed to write file '%s': %w", filePath, err)
			log.Error("Failed to write mismatch file in mismatch.SaveToFile", map[string]interface{}{
				"error":    err.Error(),
				"filePath": filePath,
			})
			saveErrors = append(saveErrors, err)
			continue
		}

		successCount++
	}

	// Log results
	if len(saveErrors) > 0 {
		log.Warn("Some mismatch files failed to save in mismatch.SaveToFile", map[string]interface{}{
			"successful": successCount,
			"failed":     len(saveErrors),
		})
	} else {
		log.Info("Successfully saved all mismatch files in mismatch.SaveToFile", map[string]interface{}{
			"count": successCount,
		})
	}

	return nil
}

// cleanupOldFiles removes old JSON files from the output directory
func cleanupOldFiles(dirPath string) error {
	log := logger.Get()

	// List all files in the directory
	files, err := os.ReadDir(dirPath)
	if err != nil {
		if log != nil {
			log.Error("Failed to read directory in mismatch.cleanupOldFiles", map[string]interface{}{
				"error":     err.Error(),
				"directory": dirPath,
			})
		}
		return fmt.Errorf("failed to read directory: %w", err)
	}

	// Delete all .json files
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".json") {
			filePath := filepath.Join(dirPath, file.Name())
			if err := os.Remove(filePath); err != nil {
				if log != nil {
					log.Error("Failed to remove file in mismatch.cleanupOldFiles", map[string]interface{}{
						"error": err.Error(),
						"file":  filePath,
					})
				}
				return fmt.Errorf("failed to remove file %s: %w", filePath, err)
			}
		}
	}

	return nil
}

// SanitizeFilename removes or replaces characters that are invalid in filenames
func SanitizeFilename(s string) string {
	// Replace invalid characters with spaces
	replacer := strings.NewReplacer(
		"<", "", ">", "", ":", " ", "\"", "", "/", " ", "\\", " ", "|", " ",
		"?", "", "*", "", "'", "", "&", "and", "%", "", "#", "",
		"@", "", "!", "", "$", "", "+", "", "`", "", "=", "", "~", "",
	)
	result := replacer.Replace(s)

	// Trim any leading/trailing spaces or dots
	result = strings.TrimSpace(result)
	result = strings.Trim(result, ".")

	// Replace multiple spaces with a single space
	for strings.Contains(result, "  ") {
		result = strings.ReplaceAll(result, "  ", " ")
	}

	// Limit length to avoid filesystem limits
	if len(result) > 100 {
		result = result[:100]
	}

	return result
}

// MediaMetadata contains metadata about an audiobook
// that can be used to enhance mismatch reporting
type MediaMetadata struct {
	Title         string
	Subtitle      string
	AuthorName    string
	NarratorName  string
	Publisher     string
	PublishedYear string
	PublishedDate string // Full publication date in YYYY-MM-DD format
	ISBN          string
	ASIN          string
	CoverURL      string  // URL to the book cover image
	Duration      float64 `json:"duration,omitempty"`
}
