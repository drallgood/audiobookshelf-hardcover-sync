package mismatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBookMismatchDurationSeconds(t *testing.T) {
	tests := []struct {
		durationHours     float64
		expectedSeconds   int
		expectedFormatted string
	}{
		{0, 0, "0h 0m 0s"},
		{1.0, 3600, "1h 0m 0s"},
		{1.5, 5400, "1h 30m 0s"},
		{2.25, 8100, "2h 15m 0s"},
		{8.2, 29520, "8h 12m 0s"},        // 8.2 * 3600 = 29520s = 8h 12m 0s
		{12.9, 46440, "12h 54m 0s"},      // 12.9 * 3600 = 46440s = 12h 54m 0s
		{24.5, 88200, "24h 30m 0s"},      // 24.5 * 3600 = 88200s = 24h 30m 0s
		{18.758333, 67530, "18h 45m 30s"}, // 18.758333 * 3600 = 67530s = 18h 45m 30s
	}

	for _, test := range tests {
		t.Run(test.expectedFormatted, func(t *testing.T) {
			// Create a BookMismatch with the duration
			mismatch := BookMismatch{
				Title:           "Test Book",
				Duration:        test.durationHours,
				DurationSeconds: int(test.durationHours*3600 + 0.5), // Same calculation as in AddWithMetadata
			}

			// Verify duration in seconds calculation
			if mismatch.DurationSeconds != test.expectedSeconds {
				t.Errorf("Duration seconds calculation: got %d, expected %d", mismatch.DurationSeconds, test.expectedSeconds)
			}

			// Format the duration for display
			formatted := formatDuration(test.durationHours)
			if formatted != test.expectedFormatted {
				t.Errorf("Formatted duration: got %s, expected %s", formatted, test.expectedFormatted)
			}
		})
	}
}

// formatDuration formats a duration in hours to a human-readable string (e.g., "1h 30m 0s")
func formatDuration(hours float64) string {
	// Calculate total seconds with proper rounding
	seconds := int(hours*3600 + 0.5)
	
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	return fmt.Sprintf("%dh %dm %ds", h, m, s)
}

func TestMismatchJSONOutput(t *testing.T) {
	// Clear any existing mismatches first
	Clear()

	// Add a test mismatch
	timestamp := time.Now().Unix()
	Add(BookMismatch{
		Title:           "Test Book",
		Author:          "Test Author",
		Duration:        18.758333,
		DurationSeconds: 67530,
		ISBN:           "1234567890",
		Reason:         "Test reason",
		Timestamp:      timestamp,
		Attempts:       1,
	})


	// Export to JSON
	jsonStr, err := ExportJSON()
	if err != nil {
		t.Fatalf("ExportJSON failed: %v", err)
	}

	// Parse the JSON to verify structure
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	// Verify the structure and values
	mismatches, ok := data["mismatches"].([]interface{})
	if !ok || len(mismatches) != 1 {
		t.Errorf("Expected 1 mismatch, got %d", len(mismatches))
	}

	m := mismatches[0].(map[string]interface{})
	if m["title"] != "Test Book" {
		t.Errorf("Unexpected title: %v", m["title"])
	}
	if m["author"] != "Test Author" {
		t.Errorf("Unexpected author: %v", m["author"])
	}
	if m["duration"] != 18.758333 {
		t.Errorf("Unexpected duration: %v", m["duration"])
	}
	if m["duration_seconds"] != 67530.0 {
		t.Errorf("Unexpected duration_seconds: %v", m["duration_seconds"])
	}
	if m["isbn"] != "1234567890" {
		t.Errorf("Unexpected isbn: %v", m["isbn"])
	}
	if m["reason"] != "Test reason" {
		t.Errorf("Unexpected reason: %v", m["reason"])
	}
	if int64(m["timestamp"].(float64)) != timestamp {
		t.Errorf("Unexpected timestamp: %v", m["timestamp"])
	}
	if m["attempts"] != 1.0 {
		t.Errorf("Unexpected attempts: %v", m["attempts"])
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"My Book: A Title", "My Book A Title"},
		{"Book/With\\Slash*And?Invalid<Chars>", "Book With SlashAndInvalidChars"},
		{"Normal Book Title 123", "Normal Book Title 123"},
		{"", ""},
		{"   ", ""},
		{"Test!@#$%^&*()_+{}|:<>?[];',./\"", "Test^and()_{} [];,"},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result := SanitizeFilename(test.input)
			if result != test.expected {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", test.input, result, test.expected)
			}
		})
	}
}

func TestSaveMismatchesJSONFileIndividual(t *testing.T) {
	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "mismatch_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to clean up temp dir: %v", err)
		}
	}()

	// Clear any existing mismatches
	Clear()

	// Add test mismatches
	now := time.Now()
	mismatches := []BookMismatch{
		{
			Title:     "Test Book 1",
			Author:    "Author 1",
			ISBN:      "1234567890",
			Reason:    "test reason 1",
			Timestamp: now.Unix(),
			CreatedAt: now,
		},
		{
			Title:     "Test Book 2",
			Author:    "Author 2",
			ISBN:      "0987654321",
			Reason:    "test reason 2",
			Timestamp: now.Add(-time.Hour).Unix(),
			CreatedAt: now.Add(-time.Hour),
		},
	}

	for _, m := range mismatches {
		Add(m)
	}

	// Save mismatches to files
	if err := SaveToFile(tempDir); err != nil {
		t.Fatalf("SaveToFile failed: %v", err)
	}

	// Verify files were created
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("Failed to read temp dir: %v", err)
	}

	if len(files) != len(mismatches) {
		t.Fatalf("Expected %d files, got %d", len(mismatches), len(files))
	}

	// Verify file contents
	for i, file := range files {
		filePath := filepath.Join(tempDir, file.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("Failed to read file %s: %v", filePath, err)
		}

		var mismatch BookMismatch
		if err := json.Unmarshal(data, &mismatch); err != nil {
			t.Fatalf("Failed to unmarshal %s: %v", filePath, err)
		}

		expected := mismatches[i]
		if mismatch.Title != expected.Title ||
			mismatch.Author != expected.Author ||
			mismatch.ISBN != expected.ISBN ||
			mismatch.Reason != expected.Reason ||
			mismatch.Timestamp != expected.Timestamp {
			t.Errorf("Mismatch in file %s: got %+v, want %+v", filePath, mismatch, expected)
		}
	}
}
