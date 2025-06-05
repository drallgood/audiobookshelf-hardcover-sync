package main

import (
	"os"
	"testing"
)

// TestGetSyncWantToRead tests the getSyncWantToRead function with various environment variable values
func TestGetSyncWantToRead(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{"Default (empty)", "", true},
		{"true", "true", true},
		{"TRUE", "TRUE", true},
		{"True", "True", true},
		{"1", "1", true},
		{"yes", "yes", true},
		{"YES", "YES", true},
		{"Yes", "Yes", true},
		{"false", "false", false},
		{"0", "0", false},
		{"no", "no", false},
		{"invalid", "invalid", true}, // defaults to true for unknown values
	}

	// Save original value to restore later
	originalValue := os.Getenv("SYNC_WANT_TO_READ")
	defer func() {
		if originalValue == "" {
			os.Unsetenv("SYNC_WANT_TO_READ")
		} else {
			os.Setenv("SYNC_WANT_TO_READ", originalValue)
		}
	}()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable
			if tt.envValue == "" {
				os.Unsetenv("SYNC_WANT_TO_READ")
			} else {
				os.Setenv("SYNC_WANT_TO_READ", tt.envValue)
			}

			result := getSyncWantToRead()
			if result != tt.expected {
				t.Errorf("getSyncWantToRead() with SYNC_WANT_TO_READ=%q = %v, want %v", tt.envValue, result, tt.expected)
			}
		})
	}
}

// TestWantToReadStatusLogic tests the status determination logic when SYNC_WANT_TO_READ is enabled
func TestWantToReadStatusLogic(t *testing.T) {
	// Save original value to restore later
	originalValue := os.Getenv("SYNC_WANT_TO_READ")
	defer func() {
		if originalValue == "" {
			os.Unsetenv("SYNC_WANT_TO_READ")
		} else {
			os.Setenv("SYNC_WANT_TO_READ", originalValue)
		}
	}()

	tests := []struct {
		name             string
		progress         float64
		syncWantToRead   bool
		expectedStatusId int
		description      string
	}{
		{
			name:             "Zero progress with SYNC_WANT_TO_READ enabled",
			progress:         0.0,
			syncWantToRead:   true,
			expectedStatusId: 1, // want to read
			description:      "Should set status to 'Want to Read' for 0% progress books",
		},
		{
			name:             "Zero progress with SYNC_WANT_TO_READ disabled",
			progress:         0.0,
			syncWantToRead:   false,
			expectedStatusId: 2, // currently reading (because progress < 0.99)
			description:      "Should set status to 'Currently Reading' for 0% progress books when feature disabled",
		},
		{
			name:             "In-progress book with SYNC_WANT_TO_READ enabled",
			progress:         0.5,
			syncWantToRead:   true,
			expectedStatusId: 2, // currently reading
			description:      "Should set status to 'Currently Reading' for in-progress books",
		},
		{
			name:             "Finished book with SYNC_WANT_TO_READ enabled",
			progress:         1.0,
			syncWantToRead:   true,
			expectedStatusId: 3, // read
			description:      "Should set status to 'Read' for finished books",
		},
		{
			name:             "Nearly finished book with SYNC_WANT_TO_READ enabled",
			progress:         0.98,
			syncWantToRead:   true,
			expectedStatusId: 2, // currently reading
			description:      "Should set status to 'Currently Reading' for nearly finished books",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable
			if tt.syncWantToRead {
				os.Setenv("SYNC_WANT_TO_READ", "true")
			} else {
				os.Setenv("SYNC_WANT_TO_READ", "false")
			}

			// Simulate the status determination logic from sync.go
			targetStatusId := 3 // default to read
			if tt.progress == 0 && getSyncWantToRead() {
				targetStatusId = 1 // want to read
			} else if tt.progress < 0.99 {
				targetStatusId = 2 // currently reading
			}

			if targetStatusId != tt.expectedStatusId {
				t.Errorf("%s: expected status ID %d, got %d", tt.description, tt.expectedStatusId, targetStatusId)
			}
		})
	}
}
