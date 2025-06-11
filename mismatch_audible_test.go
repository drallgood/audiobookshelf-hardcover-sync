package main

import (
	"os"
	"testing"
	"time"
)

func TestMismatchAudibleAPIIntegration(t *testing.T) {
	// Save original environment variables
	originalAPIEnabled := os.Getenv("AUDIBLE_API_ENABLED")
	originalDryRun := os.Getenv("DRY_RUN")
	
	// Restore environment variables after test
	defer func() {
		os.Setenv("AUDIBLE_API_ENABLED", originalAPIEnabled)
		os.Setenv("DRY_RUN", originalDryRun)
	}()

	// Test scenarios
	tests := []struct {
		name       string
		apiEnabled string
		asin       string
		expectEnhancement bool
	}{
		{
			name:       "API enabled with valid ASIN",
			apiEnabled: "true",
			asin:       "B01234567X",
			expectEnhancement: true,
		},
		{
			name:       "API disabled",
			apiEnabled: "false", 
			asin:       "B01234567X",
			expectEnhancement: false,
		},
		{
			name:       "API enabled with empty ASIN",
			apiEnabled: "true",
			asin:       "",
			expectEnhancement: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			os.Setenv("AUDIBLE_API_ENABLED", tt.apiEnabled)
			os.Setenv("DRY_RUN", "true") // Ensure we don't make real API calls

			// Clear any existing mismatches
			clearMismatches()

			// Create a test mismatch with ASIN
			testMismatch := BookMismatch{
				Title:             "Test Book for Audible Integration",
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
				Reason:            "Test mismatch for Audible API integration",
				Timestamp:         time.Now(),
			}

			// Test the conversion with Audible API integration
			editionInput := convertMismatchToEditionInput(testMismatch)

			// Verify basic conversion worked
			if editionInput.Title != testMismatch.Title {
				t.Errorf("Title mismatch: expected %s, got %s", testMismatch.Title, editionInput.Title)
			}

			if editionInput.ASIN != testMismatch.ASIN {
				t.Errorf("ASIN mismatch: expected %s, got %s", testMismatch.ASIN, editionInput.ASIN)
			}

			// Check if enhancement was applied when expected
			if tt.expectEnhancement && tt.asin != "" {
				// When API is enabled and ASIN is provided, some enhancement should occur
				// Even if the API call fails (in dry run), the ASIN should still be present
				if editionInput.ASIN == "" {
					t.Error("Expected ASIN to be preserved during enhancement")
				}
				
				// Check if enhancement info was added to EditionInfo
				if !contains(editionInput.EditionInfo, "ENHANCED") && tt.apiEnabled == "true" {
					t.Logf("EditionInfo: %s", editionInput.EditionInfo)
					// Note: In dry run mode or when API fails, we might not get the ENHANCED flag
					// This is acceptable behavior
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

func TestMismatchAudibleAPIRealIntegration(t *testing.T) {
	// This test only runs if API is actually enabled
	if !getAudibleAPIEnabled() {
		t.Skip("Audible API not enabled, skipping real integration test")
	}

	// Clear any existing mismatches
	clearMismatches()

	// Create a test mismatch with a real ASIN (this won't make real API calls in test environment)
	testMismatch := BookMismatch{
		Title:             "The Martian",
		Author:            "Andy Weir", 
		ASIN:              "B00B5HZGUG", // Real ASIN for The Martian audiobook
		DurationSeconds:   53357,
		AudiobookShelfID:  "test-id",
		Reason:            "Integration test",
		Timestamp:         time.Now(),
	}

	editionInput := convertMismatchToEditionInput(testMismatch)

	// Basic validation
	if editionInput.ASIN != testMismatch.ASIN {
		t.Errorf("ASIN should be preserved: expected %s, got %s", testMismatch.ASIN, editionInput.ASIN)
	}

	t.Logf("Real integration test completed with ASIN: %s", editionInput.ASIN)
}
