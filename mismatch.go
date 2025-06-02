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
		log.Printf("   Author: %s", mismatch.Author)
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
