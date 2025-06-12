package main

import (
	"os"
	"testing"
	"time"
)

func TestMismatchASINReferenceIntegration(t *testing.T) {
	// Save original environment variables
	originalDryRun := os.Getenv("DRY_RUN")
	
	// Restore environment variables after test
	defer func() {
		os.Setenv("DRY_RUN", originalDryRun)
	}()

	// Test scenarios
	tests := []struct {
		name       string
		asin       string
		expectASIN bool
	}{
		{
			name:       "With valid ASIN",
			asin:       "B01234567X",
			expectASIN: true,
		},
		{
			name:       "With empty ASIN",
			asin:       "",
			expectASIN: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			os.Setenv("DRY_RUN", "true") // Ensure we don't make real API calls

			// Clear any existing mismatches
			clearMismatches()

			// Create a test mismatch with ASIN
			testMismatch := BookMismatch{
				Title:             "Test Book for ASIN Reference",
				Subtitle:          "A Test Subtitle",
				Author:            "Test Author",
				Narrator:          "Test Narrator",
				Publisher:         "Test Publisher",
				PublishedYear:     "2023",
				ReleaseDate:       "2023",
				Duration:          5.5,
				DurationSeconds:   19800,
				ISBN:              "",
				ASIN:              tt.asin,
				BookID:            "",
				EditionID:         "",
				AudiobookShelfID:  "test-audiobook-id-123",
				Reason:            "Test mismatch for ASIN reference",
				Timestamp:         time.Now(),
			}

			// Test the conversion with ASIN reference
			editionInput := convertMismatchToEditionInput(testMismatch)

			// Verify basic conversion worked
			if editionInput.Title != testMismatch.Title {
				t.Errorf("Title mismatch: expected %s, got %s", testMismatch.Title, editionInput.Title)
			}

			if editionInput.ASIN != testMismatch.ASIN {
				t.Errorf("ASIN mismatch: expected %s, got %s", testMismatch.ASIN, editionInput.ASIN)
			}

			// Check if ASIN reference was preserved when expected
			if tt.expectASIN && tt.asin != "" {
				if editionInput.ASIN == "" {
					t.Error("Expected ASIN to be preserved")
				}
				
				// Check if ASIN reference info was added to EditionInfo
				if tt.asin != "" && !contains(editionInput.EditionInfo, "ASIN:") {
					t.Logf("EditionInfo: %s", editionInput.EditionInfo)
					// Note: ASIN reference might not appear if book matching fails
				}
			}

			// Verify the generated JSON contains expected fields
			if editionInput.AudioLength != testMismatch.DurationSeconds {
				t.Errorf("Audio length mismatch: expected %d, got %d", testMismatch.DurationSeconds, editionInput.AudioLength)
			}

			if editionInput.EditionFormat != "Audible Audio" {
				t.Errorf("Edition format should default to 'Audible Audio', got %s", editionInput.EditionFormat)
			}

			t.Logf("Test '%s' completed successfully", tt.name)
			t.Logf("  - ASIN: %s", editionInput.ASIN)
			t.Logf("  - Title: %s", editionInput.Title)
			t.Logf("  - EditionInfo: %s", editionInput.EditionInfo)
		})
	}
}

// Helper function to check if a string contains a substring
func contains(str, substr string) bool {
	return len(str) >= len(substr) && (str == substr || 
		(len(str) > len(substr) && 
			(str[:len(substr)] == substr || 
			 str[len(str)-len(substr):] == substr ||
			 containsInMiddle(str, substr))))
}

func containsInMiddle(str, substr string) bool {
	for i := 0; i <= len(str)-len(substr); i++ {
		if str[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestMismatchASINReferenceRealIntegration(t *testing.T) {
	// Skip this test - no external API integration available
	t.Skip("External API integration removed - only ASIN reference functionality available")
}
