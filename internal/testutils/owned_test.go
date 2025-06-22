package testutils

import (
	"os"
	"testing"
)

// TestGetSyncOwned tests the getSyncOwned function with various environment variable values
func TestGetSyncOwned(t *testing.T) {
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
		{"0", "0", false},  // '0' is explicitly handled as false in the implementation
		{"no", "no", false},  // 'no' is explicitly handled as false in the implementation
		{"invalid", "invalid", true}, // defaults to true for unknown values
	}

	// Save original value to restore later
	originalValue := os.Getenv("SYNC_OWNED")
	defer func() {
		if originalValue == "" {
			os.Unsetenv("SYNC_OWNED")
		} else {
			os.Setenv("SYNC_OWNED", originalValue)
		}
	}()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable
			if tt.envValue == "" {
				os.Unsetenv("SYNC_OWNED")
			} else {
				os.Setenv("SYNC_OWNED", tt.envValue)
			}

			result := getSyncOwned()
			if result != tt.expected {
				t.Errorf("getSyncOwned() with SYNC_OWNED=%q = %v, want %v", tt.envValue, result, tt.expected)
			}
		})
	}
}
