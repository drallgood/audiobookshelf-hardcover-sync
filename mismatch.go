package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Global slice to collect mismatches during sync
var bookMismatches []BookMismatch

// addBookMismatch adds a book mismatch to the global collection for later review
func addBookMismatch(title, author, isbn, asin, bookID, editionID, reason string) {
	mismatch := BookMismatch{
		Title:     title,
		Author:    author,
		ISBN:      isbn,
		ASIN:      asin,
		BookID:    bookID,
		EditionID: editionID,
		Reason:    reason,
		Timestamp: time.Now(),
	}
	bookMismatches = append(bookMismatches, mismatch)
	debugLog("MISMATCH COLLECTED: %s - %s", title, reason)
}

// addBookMismatchWithMetadata adds a book mismatch with enhanced metadata to the global collection
func addBookMismatchWithMetadata(metadata MediaMetadata, bookID, editionID, reason string, duration float64) {
	// Convert duration from seconds to hours for easier reading
	durationHours := duration / 3600.0
	// Store duration in seconds as integer for JSON processing
	durationSeconds := int(duration + 0.5) // Round to nearest second

	// Handle release date - prefer publishedDate, fallback to publishedYear with formatting
	releaseDate := formatReleaseDate(metadata.PublishedDate, metadata.PublishedYear)

	mismatch := BookMismatch{
		Title:           metadata.Title,
		Subtitle:        metadata.Subtitle,
		Author:          metadata.AuthorName,
		Narrator:        metadata.NarratorName,
		Publisher:       metadata.Publisher,
		PublishedYear:   metadata.PublishedYear,
		ReleaseDate:     releaseDate,
		Duration:        durationHours,
		DurationSeconds: durationSeconds,
		ISBN:            metadata.ISBN,
		ASIN:            metadata.ASIN,
		BookID:          bookID,
		EditionID:       editionID,
		Reason:          reason,
		Timestamp:       time.Now(),
	}
	bookMismatches = append(bookMismatches, mismatch)
	debugLog("MISMATCH COLLECTED: %s - %s", metadata.Title, reason)
}

// printMismatchSummary prints a summary of all collected mismatches
func printMismatchSummary() {
	if len(bookMismatches) == 0 {
		log.Printf("✅ No book matching issues found during sync")
		return
	}

	log.Printf("⚠️  MANUAL REVIEW NEEDED: Found %d book(s) that may need verification", len(bookMismatches))
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	for i, mismatch := range bookMismatches {
		log.Printf("%d. Title: %s", i+1, mismatch.Title)
		if mismatch.Subtitle != "" {
			log.Printf("   Subtitle: %s", mismatch.Subtitle)
		}
		log.Printf("   Author: %s", mismatch.Author)
		if mismatch.Narrator != "" {
			log.Printf("   Narrator: %s", mismatch.Narrator)
		}
		if mismatch.Publisher != "" {
			log.Printf("   Publisher: %s", mismatch.Publisher)
		}
		if mismatch.ReleaseDate != "" {
			log.Printf("   Release Date: %s", mismatch.ReleaseDate)
		}
		if mismatch.Duration > 0 {
			log.Printf("   Duration: %s (%d seconds)", formatDuration(mismatch.Duration), mismatch.DurationSeconds)
		}
		if mismatch.ISBN != "" {
			log.Printf("   ISBN: %s", mismatch.ISBN)
		}
		if mismatch.ASIN != "" {
			log.Printf("   ASIN: %s", mismatch.ASIN)
		}
		if mismatch.BookID != "" {
			log.Printf("   Hardcover Book ID: %s", mismatch.BookID)
		}
		if mismatch.EditionID != "" {
			log.Printf("   Hardcover Edition ID: %s", mismatch.EditionID)
		}
		log.Printf("   Issue: %s", mismatch.Reason)
		log.Printf("   Time: %s", mismatch.Timestamp.Format("2006-01-02 15:04:05"))

		if i < len(bookMismatches)-1 {
			log.Printf("   ──────────────────────────────────────────────────────────────────────")
		}
	}

	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("💡 RECOMMENDATIONS:")
	log.Printf("   1. Check if the Hardcover Book ID corresponds to the correct audiobook edition")
	log.Printf("   2. Verify progress syncing is working correctly for these books")
	log.Printf("   3. Consider updating book metadata if ISBN/ASIN is missing or incorrect")
	log.Printf("   4. Set AUDIOBOOK_MATCH_MODE=skip or AUDIOBOOK_MATCH_MODE=fail to change behavior")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
}

// clearMismatches clears the collected mismatches (useful for testing or multiple syncs)
func clearMismatches() {
	bookMismatches = []BookMismatch{}
}

// exportMismatchesJSON exports all collected mismatches as JSON string
// This includes duration in seconds for JSON processing compatibility
func exportMismatchesJSON() (string, error) {
	if len(bookMismatches) == 0 {
		return "[]", nil
	}

	jsonData, err := json.MarshalIndent(bookMismatches, "", "  ")
	if err != nil {
		return "", err
	}

	return string(jsonData), nil
}

// saveMismatchesJSONFile saves all collected mismatches as individual JSON files
// The directory path is determined by MISMATCH_JSON_FILE environment variable
// If not set, no files are saved
func saveMismatchesJSONFile() error {
	dirPath := getMismatchJSONFile()
	if dirPath == "" {
		return nil // File saving is disabled
	}

	if len(bookMismatches) == 0 {
		log.Printf("📝 No mismatches to save")
		return nil
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %v", dirPath, err)
	}

	// Save each mismatch as an individual JSON file
	savedCount := 0
	for i, mismatch := range bookMismatches {
		// Create a safe filename from title and index
		safeTitle := sanitizeFilename(mismatch.Title)
		if len(safeTitle) > 50 {
			safeTitle = safeTitle[:50] // Limit filename length
		}
		
		fileName := fmt.Sprintf("%03d_%s.json", i+1, safeTitle)
		filePath := filepath.Join(dirPath, fileName)

		// Convert single mismatch to JSON
		jsonData, err := json.MarshalIndent(mismatch, "", "  ")
		if err != nil {
			log.Printf("Warning: Failed to marshal mismatch %d (%s): %v", i+1, mismatch.Title, err)
			continue
		}

		// Write JSON to individual file
		if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
			log.Printf("Warning: Failed to write file %s: %v", filePath, err)
			continue
		}

		savedCount++
	}

	log.Printf("💾 Mismatches saved to directory: %s", dirPath)
	log.Printf("📊 Successfully saved: %d/%d individual JSON files", savedCount, len(bookMismatches))
	
	if savedCount < len(bookMismatches) {
		log.Printf("⚠️  Some files failed to save - check warnings above")
	}

	return nil
}

// sanitizeFilename removes or replaces characters that are invalid in filenames
func sanitizeFilename(title string) string {
	// Replace problematic characters with underscores
	result := ""
	for _, char := range title {
		switch {
		case char >= 'a' && char <= 'z':
			result += string(char)
		case char >= 'A' && char <= 'Z':
			result += string(char)
		case char >= '0' && char <= '9':
			result += string(char)
		case char == ' ':
			result += "_"
		case char == '-' || char == '.':
			result += string(char)
		default:
			result += "_" // Replace any other character with underscore
		}
	}
	return result
}
