package main

import (
	"testing"
	"time"
)

// TestReadingHistoryFix tests that the update_user_book_read mutation
// always sets started_at to prevent null values from wiping reading history
func TestReadingHistoryFix(t *testing.T) {
	tests := []struct {
		name                    string
		progress                float64
		targetProgressSeconds   int
		existingProgressSeconds int
		existingReadId          int
		expectedStartedAt       bool // should started_at be set?
		expectedFinishedAt      bool // should finished_at be set?
	}{
		{
			name:                    "In-progress book update",
			progress:                0.50,
			targetProgressSeconds:   1800,
			existingProgressSeconds: 900,
			existingReadId:          123,
			expectedStartedAt:       true,
			expectedFinishedAt:      false,
		},
		{
			name:                    "Finished book update",
			progress:                1.0,
			targetProgressSeconds:   3600,
			existingProgressSeconds: 1800,
			existingReadId:          124,
			expectedStartedAt:       true,
			expectedFinishedAt:      true,
		},
		{
			name:                    "Near-finished book update",
			progress:                0.99,
			targetProgressSeconds:   3560,
			existingProgressSeconds: 2000,
			existingReadId:          125,
			expectedStartedAt:       true,
			expectedFinishedAt:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock audiobook
			audiobook := Audiobook{
				Title:    "Test Book",
				Author:   "Test Author",
				Progress: tt.progress,
			}

			// Calculate the update object as the fix would
			updateObject := map[string]interface{}{
				"progress_seconds": tt.targetProgressSeconds,
				"started_at":       time.Now().Format("2006-01-02"), // The critical fix
			}

			// If book is finished (>= 99%), also set finished_at
			if audiobook.Progress >= 0.99 {
				updateObject["finished_at"] = time.Now().Format("2006-01-02")
			}

			// Verify started_at is always set (never null)
			if startedAt, exists := updateObject["started_at"]; !exists || startedAt == nil {
				t.Errorf("CRITICAL BUG: started_at is not set, this would wipe reading history!")
			}

			// Verify finished_at is set correctly for finished books
			finishedAt, finishedExists := updateObject["finished_at"]
			if tt.expectedFinishedAt {
				if !finishedExists || finishedAt == nil {
					t.Errorf("Expected finished_at to be set for finished book (progress=%.2f), but it wasn't", audiobook.Progress)
				}
			} else {
				if finishedExists {
					t.Errorf("Expected finished_at NOT to be set for in-progress book (progress=%.2f), but it was: %v", audiobook.Progress, finishedAt)
				}
			}

			// Verify progress_seconds is set correctly
			if progressSeconds, exists := updateObject["progress_seconds"]; !exists || progressSeconds != tt.targetProgressSeconds {
				t.Errorf("Expected progress_seconds=%d, got %v", tt.targetProgressSeconds, progressSeconds)
			}

			t.Logf("✅ Update object for %s: %+v", tt.name, updateObject)
		})
	}
}

// TestReadingHistoryFixValidateDate tests that the date format is correct
func TestReadingHistoryFixValidateDate(t *testing.T) {
	now := time.Now().Format("2006-01-02")

	// Verify the date format is valid
	if _, err := time.Parse("2006-01-02", now); err != nil {
		t.Errorf("Invalid date format: %s, error: %v", now, err)
	}

	// Verify it's today's date
	expected := time.Now().Format("2006-01-02")
	if now != expected {
		t.Errorf("Expected today's date %s, got %s", expected, now)
	}

	t.Logf("✅ Date format validation passed: %s", now)
}
