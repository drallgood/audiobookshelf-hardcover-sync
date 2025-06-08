package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEditionReadyMismatchFiles(t *testing.T) {
	// Create a temporary directory for test output
	tempDir := "/tmp/mismatch_test"
	defer os.RemoveAll(tempDir)

	// Clear any existing mismatches
	bookMismatches = []BookMismatch{}

	// Add a test mismatch with metadata similar to the "Accelerate" example
	testMismatch := BookMismatch{
		Title:           "Accelerate: Building and Scaling High Performing Technology Organizations",
		Subtitle:        "The Science of Lean Software and DevOps: Building and Scaling High Performing Technology Organizations",
		Author:          "Jez Humble, Gene Kim, Nicole Forsgren PhD",
		Narrator:        "Nicole Forsgren",
		Publisher:       "IT Revolution Press",
		PublishedYear:   "2018",
		ReleaseDate:     "2018",
		Duration:        4.982984719166667,
		DurationSeconds: 17939,
		ISBN:            "",
		ASIN:            "B07BLZDZFQ",
		BookID:          "",
		EditionID:       "",
		Reason:          "Book lookup failed - not found in Hardcover database using ASIN B07BLZDZFQ, ISBN , or title/author search",
		Timestamp:       time.Now(),
	}

	bookMismatches = append(bookMismatches, testMismatch)

	// Test the edition-ready file creation
	err := createEditionReadyMismatchFiles(tempDir)
	if err != nil {
		t.Fatalf("Failed to create edition-ready files: %v", err)
	}

	// Check that the file was created
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("Failed to read temp directory: %v", err)
	}

	if len(files) != 1 {
		t.Fatalf("Expected 1 file, got %d", len(files))
	}

	// Read and parse the generated file
	filePath := filepath.Join(tempDir, files[0].Name())
	fileContent, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read generated file: %v", err)
	}

	// Parse as EditionCreatorInput
	var editionInput EditionCreatorInput
	err = json.Unmarshal(fileContent, &editionInput)
	if err != nil {
		t.Fatalf("Failed to parse edition input JSON: %v", err)
	}

	// Verify the conversion
	if editionInput.Title != testMismatch.Title {
		t.Errorf("Title mismatch: expected %s, got %s", testMismatch.Title, editionInput.Title)
	}

	if editionInput.ASIN != testMismatch.ASIN {
		t.Errorf("ASIN mismatch: expected %s, got %s", testMismatch.ASIN, editionInput.ASIN)
	}

	if editionInput.AudioLength != testMismatch.DurationSeconds {
		t.Errorf("Audio length mismatch: expected %d, got %d", testMismatch.DurationSeconds, editionInput.AudioLength)
	}

	if editionInput.EditionFormat != "Audible Audio" {
		t.Errorf("Edition format mismatch: expected 'Audible Audio', got %s", editionInput.EditionFormat)
	}

	// Check that manual steps are included in EditionInfo
	expectedText := "MANUAL LOOKUP REQUIRED:"
	if editionInput.EditionInfo == "" || len(editionInput.EditionInfo) < len(expectedText) {
		t.Errorf("EditionInfo should contain manual lookup instructions, got: %s", editionInput.EditionInfo)
	}

	// Check that Audible image URL is generated correctly
	expectedImageURL := "https://m.media-amazon.com/images/I/51B07BLZDZFQ._SL500_.jpg"
	if editionInput.ImageURL != expectedImageURL {
		t.Errorf("Image URL mismatch: expected %s, got %s", expectedImageURL, editionInput.ImageURL)
	}

	t.Logf("✅ Edition-ready file generated successfully: %s", files[0].Name())
	t.Logf("📄 File content (first 500 chars): %s", string(fileContent[:minInt(500, len(fileContent))]))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
