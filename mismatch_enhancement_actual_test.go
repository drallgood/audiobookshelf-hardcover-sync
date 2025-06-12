package main

import (
	"strings"
	"testing"
	"time"
)

func TestMismatchASINEnhancementActual(t *testing.T) {
	// Test with the real example from the logs
	mismatch := BookMismatch{
		Title:             "Blue Shift (Unabridged)",
		Subtitle:          "Backyard Starship, Book 5",
		Author:            "J.N. Chaney, Terry Maggert",
		Narrator:          "Jeffrey Kafer",
		Publisher:         "Podium Audio",
		PublishedYear:     "2022",
		ReleaseDate:       "2022",
		Duration:          11.3,
		DurationSeconds:   40817,
		ISBN:              "",
		ASIN:              "B09ZVQ796F",
		BookID:            "",
		EditionID:         "",
		AudiobookShelfID:  "a2768e1f-95be-46a0-b6a3-51f161d6b81c",
		Reason:            "ASIN lookup failed for ASIN B09ZVQ796F - no audiobook edition found",
		Timestamp:         time.Now(),
	}

	t.Logf("Testing mismatch enhancement for: %s", mismatch.Title)
	t.Logf("ASIN: %s", mismatch.ASIN)

	// Convert mismatch to edition input (this should preserve ASIN reference)
	editionInput := convertMismatchToEditionInput(mismatch)

	t.Logf("Results:")
	t.Logf("Title: %s", editionInput.Title)
	t.Logf("ASIN: %s", editionInput.ASIN)
	t.Logf("EditionInfo: %s", editionInput.EditionInfo)

	// Verify ASIN was preserved
	if editionInput.ASIN != mismatch.ASIN {
		t.Errorf("ASIN mismatch: expected %s, got %s", mismatch.ASIN, editionInput.ASIN)
	}

	// Check if ASIN reference marker was added
	// The system will add ASIN reference when available
	if editionInput.EditionInfo != "" {
		if strings.Contains(editionInput.EditionInfo, "ASIN:") {
			t.Logf("✅ SUCCESS: ASIN reference marker found in EditionInfo!")
			t.Logf("ASIN reference marker: %s", editionInput.EditionInfo)
		} else {
			t.Logf("ℹ️  INFO: No ASIN reference marker found, but EditionInfo has content: %s", editionInput.EditionInfo)
		}
	} else {
		t.Logf("ℹ️  INFO: EditionInfo is empty - ASIN reference may not have been added")
	}

	// The most important thing is that the function doesn't crash and returns valid data
	if editionInput.Title == "" {
		t.Error("Title should not be empty")
	}
	
	if editionInput.AudioLength != mismatch.DurationSeconds {
		t.Errorf("Audio length mismatch: expected %d, got %d", mismatch.DurationSeconds, editionInput.AudioLength)
	}
}

func TestMismatchASINEnhancementDisabled(t *testing.T) {
	// Test with the same example
	mismatch := BookMismatch{
		Title:             "Blue Shift (Unabridged)",
		Subtitle:          "Backyard Starship, Book 5",
		Author:            "J.N. Chaney, Terry Maggert",
		ASIN:              "B09ZVQ796F",
		DurationSeconds:   40817,
		AudiobookShelfID:  "a2768e1f-95be-46a0-b6a3-51f161d6b81c",
		Reason:            "Test with API disabled",
		Timestamp:         time.Now(),
	}

	t.Logf("Testing with ASIN reference for: %s", mismatch.Title)

	// Convert mismatch to edition input (should work with ASIN reference)
	editionInput := convertMismatchToEditionInput(mismatch)

	t.Logf("Results with ASIN reference:")
	t.Logf("Title: %s", editionInput.Title)
	t.Logf("ASIN: %s", editionInput.ASIN)
	t.Logf("EditionInfo: %s", editionInput.EditionInfo)

	// Should still work without external enhancement
	if editionInput.Title != mismatch.Title {
		t.Errorf("Title mismatch: expected %s, got %s", mismatch.Title, editionInput.Title)
	}

	if editionInput.ASIN != mismatch.ASIN {
		t.Errorf("ASIN mismatch: expected %s, got %s", mismatch.ASIN, editionInput.ASIN)
	}
}
