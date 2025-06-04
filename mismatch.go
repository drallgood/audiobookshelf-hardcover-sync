package main

import (
	"log"
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

	// Handle release date - prefer publishedDate, fallback to publishedYear with formatting
	releaseDate := formatReleaseDate(metadata.PublishedDate, metadata.PublishedYear)

	mismatch := BookMismatch{
		Title:         metadata.Title,
		Subtitle:      metadata.Subtitle,
		Author:        metadata.AuthorName,
		Narrator:      metadata.NarratorName,
		Publisher:     metadata.Publisher,
		PublishedYear: metadata.PublishedYear,
		ReleaseDate:   releaseDate,
		Duration:      durationHours,
		ISBN:          metadata.ISBN,
		ASIN:          metadata.ASIN,
		BookID:        bookID,
		EditionID:     editionID,
		Reason:        reason,
		Timestamp:     time.Now(),
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
			log.Printf("   Duration: %s", formatDuration(mismatch.Duration))
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
