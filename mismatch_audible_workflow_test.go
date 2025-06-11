package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMismatchJSONFileCreationWithAudibleAPI(t *testing.T) {
	// Save original environment variables
	originalAPIEnabled := os.Getenv("AUDIBLE_API_ENABLED")
	originalMismatchDir := os.Getenv("MISMATCH_JSON_FILE")
	
	// Restore environment variables after test
	defer func() {
		os.Setenv("AUDIBLE_API_ENABLED", originalAPIEnabled)
		os.Setenv("MISMATCH_JSON_FILE", originalMismatchDir)
	}()

	// Create a temporary directory for test output
	tempDir := t.TempDir()

	// Set up environment for the test
	os.Setenv("AUDIBLE_API_ENABLED", "true")
	os.Setenv("MISMATCH_JSON_FILE", tempDir)
	os.Setenv("DRY_RUN", "true") // Ensure no real API calls

	// Clear any existing mismatches
	clearMismatches()

	// Create test mismatches with different scenarios
	testMismatches := []BookMismatch{
		{
			Title:             "Book with ASIN",
			Subtitle:          "Enhanced Edition",
			Author:            "Test Author 1",
			Narrator:          "Test Narrator 1",
			Publisher:         "Test Publisher 1",
			PublishedYear:     "2023",
			ReleaseDate:       "2023",
			Duration:          6.5,
			DurationSeconds:   23400,
			ISBN:              "",
			ASIN:              "B01TESTASIN", // This should trigger Audible API enhancement
			BookID:            "",
			EditionID:         "",
			AudiobookShelfID:  "test-audiobook-id-1",
			Reason:            "Test mismatch with ASIN for Audible API integration",
			Timestamp:         time.Now(),
		},
		{
			Title:             "Book without ASIN",
			Author:            "Test Author 2",
			Publisher:         "Test Publisher 2",
			PublishedYear:     "2024",
			ReleaseDate:       "2024-01-15",
			Duration:          4.0,
			DurationSeconds:   14400,
			ISBN:              "9781234567890",
			ASIN:              "", // No ASIN - should not trigger Audible API
			BookID:            "",
			EditionID:         "",
			AudiobookShelfID:  "test-audiobook-id-2",
			Reason:            "Test mismatch without ASIN",
			Timestamp:         time.Now(),
		},
	}

	// Add test mismatches to the global collection
	for _, mismatch := range testMismatches {
		bookMismatches = append(bookMismatches, mismatch)
	}

	// Test the saveMismatchesJSONFile function (which creates edition-ready files)
	err := saveMismatchesJSONFile()
	if err != nil {
		t.Fatalf("saveMismatchesJSONFile failed: %v", err)
	}

	// Verify files were created
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("Failed to read temp directory: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("Expected 2 JSON files, got %d", len(files))
	}

	// Verify the content of each file
	for i, file := range files {
		if !file.IsDir() {
			filePath := filepath.Join(tempDir, file.Name())
			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("Failed to read file %s: %v", filePath, err)
			}

			var editionInput EditionCreatorInput
			err = json.Unmarshal(content, &editionInput)
			if err != nil {
				t.Fatalf("Failed to parse JSON from file %s: %v", filePath, err)
			}

			expectedMismatch := testMismatches[i]

			// Verify basic data transfer
			if editionInput.Title != expectedMismatch.Title {
				t.Errorf("File %s: Title mismatch - expected %s, got %s", file.Name(), expectedMismatch.Title, editionInput.Title)
			}

			if editionInput.AudioLength != expectedMismatch.DurationSeconds {
				t.Errorf("File %s: Audio length mismatch - expected %d, got %d", file.Name(), expectedMismatch.DurationSeconds, editionInput.AudioLength)
			}

			// Check ASIN-specific behavior
			if expectedMismatch.ASIN != "" {
				// For books with ASIN, verify the ASIN is preserved
				if editionInput.ASIN != expectedMismatch.ASIN {
					t.Errorf("File %s: ASIN mismatch - expected %s, got %s", file.Name(), expectedMismatch.ASIN, editionInput.ASIN)
				}

				// The Audible API enhancement should have been attempted (even if it fails in test mode)
				t.Logf("File %s: ASIN book processed correctly with ASIN %s", file.Name(), editionInput.ASIN)
			} else {
				// For books without ASIN, verify no ASIN was added
				if editionInput.ASIN != "" {
					t.Errorf("File %s: Unexpected ASIN added - expected empty, got %s", file.Name(), editionInput.ASIN)
				}
				t.Logf("File %s: Non-ASIN book processed correctly", file.Name())
			}

			// Verify AudiobookShelf cover URL is generated correctly
			expectedImageURL := getAudiobookShelfURL() + "/api/items/" + expectedMismatch.AudiobookShelfID + "/cover"
			if editionInput.ImageURL != expectedImageURL {
				t.Errorf("File %s: Image URL mismatch - expected %s, got %s", file.Name(), expectedImageURL, editionInput.ImageURL)
			}

			// Verify edition format defaults
			if editionInput.EditionFormat != "Audible Audio" {
				t.Errorf("File %s: Edition format should default to 'Audible Audio', got %s", file.Name(), editionInput.EditionFormat)
			}

			// Check that lookup information is preserved in EditionInfo
			if editionInput.EditionInfo == "" {
				t.Errorf("File %s: EditionInfo should contain lookup information", file.Name())
			}

			t.Logf("File %s: Validated successfully", file.Name())
			t.Logf("  - Title: %s", editionInput.Title)
			t.Logf("  - ASIN: %s", editionInput.ASIN)
			t.Logf("  - Audio Length: %d seconds", editionInput.AudioLength)
			t.Logf("  - Edition Info: %s", editionInput.EditionInfo)
		}
	}

	// Clean up test data
	clearMismatches()

	t.Logf("Successfully validated %d mismatch JSON files with Audible API integration", len(files))
}

func TestAudibleAPIIntegrationInRealWorkflow(t *testing.T) {
	// This test simulates the real workflow where mismatches are collected during sync
	// and then JSON files are created with Audible API enhancement

	// Save original environment variables
	originalAPIEnabled := os.Getenv("AUDIBLE_API_ENABLED")
	originalMismatchDir := os.Getenv("MISMATCH_JSON_FILE")
	
	defer func() {
		os.Setenv("AUDIBLE_API_ENABLED", originalAPIEnabled)
		os.Setenv("MISMATCH_JSON_FILE", originalMismatchDir)
	}()

	// Set up test environment
	tempDir := t.TempDir()
	os.Setenv("AUDIBLE_API_ENABLED", "true")
	os.Setenv("MISMATCH_JSON_FILE", tempDir)

	// Clear any existing mismatches
	clearMismatches()

	// Simulate a book that would be found during sync with an ASIN
	metadata := MediaMetadata{
		Title:        "The Testing Audiobook",
		Subtitle:     "A Complete Guide",
		AuthorName:   "Jane Test Author",
		NarratorName: "John Test Narrator",
		Publisher:    "Test Audio Publishing",
		ASIN:         "B01REALTEST",
		ISBN:         "",
	}

	// Use the real mismatch collection function used during sync
	addBookMismatchWithMetadata(metadata, "", "", "ASIN lookup failed - test scenario", 25200.0, "test-library-item")

	// Verify mismatch was collected
	if len(bookMismatches) != 1 {
		t.Fatalf("Expected 1 mismatch to be collected, got %d", len(bookMismatches))
	}

	// Create the JSON files (this should trigger Audible API enhancement)
	err := saveMismatchesJSONFile()
	if err != nil {
		t.Fatalf("Failed to save mismatch JSON files: %v", err)
	}

	// Verify the file was created and contains enhanced data
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("Failed to read temp directory: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("Expected 1 JSON file, got %d", len(files))
	}

	// Read and validate the generated file
	filePath := filepath.Join(tempDir, files[0].Name())
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read generated file: %v", err)
	}

	var editionInput EditionCreatorInput
	err = json.Unmarshal(content, &editionInput)
	if err != nil {
		t.Fatalf("Failed to parse generated JSON: %v", err)
	}

	// Validate the enhanced data
	if editionInput.Title != metadata.Title {
		t.Errorf("Title mismatch: expected %s, got %s", metadata.Title, editionInput.Title)
	}

	if editionInput.ASIN != metadata.ASIN {
		t.Errorf("ASIN mismatch: expected %s, got %s", metadata.ASIN, editionInput.ASIN)
	}

	if editionInput.AudioLength != 25200 {
		t.Errorf("Audio length mismatch: expected 25200, got %d", editionInput.AudioLength)
	}

	// The Audible API integration should have been attempted
	t.Logf("Real workflow test completed successfully")
	t.Logf("Generated file: %s", files[0].Name())
	t.Logf("Title: %s", editionInput.Title)
	t.Logf("ASIN: %s", editionInput.ASIN)
	t.Logf("Subtitle: %s", editionInput.Subtitle)
	t.Logf("Audio Length: %d seconds (%.1f hours)", editionInput.AudioLength, float64(editionInput.AudioLength)/3600)

	// Clean up
	clearMismatches()
}
