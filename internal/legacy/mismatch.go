// +build ignore

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
func addBookMismatchWithMetadata(metadata MediaMetadata, bookID, editionID, reason string, duration float64, audiobookShelfID string) {
	// Convert duration from seconds to hours for easier reading
	durationHours := duration / 3600.0
	// Store duration in seconds as integer for JSON processing
	durationSeconds := int(duration + 0.5) // Round to nearest second

	// Handle release date - prefer publishedDate, fallback to publishedYear with formatting
	releaseDate := formatReleaseDate(metadata.PublishedDate, metadata.PublishedYear)

	mismatch := BookMismatch{
		Title:             metadata.Title,
		Subtitle:          metadata.Subtitle,
		Author:            metadata.AuthorName,
		Narrator:          metadata.NarratorName,
		Publisher:         metadata.Publisher,
		PublishedYear:     metadata.PublishedYear,
		ReleaseDate:       releaseDate,
		Duration:          durationHours,
		DurationSeconds:   durationSeconds,
		ISBN:              metadata.ISBN,
		ASIN:              metadata.ASIN,
		BookID:            bookID,
		EditionID:         editionID,
		AudiobookShelfID:  audiobookShelfID,
		Reason:            reason,
		Timestamp:         time.Now(),
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

	// Save each mismatch as an edition-ready JSON file
	return createEditionReadyMismatchFiles(dirPath)
}

// cleanupOldMismatchFiles removes old JSON files from the mismatch directory
// to prevent accumulation of outdated data
func cleanupOldMismatchFiles(dirPath string) error {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		// Directory doesn't exist or can't be read, no cleanup needed
		return nil
	}

	deletedCount := 0
	for _, file := range files {
		if file.IsDir() {
			continue // Skip subdirectories
		}

		// Only delete JSON files to avoid accidentally removing other files
		if strings.HasSuffix(strings.ToLower(file.Name()), ".json") {
			filePath := filepath.Join(dirPath, file.Name())
			if err := os.Remove(filePath); err != nil {
				log.Printf("Warning: Failed to remove old mismatch file %s: %v", filePath, err)
			} else {
				deletedCount++
				debugLog("Removed old mismatch file: %s", file.Name())
			}
		}
	}

	if deletedCount > 0 {
		log.Printf("🧹 Cleaned up %d old mismatch JSON files from %s", deletedCount, dirPath)
	}

	return nil
}

// createEditionReadyMismatchFiles saves mismatches as edition-ready JSON files
// These files can be used directly with the edition creation process
func createEditionReadyMismatchFiles(dirPath string) error {
	if len(bookMismatches) == 0 {
		return nil
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %v", dirPath, err)
	}

	// Clean up old JSON files in the directory before creating new ones
	err := cleanupOldMismatchFiles(dirPath)
	if err != nil {
		log.Printf("Warning: Failed to clean up old mismatch files: %v", err)
		// Continue processing even if cleanup fails
	}

	// Save each mismatch as an edition-ready JSON file
	savedCount := 0
	for i, mismatch := range bookMismatches {
		// Create a safe filename from title and index
		safeTitle := sanitizeFilename(mismatch.Title)
		if len(safeTitle) > 50 {
			safeTitle = safeTitle[:50] // Limit filename length
		}

		fileName := fmt.Sprintf("%03d_%s.json", i+1, safeTitle)
		filePath := filepath.Join(dirPath, fileName)

		// Convert mismatch to EditionCreatorInput format
		editionInput := convertMismatchToEditionInput(mismatch)

		// Convert to JSON
		jsonData, err := json.MarshalIndent(editionInput, "", "  ")
		if err != nil {
			log.Printf("Warning: Failed to marshal edition input %d (%s): %v", i+1, mismatch.Title, err)
			continue
		}

		// Write JSON to individual file
		if err := os.WriteFile(filePath, jsonData, 0644); err != nil {
			log.Printf("Warning: Failed to write file %s: %v", filePath, err)
			continue
		}

		savedCount++
	}

	log.Printf("🏗️  Edition-ready files saved to directory: %s", dirPath)
	log.Printf("📊 Successfully saved: %d/%d edition creation JSON files", savedCount, len(bookMismatches))

	if savedCount < len(bookMismatches) {
		log.Printf("⚠️  Some files failed to save - check warnings above")
	}

	return nil
}

// convertMismatchToEditionInput converts a BookMismatch to EditionCreatorInput format
// This enhanced version integrates existing lookup functions to provide better role identification
// and potentially auto-populate IDs when found
func convertMismatchToEditionInput(mismatch BookMismatch) EditionCreatorInput {
	// Create edition input with available data
	input := EditionCreatorInput{
		BookID:        0, // Will be parsed from mismatch.BookID if available
		Title:         mismatch.Title,
		Subtitle:      mismatch.Subtitle,
		ImageURL:      "", // Will need to be determined from ASIN if available
		ASIN:          mismatch.ASIN,
		ISBN10:        "",      // Will need to be extracted from ISBN if available
		ISBN13:        "",      // Will need to be extracted from ISBN if available
		AuthorIDs:     []int{}, // Will attempt smart lookup
		NarratorIDs:   []int{}, // Will attempt smart lookup
		PublisherID:   0,       // Will attempt smart lookup
		ReleaseDate:   mismatch.ReleaseDate,
		AudioLength:   mismatch.DurationSeconds,
		EditionFormat: "Audible Audio", // Default for audiobooks
		EditionInfo:   "",              // Enhanced info will be added
		LanguageID:    1,               // Default to English
		CountryID:     1,               // Default to USA
	}

	// Parse BookID from string if available
	if mismatch.BookID != "" {
		if bookID, err := strconv.Atoi(mismatch.BookID); err == nil {
			input.BookID = bookID
		} else {
			debugLog("Failed to parse BookID '%s' as integer: %v", mismatch.BookID, err)
		}
	}

	// Generate cover image URL from AudiobookShelf API
	// Use the cover API endpoint which provides the actual cover image
	input.ImageURL = fmt.Sprintf("%s/api/items/%s/cover", getAudiobookShelfURL(), mismatch.AudiobookShelfID)

	// Extract ISBN-10 and ISBN-13 from the ISBN field
	if mismatch.ISBN != "" {
		isbn := strings.ReplaceAll(mismatch.ISBN, "-", "")
		isbn = strings.ReplaceAll(isbn, " ", "")

		if len(isbn) == 10 {
			input.ISBN10 = isbn
		} else if len(isbn) == 13 {
			input.ISBN13 = isbn
		}
	}

	// Enhanced lookup logic using existing sophisticated search functions
	var manualSteps []string
	var lookupErrors []string

	// Book ID lookup
	if input.BookID == 0 {
		manualSteps = append(manualSteps, "book_id: Search for existing book or create new one")
	}

	// Smart author processing
	if mismatch.Author != "" {
		authorIDs, authorMsg := processAuthorsWithLookup(mismatch.Author)
		input.AuthorIDs = authorIDs
		if authorMsg != "" {
			if len(authorIDs) > 0 {
				manualSteps = append(manualSteps, fmt.Sprintf("author_ids: %s", authorMsg))
			} else {
				lookupErrors = append(lookupErrors, fmt.Sprintf("author_ids: %s", authorMsg))
			}
		}
	}

	// Smart narrator processing
	if mismatch.Narrator != "" {
		narratorIDs, narratorMsg := processNarratorsWithLookup(mismatch.Narrator)
		input.NarratorIDs = narratorIDs
		if narratorMsg != "" {
			if len(narratorIDs) > 0 {
				manualSteps = append(manualSteps, fmt.Sprintf("narrator_ids: %s", narratorMsg))
			} else {
				lookupErrors = append(lookupErrors, fmt.Sprintf("narrator_ids: %s", narratorMsg))
			}
		}
	}

	// Smart publisher processing
	if mismatch.Publisher != "" {
		publisherID, publisherMsg := processPublisherWithLookup(mismatch.Publisher)
		input.PublisherID = publisherID
		if publisherMsg != "" {
			if publisherID > 0 {
				manualSteps = append(manualSteps, fmt.Sprintf("publisher_id: %s", publisherMsg))
			} else {
				lookupErrors = append(lookupErrors, fmt.Sprintf("publisher_id: %s", publisherMsg))
			}
		}
	}

	// Build enhanced EditionInfo with smart lookup results
	var infoSections []string
	if len(manualSteps) > 0 {
		infoSections = append(infoSections, fmt.Sprintf("VERIFY: %s", strings.Join(manualSteps, "; ")))
	}
	if len(lookupErrors) > 0 {
		infoSections = append(infoSections, fmt.Sprintf("LOOKUP NEEDED: %s", strings.Join(lookupErrors, "; ")))
	}

	if len(infoSections) > 0 {
		input.EditionInfo = strings.Join(infoSections, " | ")
	}

	// Enhance with Audible API data if enabled and ASIN is available
	if input.ASIN != "" {
		// Create a PrepopulatedEditionInput for the enhancement function
		prepopulated := PrepopulatedEditionInput{
			BookID:              input.BookID,
			Title:               input.Title,
			Subtitle:            input.Subtitle,
			ImageURL:            input.ImageURL,
			ASIN:                input.ASIN,
			ISBN10:              input.ISBN10,
			ISBN13:              input.ISBN13,
			AuthorIDs:           input.AuthorIDs,
			NarratorIDs:         input.NarratorIDs,
			PublisherID:         input.PublisherID,
			ReleaseDate:         input.ReleaseDate,
			AudioSeconds:        input.AudioLength,
			EditionFormat:       input.EditionFormat,
			EditionInfo:         input.EditionInfo,
			LanguageID:          input.LanguageID,
			CountryID:           input.CountryID,
			PrepopulationSource: "mismatch",
		}

		// Attempt Audible API enhancement
		if err := enhanceWithExternalData(&prepopulated, input.ASIN); err != nil {
			debugLog("Failed to enhance mismatch data with Audible API for ASIN %s: %v", input.ASIN, err)
		} else {
			// Update the input with enhanced data
			input.Title = prepopulated.Title
			input.Subtitle = prepopulated.Subtitle
			input.ImageURL = prepopulated.ImageURL
			input.ASIN = prepopulated.ASIN
			input.ISBN10 = prepopulated.ISBN10
			input.ISBN13 = prepopulated.ISBN13
			input.AuthorIDs = prepopulated.AuthorIDs
			input.NarratorIDs = prepopulated.NarratorIDs
			input.PublisherID = prepopulated.PublisherID
			input.ReleaseDate = prepopulated.ReleaseDate
			input.AudioLength = prepopulated.AudioSeconds
			input.EditionFormat = prepopulated.EditionFormat
			input.EditionInfo = prepopulated.EditionInfo
			input.LanguageID = prepopulated.LanguageID
			input.CountryID = prepopulated.CountryID

			// Add ASIN reference info if available
			if prepopulated.PrepopulationSource == "hardcover+asin" && input.ASIN != "" {
				if input.EditionInfo != "" {
					input.EditionInfo += " | ASIN: " + input.ASIN
				} else {
					input.EditionInfo = "ASIN: " + input.ASIN
				}
				debugLog("Added ASIN reference %s to mismatch data for '%s'", input.ASIN, input.Title)
			}
			// No enhancement markers for non-functional APIs
		}
	}

	return input
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

// Smart lookup helper functions that integrate with existing search functionality

// processAuthorsWithLookup attempts to find and validate authors using the existing search functions
// Returns: found IDs (can be empty), message for EditionInfo
func processAuthorsWithLookup(authorString string) ([]int, string) {
	if authorString == "" {
		return []int{}, ""
	}

	// Split multiple authors (common patterns: ", " and " and ")
	var authorNames []string

	// First try splitting by ", " (most common)
	if strings.Contains(authorString, ", ") {
		authorNames = strings.Split(authorString, ", ")
	} else if strings.Contains(authorString, " and ") {
		// Split by " and " but be careful of names like "John and Jane Smith"
		parts := strings.Split(authorString, " and ")
		authorNames = parts
	} else {
		// Single author
		authorNames = []string{authorString}
	}

	var foundIDs []int
	var messages []string
	var notFoundNames []string

	for _, name := range authorNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		// Remove common suffixes that might interfere with search
		cleanName := strings.TrimSuffix(name, " PhD")
		cleanName = strings.TrimSuffix(cleanName, " Ph.D")
		cleanName = strings.TrimSuffix(cleanName, " Dr.")

		// Try searching for this author (with caching)
		authors, err := searchAuthorsCached(cleanName, 3) // Limit to 3 to avoid too many results
		if err != nil {
			debugLog("Author search failed for '%s': %v", cleanName, err)
			notFoundNames = append(notFoundNames, name)
			continue
		}

		if len(authors) == 0 {
			notFoundNames = append(notFoundNames, name)
			continue
		}

		// Check for exact matches first
		var exactMatch *PersonSearchResult
		for _, author := range authors {
			if strings.EqualFold(author.Name, name) || strings.EqualFold(author.Name, cleanName) {
				exactMatch = &author
				break
			}
		}

		if exactMatch != nil {
			foundIDs = append(foundIDs, exactMatch.ID)
			canonicalNote := ""
			if !exactMatch.IsCanonical {
				canonicalNote = " (alias)"
			}
			messages = append(messages, fmt.Sprintf("Found %s (ID: %d%s)", exactMatch.Name, exactMatch.ID, canonicalNote))
		} else {
			// No exact match, provide search suggestion with top result
			topResult := authors[0]
			canonicalNote := ""
			if !topResult.IsCanonical {
				canonicalNote = " (alias)"
			}
			messages = append(messages, fmt.Sprintf("'%s' -> suggest %s (ID: %d%s)", name, topResult.Name, topResult.ID, canonicalNote))
		}
	}

	// Add any names that weren't found
	for _, name := range notFoundNames {
		messages = append(messages, fmt.Sprintf("'%s' -> search manually", name))
	}

	resultMsg := strings.Join(messages, "; ")
	return foundIDs, resultMsg
}

// processNarratorsWithLookup attempts to find and validate narrators, with smart role detection
// Returns: found IDs (can be empty), message for EditionInfo
func processNarratorsWithLookup(narratorString string) ([]int, string) {
	if narratorString == "" {
		return []int{}, ""
	}

	// Split multiple narrators (common patterns: ", " and " and ")
	var narratorNames []string

	if strings.Contains(narratorString, ", ") {
		narratorNames = strings.Split(narratorString, ", ")
	} else if strings.Contains(narratorString, " and ") {
		parts := strings.Split(narratorString, " and ")
		narratorNames = parts
	} else {
		narratorNames = []string{narratorString}
	}

	var foundIDs []int
	var messages []string
	var notFoundNames []string

	for _, name := range narratorNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		// Remove common suffixes
		cleanName := strings.TrimSuffix(name, " PhD")
		cleanName = strings.TrimSuffix(cleanName, " Ph.D")
		cleanName = strings.TrimSuffix(cleanName, " Dr.")

		// First try searching as narrator (with caching)
		narrators, err := searchNarratorsCached(cleanName, 3)
		if err != nil {
			debugLog("Narrator search failed for '%s': %v", cleanName, err)
			// If narrator search fails, try as author (person might be narrator but listed as author) - with caching
			authors, authorErr := searchAuthorsCached(cleanName, 3)
			if authorErr != nil {
				notFoundNames = append(notFoundNames, name)
				continue
			}

			if len(authors) > 0 {
				topAuthor := authors[0]
				messages = append(messages, fmt.Sprintf("'%s' found as author (ID: %d) - verify narrator role", name, topAuthor.ID))
			} else {
				notFoundNames = append(notFoundNames, name)
			}
			continue
		}

		if len(narrators) == 0 {
			// Try searching as author (some narrators might be listed primarily as authors) - with caching
			authors, authorErr := searchAuthorsCached(cleanName, 3)
			if authorErr == nil && len(authors) > 0 {
				topAuthor := authors[0]
				canonicalNote := ""
				if !topAuthor.IsCanonical {
					canonicalNote = " (alias)"
				}
				messages = append(messages, fmt.Sprintf("'%s' found as author %s (ID: %d%s) - verify narrator role", name, topAuthor.Name, topAuthor.ID, canonicalNote))
			} else {
				notFoundNames = append(notFoundNames, name)
			}
			continue
		}

		// Check for exact matches first
		var exactMatch *PersonSearchResult
		for _, narrator := range narrators {
			if strings.EqualFold(narrator.Name, name) || strings.EqualFold(narrator.Name, cleanName) {
				exactMatch = &narrator
				break
			}
		}

		if exactMatch != nil {
			foundIDs = append(foundIDs, exactMatch.ID)
			canonicalNote := ""
			if !exactMatch.IsCanonical {
				canonicalNote = " (alias)"
			}
			messages = append(messages, fmt.Sprintf("Found narrator %s (ID: %d%s)", exactMatch.Name, exactMatch.ID, canonicalNote))
		} else {
			// No exact match, provide search suggestion
			topResult := narrators[0]
			canonicalNote := ""
			if !topResult.IsCanonical {
				canonicalNote = " (alias)"
			}
			messages = append(messages, fmt.Sprintf("'%s' -> suggest narrator %s (ID: %d%s)", name, topResult.Name, topResult.ID, canonicalNote))
		}
	}

	// Add any names that weren't found
	for _, name := range notFoundNames {
		messages = append(messages, fmt.Sprintf("'%s' -> search manually", name))
	}

	resultMsg := strings.Join(messages, "; ")
	return foundIDs, resultMsg
}

// processPublisherWithLookup attempts to find and validate publisher
// Returns: found ID (0 if not found), message for EditionInfo
func processPublisherWithLookup(publisherName string) (int, string) {
	if publisherName == "" {
		return 0, ""
	}

	publisherName = strings.TrimSpace(publisherName)

	// Try searching for this publisher (with caching)
	publishers, err := searchPublishersCached(publisherName, 3)
	if err != nil {
		debugLog("Publisher search failed for '%s': %v", publisherName, err)
		return 0, fmt.Sprintf("'%s' -> search manually (search failed)", publisherName)
	}

	if len(publishers) == 0 {
		return 0, fmt.Sprintf("'%s' -> search manually (no results)", publisherName)
	}

	// Check for exact matches first
	for _, publisher := range publishers {
		if strings.EqualFold(publisher.Name, publisherName) {
			canonicalNote := ""
			if !publisher.IsCanonical {
				canonicalNote = " (alias)"
			}
			return publisher.ID, fmt.Sprintf("Found %s (ID: %d%s)", publisher.Name, publisher.ID, canonicalNote)
		}
	}

	// No exact match, provide search suggestion with top result
	topResult := publishers[0]
	canonicalNote := ""
	if !topResult.IsCanonical {
		canonicalNote = " (alias)"
	}

	return 0, fmt.Sprintf("'%s' -> suggest %s (ID: %d%s)", publisherName, topResult.Name, topResult.ID, canonicalNote)
}
