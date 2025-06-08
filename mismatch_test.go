package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBookMismatchDurationSeconds(t *testing.T) {
	tests := []struct {
		durationHours    float64
		expectedSeconds  int
		expectedFormatted string
	}{
		{0, 0, "0h 0m 0s"},
		{1.0, 3600, "1h 00m 00s"},
		{1.5, 5400, "1h 30m 00s"},
		{2.25, 8100, "2h 15m 00s"},
		{8.2, 29520, "8h 12m 00s"},
		{12.9, 46440, "12h 54m 00s"},
		{24.5, 88200, "24h 30m 00s"},
		{18.758333, 67530, "18h 45m 30s"}, // 18h 45m 30s = 67530 seconds
	}

	for _, test := range tests {
		t.Run(test.expectedFormatted, func(t *testing.T) {
			// Create a BookMismatch with the duration
			mismatch := BookMismatch{
				Title:    "Test Book",
				Duration: test.durationHours,
				DurationSeconds: int(test.durationHours*3600 + 0.5), // Same calculation as in addBookMismatchWithMetadata
			}

			// Verify duration in seconds calculation
			if mismatch.DurationSeconds != test.expectedSeconds {
				t.Errorf("Duration seconds calculation: got %d, expected %d", mismatch.DurationSeconds, test.expectedSeconds)
			}

			// Verify formatted duration matches expected
			formatted := formatDuration(test.durationHours)
			if formatted != test.expectedFormatted {
				t.Errorf("Formatted duration: got %s, expected %s", formatted, test.expectedFormatted)
			}
		})
	}
}

func TestMismatchJSONOutput(t *testing.T) {
	// Create a sample mismatch
	mismatch := BookMismatch{
		Title:           "Test Audiobook",
		Author:          "Test Author",
		Duration:        18.758333, // ~18h 45m 30s
		DurationSeconds: 67530,
		ISBN:            "1234567890",
		Publisher:       "Test Publisher",
		BookID:          "hc-123",
	}

	mismatches := []BookMismatch{mismatch}

	// Test JSON marshaling
	jsonData, err := json.MarshalIndent(mismatches, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal JSON: %v", err)
	}

	jsonString := string(jsonData)

	// Verify JSON contains all expected fields
	expectedFields := []string{
		`"Title": "Test Audiobook"`,
		`"Author": "Test Author"`,
		`"Duration": 18.758333`,
		`"DurationSeconds": 67530`,
		`"ISBN": "1234567890"`,
		`"Publisher": "Test Publisher"`,
		`"BookID": "hc-123"`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(jsonString, field) {
			t.Errorf("JSON output missing expected field: %s\nActual JSON: %s", field, jsonString)
		}
	}

	t.Logf("Generated JSON:\n%s", jsonString)
}

func TestExportMismatchesJSON(t *testing.T) {
	// Clear any existing mismatches first
	clearMismatches()

	// Add sample mismatches to the global collection
	bookMismatches = []BookMismatch{
		{
			Title:           "Book One",
			Author:          "Author One", 
			Duration:        5.5,
			DurationSeconds: 19800, // 5.5 * 3600
			ISBN:            "111",
			Publisher:       "Publisher One",
			BookID:          "hc-1",
			Reason:          "Test reason 1",
			Timestamp:       time.Now(),
		},
		{
			Title:           "Book Two",
			Author:          "Author Two",
			Duration:        12.25,
			DurationSeconds: 44100, // 12.25 * 3600
			ISBN:            "222", 
			Publisher:       "Publisher Two",
			BookID:          "hc-2",
			Reason:          "Test reason 2",
			Timestamp:       time.Now(),
		},
	}

	// Test the exportMismatchesJSON function
	jsonOutput, err := exportMismatchesJSON()
	if err != nil {
		t.Fatalf("exportMismatchesJSON failed: %v", err)
	}

	// Verify it's valid JSON
	var result []BookMismatch
	err = json.Unmarshal([]byte(jsonOutput), &result)
	if err != nil {
		t.Fatalf("exportMismatchesJSON produced invalid JSON: %v", err)
	}

	// Verify data integrity
	if len(result) != 2 {
		t.Errorf("Expected 2 books in JSON output, got %d", len(result))
	}

	if result[0].DurationSeconds != 19800 {
		t.Errorf("First book duration seconds: got %d, expected 19800", result[0].DurationSeconds)
	}

	if result[1].DurationSeconds != 44100 {
		t.Errorf("Second book duration seconds: got %d, expected 44100", result[1].DurationSeconds)
	}

	// Verify that duration in seconds is correctly calculated
	expectedFirstSeconds := 19800  // 5.5 hours * 3600 seconds/hour
	expectedSecondSeconds := 44100 // 12.25 hours * 3600 seconds/hour
	
	if result[0].DurationSeconds != expectedFirstSeconds {
		t.Errorf("First book duration seconds calculation: got %d, expected %d", result[0].DurationSeconds, expectedFirstSeconds)
	}
	
	if result[1].DurationSeconds != expectedSecondSeconds {
		t.Errorf("Second book duration seconds calculation: got %d, expected %d", result[1].DurationSeconds, expectedSecondSeconds)
	}

	// Clean up
	clearMismatches()

	t.Logf("Exported JSON:\n%s", jsonOutput)
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Simple Title", "Simple_Title"},
		{"Title with (Special) Characters!", "Title_with__Special__Characters_"},
		{"Title: The Sequel", "Title__The_Sequel"},
		{"Book & Author", "Book___Author"},
		{"Numbers 123", "Numbers_123"},
		{"Hyphen-ed Book", "Hyphen-ed_Book"},
		{"File.ext", "File.ext"},
		{"", ""},
		{"   ", "___"},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result := sanitizeFilename(test.input)
			if result != test.expected {
				t.Errorf("sanitizeFilename(%q) = %q; expected %q", test.input, result, test.expected)
			}
		})
	}
}

func TestSaveMismatchesJSONFileIndividual(t *testing.T) {
	// Clear any existing mismatches first
	clearMismatches()

	// Create a temporary directory for testing
	tempDir := t.TempDir()
	
	// Set up environment variable for testing
	originalEnv := os.Getenv("MISMATCH_JSON_FILE")
	os.Setenv("MISMATCH_JSON_FILE", tempDir)
	defer os.Setenv("MISMATCH_JSON_FILE", originalEnv)

	// Add sample mismatches to the global collection
	bookMismatches = []BookMismatch{
		{
			Title:           "Test Book One",
			Author:          "Author One",
			Duration:        5.5,
			DurationSeconds: 19800,
			ISBN:            "111",
			Reason:          "Test reason 1",
			Timestamp:       time.Now(),
		},
		{
			Title:           "Book: Special Characters!",
			Author:          "Author Two",
			Duration:        12.25,
			DurationSeconds: 44100,
			ISBN:            "222",
			Reason:          "Test reason 2",
			Timestamp:       time.Now(),
		},
	}

	// Test the saveMismatchesJSONFile function
	err := saveMismatchesJSONFile()
	if err != nil {
		t.Fatalf("saveMismatchesJSONFile failed: %v", err)
	}

	// Verify individual files were created
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("Failed to read temp directory: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("Expected 2 JSON files, got %d", len(files))
	}

	// Check that files have expected names
	expectedFiles := []string{"001_Test_Book_One.json", "002_Book__Special_Characters_.json"}
	actualFiles := make([]string, len(files))
	for i, file := range files {
		actualFiles[i] = file.Name()
	}

	for _, expected := range expectedFiles {
		found := false
		for _, actual := range actualFiles {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected file %s not found. Actual files: %v", expected, actualFiles)
		}
	}

	// Verify file contents (now in EditionCreatorInput format)
	firstFilePath := filepath.Join(tempDir, "001_Test_Book_One.json")
	content, err := os.ReadFile(firstFilePath)
	if err != nil {
		t.Fatalf("Failed to read first file: %v", err)
	}

	var result EditionCreatorInput
	err = json.Unmarshal(content, &result)
	if err != nil {
		t.Fatalf("Failed to unmarshal JSON from file: %v", err)
	}

	if result.Title != "Test Book One" {
		t.Errorf("Expected title 'Test Book One', got %s", result.Title)
	}

	if result.AudioLength != 19800 {
		t.Errorf("Expected duration seconds 19800, got %d", result.AudioLength)
	}

	// Clean up
	clearMismatches()

	t.Logf("Successfully created %d individual JSON files in %s", len(files), tempDir)
}
