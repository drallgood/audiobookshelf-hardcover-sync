package testutils

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMismatchJSONFileCreationWithASINReference(t *testing.T) {
	// Save original environment variables
	originalMismatchDir := os.Getenv("MISMATCH_JSON_FILE")
	
	// Restore environment variables after test
	defer func() {
		os.Setenv("MISMATCH_JSON_FILE", originalMismatchDir)
	}()

	// Create a temporary directory for test output
	tempDir := t.TempDir()

	// Set up environment for the test
	os.Setenv("MISMATCH_JSON_FILE", tempDir)
	os.Setenv("DRY_RUN", "true") // Ensure no real API calls

	// Clear any existing mismatches
	clearMismatches()

	// Create test mismatches with different scenarios
	timestamp1 := time.Now().Unix()
	timestamp2 := time.Now().Add(-1 * time.Hour).Unix()
	testMismatches := []BookMismatch{
		{
			Title:     "Book with ASIN",
			Author:    "Test Author 1",
			ISBN:      "",
			ASIN:      "B01TESTASIN", // This should preserve ASIN reference
			Reason:    "Test mismatch with ASIN for reference",
			Timestamp: timestamp1,
			Attempts:  0,
			Metadata:  "test-audiobook-id-1",
		},
		{
			Title:     "Book without ASIN",
			Author:    "Test Author 2",
			ISBN:      "9781234567890",
			ASIN:      "", // No ASIN - no reference added
			Reason:    "Test mismatch without ASIN",
			Timestamp: timestamp2,
			Attempts:  0,
			Metadata:  "test-audiobook-id-2",
		},
	}

	// Add test mismatches to the global collection
	for _, mismatch := range testMismatches {
		bookMismatches = append(bookMismatches, mismatch)
	}

	// Test the saveMismatchesJSONFile function (which creates edition-ready files)
	err := saveMismatchesJSONFile(tempDir)
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

			// Check ASIN-specific behavior
			if expectedMismatch.ASIN != "" {
				// For books with ASIN, verify the ASIN is preserved
				if editionInput.ASIN != expectedMismatch.ASIN {
					t.Errorf("File %s: ASIN mismatch - expected %s, got %s", file.Name(), expectedMismatch.ASIN, editionInput.ASIN)
				}

				// The ASIN reference should have been preserved
				t.Logf("File %s: ASIN book processed correctly with ASIN %s", file.Name(), editionInput.ASIN)
			} else {
				// For books without ASIN, verify no ASIN was added
				if editionInput.ASIN != "" {
					t.Errorf("File %s: Unexpected ASIN added - expected empty, got %s", file.Name(), editionInput.ASIN)
				}
				t.Logf("File %s: Non-ASIN book processed correctly", file.Name())
			}

			// Verify AudiobookShelf cover URL is generated correctly if metadata contains ID
			if expectedMismatch.Metadata != "" {
				expectedImageURL := getAudiobookShelfURL() + "/api/items/" + expectedMismatch.Metadata + "/cover"
				if editionInput.ImageURL != expectedImageURL {
					t.Errorf("File %s: Image URL mismatch - expected %s, got %s", file.Name(), expectedImageURL, editionInput.ImageURL)
				}
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
			if editionInput.AudioLength > 0 {
				t.Logf("  - Audio Length: %d seconds", editionInput.AudioLength)
			}
			t.Logf("  - Edition Info: %s", editionInput.EditionInfo)
		}
	}

	// Clean up test data
	clearMismatches()

	t.Logf("Successfully validated %d mismatch JSON files with ASIN reference", len(files))
}

func TestASINReferenceInRealWorkflow(t *testing.T) {
	// This test simulates the real workflow where mismatches are collected during sync
	// and then JSON files are created with ASIN reference

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
	timestamp := time.Now().Unix()
	testMismatch := BookMismatch{
		Title:     "The Testing Audiobook",
		Author:    "Jane Test Author",
		ISBN:      "",
		ASIN:      "B01REALTEST",
		Reason:    "ASIN lookup failed - test scenario",
		Timestamp: timestamp,
		Attempts:  0,
		Metadata:  "test-library-item",
	}

	// Add the test mismatch
	bookMismatches = append(bookMismatches, testMismatch)

	// Verify mismatch was collected
	if len(bookMismatches) != 1 {
		t.Fatalf("Expected 1 mismatch to be collected, got %d", len(bookMismatches))
	}

	// Create the JSON files (this should preserve ASIN reference)
	err := saveMismatchesJSONFile(tempDir)
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
	expectedTitle := "The Testing Audiobook"
	expectedASIN := "B01REALTEST"

	if editionInput.Title != expectedTitle {
		t.Errorf("Title mismatch: expected %s, got %s", expectedTitle, editionInput.Title)
	}

	if editionInput.ASIN != expectedASIN {
		t.Errorf("ASIN mismatch: expected %s, got %s", expectedASIN, editionInput.ASIN)
	}

	if editionInput.AudioLength != 25200 {
		t.Errorf("Audio length mismatch: expected 25200, got %d", editionInput.AudioLength)
	}

	// The ASIN reference should have been preserved
	t.Logf("Real workflow test completed successfully")
	t.Logf("Generated file: %s", files[0].Name())
	t.Logf("Title: %s", editionInput.Title)
	t.Logf("ASIN: %s", editionInput.ASIN)
	if editionInput.Subtitle != "" {
		t.Logf("Subtitle: %s", editionInput.Subtitle)
	}
	if editionInput.AudioLength > 0 {
		t.Logf("Audio Length: %d seconds (%.1f hours)", editionInput.AudioLength, float64(editionInput.AudioLength)/3600)
	}

	// Clean up
	clearMismatches()
}
