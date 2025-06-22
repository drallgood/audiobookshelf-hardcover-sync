package mismatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
		log.Info().
			Str("title", book.Title).
			Str("reason", book.Reason).
			Msg("Mismatch recorded")
	}
}

// AddWithMetadata creates and adds a new book mismatch with enhanced metadata
func AddWithMetadata(metadata MediaMetadata, bookID, editionID, reason string, duration float64, audiobookShelfID string) {
	// Convert duration from seconds to hours for display
	durationHours := duration / 3600.0
	// Store duration in seconds as integer for JSON processing
	durationSeconds := int(duration + 0.5) // Round to nearest second

	// Handle release date - prefer publishedDate, fallback to publishedYear with formatting
	releaseDate := formatReleaseDate(metadata.PublishedDate, metadata.PublishedYear)

	mismatch := BookMismatch{
		Title:            metadata.Title,
		Subtitle:         metadata.Subtitle,
		Author:           metadata.AuthorName,
		Narrator:         metadata.NarratorName,
		Publisher:        metadata.Publisher,
		PublishedYear:    metadata.PublishedYear,
		ReleaseDate:      releaseDate,
		Duration:         durationHours,
		DurationSeconds:  durationSeconds,
		ISBN:             metadata.ISBN,
		ASIN:             metadata.ASIN,
		BookID:           bookID,
		EditionID:        editionID,
		AudiobookShelfID: audiobookShelfID,
		Reason:           reason,
		Timestamp:        time.Now().Unix(),
		Attempts:         1,
		CreatedAt:        time.Now(),
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
// If outputDir is empty, it will use the directory from the config
func SaveToFile(outputDir string) error {
	mismatchLock.Lock()
	defer mismatchLock.Unlock()

	log := logger.Get()
	if log == nil {
		return fmt.Errorf("logger not initialized")
	}

	// If no output directory is provided, try to get it from the config
	if outputDir == "" {
		// Try to get config
		cfg, err := config.Load("")
		if err == nil && cfg != nil && cfg.App.MismatchOutputDir != "" {
			outputDir = cfg.App.MismatchOutputDir
		} else {
			// Fall back to environment variable for backward compatibility
			outputDir = os.Getenv("MISMATCH_JSON_FILE")
			if outputDir == "" {
				log.Info().Msg("No output directory specified for mismatch files")
				return nil // No output directory specified
			}
		}
	}

	// Create the output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Error().
			Err(err).
			Str("directory", outputDir).
			Msg("Failed to create output directory")
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Clean up old files first
	if err := cleanupOldFiles(outputDir); err != nil {
		log.Warn().
			Err(err).
			Str("directory", outputDir).
			Msg("Failed to clean up old mismatch files")
	}

	// Save each mismatch as a separate JSON file
	for i, m := range mismatches {
		// Generate a safe filename
		safeTitle := SanitizeFilename(m.Title)
		if safeTitle == "" {
			safeTitle = "untitled"
		}

		// Add a number prefix for sorting
		filename := fmt.Sprintf("%03d_%s.json", i+1, safeTitle)
		filePath := filepath.Join(outputDir, filename)

		// Create the JSON data with indentation
		jsonData, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			log.Error().
				Err(err).
				Str("title", m.Title).
				Msg("Failed to marshal mismatch to JSON")
			continue
		}

		// Write to file
		if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
			log.Error().
				Err(err).
				Str("path", filePath).
				Msg("Failed to write mismatch file")
			continue
		}

		log.Debug().
			Str("path", filePath).
			Msg("Saved mismatch to file")
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
			log.Error().
				Err(err).
				Str("directory", dirPath).
				Msg("Failed to read directory")
		}
		return fmt.Errorf("failed to read directory: %w", err)
	}

	// Delete all .json files
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".json") {
			filePath := filepath.Join(dirPath, file.Name())
			if err := os.Remove(filePath); err != nil {
				if log != nil {
					log.Error().
						Err(err).
						Str("file", filePath).
						Msg("Failed to remove file")
				}
				return fmt.Errorf("failed to remove file %s: %w", filePath, err)
			}
		}
	}

	return nil
}

// formatReleaseDate formats a release date from the given components
func formatReleaseDate(date, year string) string {
	if date != "" {
		return date
	}
	if year != "" {
		return year
	}
	return ""
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

// MediaMetadata represents the metadata for an audiobook from Audiobookshelf
type MediaMetadata struct {
	Title         string  `json:"title"`
	Subtitle      string  `json:"subtitle,omitempty"`
	AuthorName    string  `json:"authorName"`
	NarratorName  string  `json:"narratorName,omitempty"`
	Publisher     string  `json:"publisher,omitempty"`
	PublishedYear string  `json:"publishedYear,omitempty"`
	PublishedDate string  `json:"publishedDate,omitempty"`
	ISBN          string  `json:"isbn,omitempty"`
	ASIN          string  `json:"asin,omitempty"`
	Duration      float64 `json:"duration,omitempty"`
}
