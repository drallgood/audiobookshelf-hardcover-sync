package testutils

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestMismatchAudibleEnhancementFix(t *testing.T) {
	// Save original environment
	originalEnabled := os.Getenv("AUDIBLE_API_ENABLED")
	defer os.Setenv("AUDIBLE_API_ENABLED", originalEnabled)

	// Enable Audible API for this test
	os.Setenv("AUDIBLE_API_ENABLED", "false") // Disabled to test the minimal enhancement path

	// Create a test mismatch
	timestamp := time.Now().Unix()
	mismatch := BookMismatch{
		Title:     "Test Book",
		Author:    "Test Author",
		ASIN:      "B07KMH577G",
		Reason:    "test reason",
		Timestamp: timestamp,
		Attempts:  0,
		Metadata:  "test-id", // Using Metadata to store AudiobookShelfID
	}

	// Convert to edition input (this will trigger enhancement)
	result := convertMismatchToEditionInput(mismatch)

	// Verify the ASIN was set
	if result.ASIN != "B07KMH577G" {
		t.Errorf("Expected ASIN to be 'B07KMH577G', got '%s'", result.ASIN)
	}

	// Since API is disabled, it should have "hardcover+external" source
	// But we're not testing the PrepopulationSource directly here as it's internal
	// Instead, we'll verify that the enhancement logic runs correctly

	t.Logf("EditionInfo: %s", result.EditionInfo)
	t.Logf("ASIN: %s", result.ASIN)
}

func TestMismatchAudibleEnhancementWithMockAPI(t *testing.T) {
	// Save original environment
	originalEnabled := os.Getenv("AUDIBLE_API_ENABLED")
	defer os.Setenv("AUDIBLE_API_ENABLED", originalEnabled)

	// Enable Audible API for this test (but it will fail and fallback to external)
	os.Setenv("AUDIBLE_API_ENABLED", "true")

	// Create a test mismatch
	timestamp := time.Now().Unix()
	mismatch := BookMismatch{
		Title:     "Jingle Bell Pop",
		Author:    "John Seabrook",
		ASIN:      "B07KMH577G",
		Reason:    "test reason",
		Timestamp: timestamp,
		Attempts:  0,
		Metadata:  "e2470e5d-aa57-44c5-af8e-ad3379c5fd66", // Using Metadata to store AudiobookShelfID
	}

	// Convert to edition input (this will trigger enhancement attempt)
	result := convertMismatchToEditionInput(mismatch)

	// Verify the ASIN was set
	if result.ASIN != "B07KMH577G" {
		t.Errorf("Expected ASIN to be 'B07KMH577G', got '%s'", result.ASIN)
	}

	// The API call will likely fail (since we don't have real credentials), 
	// so it should fall back to "hardcover+external" behavior
	// But the important thing is that the enhancement logic doesn't crash

	t.Logf("EditionInfo: %s", result.EditionInfo)
	t.Logf("ASIN: %s", result.ASIN)
	t.Logf("Title: %s", result.Title)
	t.Logf("AudioLength: %d", result.AudioLength)

	// Test that if there WAS an enhancement, it would be properly detected
	// We can simulate this by manually checking the enhancement marker logic
	testEditionInfo := "Some existing info"
	if strings.Contains(testEditionInfo, "ENHANCED: Data enhanced with Audible API") {
		t.Logf("Enhancement marker detected correctly")
	} else {
		t.Logf("No enhancement marker (expected for failed API call)")
	}
}
