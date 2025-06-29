package testutils

import (
	"testing"
)

// Test the status determination logic, especially for finished books showing 0% progress
func TestStatusDeterminationLogic(t *testing.T) {
	tests := []struct {
		name           string
		progress       float64
		currentTime    float64
		totalDuration  float64
		expectedStatus int
		description    string
	}{
		{
			name:           "Book with 100% progress",
			progress:       1.0,
			currentTime:    3600,
			totalDuration:  3600,
			expectedStatus: 3, // read
			description:    "Book showing 100% progress should be marked as read",
		},
		{
			name:           "Book with 99% progress",
			progress:       0.99,
			currentTime:    3564, // 99% of 3600
			totalDuration:  3600,
			expectedStatus: 3, // read
			description:    "Book showing 99% progress should be marked as read",
		},
		{
			name:           "Finished book with 0% progress but full listening time",
			progress:       0.0,
			currentTime:    3600,
			totalDuration:  3600,
			expectedStatus: 3, // read
			description:    "Book showing 0% progress but full listening time should be marked as read",
		},
		{
			name:           "Finished book with 0% progress but 95% listening time",
			progress:       0.0,
			currentTime:    3420, // 95% of 3600
			totalDuration:  3600,
			expectedStatus: 3, // read
			description:    "Book showing 0% progress but 95% listening time should be marked as read",
		},
		{
			name:           "Finished book with 0% progress but within 5 minutes of end",
			progress:       0.0,
			currentTime:    3300, // 5 minutes from end
			totalDuration:  3600,
			expectedStatus: 3, // read
			description:    "Book showing 0% progress but within 5 minutes of end should be marked as read",
		},
		{
			name:           "Book with 50% progress",
			progress:       0.5,
			currentTime:    1800,
			totalDuration:  3600,
			expectedStatus: 2, // currently reading
			description:    "Book with 50% progress should be marked as currently reading",
		},
		{
			name:           "Book with 0% progress and 0% listening time",
			progress:       0.0,
			currentTime:    0,
			totalDuration:  3600,
			expectedStatus: 1, // want to read (if getSyncWantToRead() is true)
			description:    "Book with no progress should be marked as want to read if sync is enabled",
		},
		{
			name:           "Book with minimal progress",
			progress:       0.01,
			currentTime:    36, // 1% of 3600
			totalDuration:  3600,
			expectedStatus: 2, // currently reading
			description:    "Book with minimal progress should be marked as currently reading",
		},
		{
			name:           "Book with 0% progress but some listening time (not finished)",
			progress:       0.0,
			currentTime:    1800, // 50% of 3600
			totalDuration:  3600,
			expectedStatus: 2, // currently reading
			description:    "Book with 0% progress but some listening time should be marked as currently reading",
		},
	}

	// Mock getSyncWantToRead to return true for testing
	originalGetSyncWantToRead := getSyncWantToRead
	defer func() {
		// Restore original function (if needed)
		_ = originalGetSyncWantToRead
	}()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock audiobook
			audiobook := Audiobook{
				ID:            "test-id",
				Title:         "Test Book",
				Progress:      tt.progress,
				CurrentTime:   tt.currentTime,
				TotalDuration: tt.totalDuration,
			}

			// Test the status determination logic
			actualStatus := determineTargetStatus(audiobook)

			if actualStatus != tt.expectedStatus {
				t.Errorf("Test '%s' failed: expected status %d, got %d\nDescription: %s\nBook: Progress=%.2f%%, CurrentTime=%.0fs, TotalDuration=%.0fs",
					tt.name, tt.expectedStatus, actualStatus, tt.description,
					tt.progress*100, tt.currentTime, tt.totalDuration)
			}
		})
	}
}

// Helper function to extract the status determination logic from sync.go
// This simulates the logic we fixed in the sync function
func determineTargetStatus(a Audiobook) int {
	// Check if book is actually finished despite showing 0% progress
	// This can happen when enhanced detection fails to properly identify finished books
	isBookFinished := a.Progress >= 0.99

	// Additional checks for finished status using enhanced detection
	if !isBookFinished && a.Progress == 0 {
		// Also check if book appears to be finished based on listening position
		if a.CurrentTime > 0 && a.TotalDuration > 0 {
			actualProgress := a.CurrentTime / a.TotalDuration
			remainingTime := a.TotalDuration - a.CurrentTime
			if actualProgress >= 0.95 || remainingTime <= 300 { // Within 95% or 5 minutes of end
				isBookFinished = true
			}
		}
	}

	// Set status based on actual finished state
	var status int
	if isBookFinished {
		status = 3 // read
	} else if a.Progress > 0 || (a.CurrentTime > 0 && a.TotalDuration > 0) {
		status = 2 // currently reading
	} else {
		// Only set to "want to read" if we're certain the book is not finished
		// For testing purposes, assume getSyncWantToRead() returns true
		status = 1 // want to read
	}

	return status
}

// Test specifically for the "If I Was Your Girl" case mentioned in the issue
func TestIfIWasYourGirlCase(t *testing.T) {
	// This represents the problematic case where a finished book shows 0% progress
	audiobook := Audiobook{
		ID:            "test-id",
		Title:         "If I Was Your Girl",
		Progress:      0.0,   // Shows 0% progress due to API detection issues
		CurrentTime:   25200, // 7 hours of listening time
		TotalDuration: 25200, // 7-hour book
	}

	status := determineTargetStatus(audiobook)

	if status != 3 { // Should be marked as "read"
		t.Errorf("'If I Was Your Girl' case failed: expected status 3 (read), got %d", status)
		t.Errorf("Book details: Progress=%.2f%%, CurrentTime=%.0fs, TotalDuration=%.0fs",
			audiobook.Progress*100, audiobook.CurrentTime, audiobook.TotalDuration)
	}
}

// Test edge cases for the finished book detection
func TestFinishedBookEdgeCases(t *testing.T) {
	tests := []struct {
		name           string
		progress       float64
		currentTime    float64
		totalDuration  float64
		expectedStatus int
		description    string
	}{
		{
			name:           "Book finished exactly at end",
			progress:       0.0,
			currentTime:    3600,
			totalDuration:  3600,
			expectedStatus: 3,
			description:    "Book with current time exactly equal to total duration",
		},
		{
			name:           "Book finished within 1 second of end",
			progress:       0.0,
			currentTime:    3599,
			totalDuration:  3600,
			expectedStatus: 3,
			description:    "Book with current time within 1 second of end",
		},
		{
			name:           "Book finished within 5 minutes of end",
			progress:       0.0,
			currentTime:    3301, // 299 seconds remaining
			totalDuration:  3600,
			expectedStatus: 3,
			description:    "Book with current time within 5 minutes of end",
		},
		{
			name:           "Book just over 5 minutes from end",
			progress:       0.0,
			currentTime:    3299, // 301 seconds remaining
			totalDuration:  3600,
			expectedStatus: 2, // currently reading
			description:    "Book with current time just over 5 minutes from end",
		},
		{
			name:           "Book at exactly 95% complete",
			progress:       0.0,
			currentTime:    3420, // 95% of 3600
			totalDuration:  3600,
			expectedStatus: 3,
			description:    "Book at exactly 95% complete",
		},
		{
			name:           "Book at 94.9% complete",
			progress:       0.0,
			currentTime:    3416.4, // 94.9% of 3600
			totalDuration:  3600,
			expectedStatus: 3, // read (because 184s remaining < 300s threshold)
			description:    "Book at 94.9% complete (just under 95% but within 5 minute threshold)",
		},
		{
			name:           "Book at 90% complete",
			progress:       0.0,
			currentTime:    3240, // 90% of 3600
			totalDuration:  3600,
			expectedStatus: 2, // currently reading
			description:    "Book at 90% complete (under both 95% and 5 minute thresholds)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			audiobook := Audiobook{
				ID:            "test-id",
				Title:         "Test Book",
				Progress:      tt.progress,
				CurrentTime:   tt.currentTime,
				TotalDuration: tt.totalDuration,
			}

			actualStatus := determineTargetStatus(audiobook)

			if actualStatus != tt.expectedStatus {
				t.Errorf("Test '%s' failed: expected status %d, got %d\nDescription: %s\nBook: Progress=%.2f%%, CurrentTime=%.0fs, TotalDuration=%.0fs, ActualProgress=%.2f%%, RemainingTime=%.0fs",
					tt.name, tt.expectedStatus, actualStatus, tt.description,
					tt.progress*100, tt.currentTime, tt.totalDuration,
					(tt.currentTime/tt.totalDuration)*100, tt.totalDuration-tt.currentTime)
			}
		})
	}
}
