package testutils

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSyncStateManagement(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()
	stateFile := filepath.Join(tempDir, "test_sync_state.json")

	// Override the state file path for testing
	oldVal := os.Getenv("SYNC_STATE_FILE")
	defer os.Setenv("SYNC_STATE_FILE", oldVal)
	os.Setenv("SYNC_STATE_FILE", stateFile)

	// Test loading non-existent state file
	state, err := loadSyncState()
	if err != nil {
		t.Fatalf("Expected no error loading non-existent state file, got: %v", err)
	}
	if state.LastSyncTimestamp != 0 {
		t.Errorf("Expected LastSyncTimestamp to be 0, got: %d", state.LastSyncTimestamp)
	}
	if state.Version != StateVersion {
		t.Errorf("Expected Version to be %s, got: %s", StateVersion, state.Version)
	}

	// Test saving state
	now := time.Now().UnixMilli()
	state.LastSyncTimestamp = now
	state.LastFullSync = now

	err = saveSyncState(state)
	if err != nil {
		t.Fatalf("Expected no error saving state, got: %v", err)
	}

	// Test loading saved state
	loadedState, err := loadSyncState()
	if err != nil {
		t.Fatalf("Expected no error loading saved state, got: %v", err)
	}
	if loadedState.LastSyncTimestamp != now {
		t.Errorf("Expected LastSyncTimestamp to be %d, got: %d", now, loadedState.LastSyncTimestamp)
	}
	if loadedState.LastFullSync != now {
		t.Errorf("Expected LastFullSync to be %d, got: %d", now, loadedState.LastFullSync)
	}
	if loadedState.Version != StateVersion {
		t.Errorf("Expected Version to be %s, got: %s", StateVersion, loadedState.Version)
	}
}

func TestShouldPerformFullSync(t *testing.T) {
	tests := []struct {
		name          string
		forceFullSync bool
		envValue      string
		lastFullSync  int64
		expected      bool
		description   string
	}{
		{
			name:          "Force full sync enabled",
			forceFullSync: true,
			envValue:      "true",
			lastFullSync:  time.Now().UnixMilli(),
			expected:      true,
			description:   "Should perform full sync when FORCE_FULL_SYNC=true",
		},
		{
			name:          "Never synced before",
			forceFullSync: false,
			envValue:      "false",
			lastFullSync:  0,
			expected:      true,
			description:   "Should perform full sync when never synced before",
		},
		{
			name:          "Recent full sync",
			forceFullSync: false,
			envValue:      "false",
			lastFullSync:  time.Now().Add(-1 * time.Hour).UnixMilli(),
			expected:      false,
			description:   "Should not perform full sync when recently synced",
		},
		{
			name:          "Old full sync",
			forceFullSync: false,
			envValue:      "false",
			lastFullSync:  time.Now().Add(-8 * 24 * time.Hour).UnixMilli(), // 8 days ago
			expected:      true,
			description:   "Should perform full sync when last sync was more than 7 days ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment variable if envValue is not empty
			oldVal := os.Getenv("FORCE_FULL_SYNC")
			if tt.envValue != "" {
				defer os.Setenv("FORCE_FULL_SYNC", oldVal)
				os.Setenv("FORCE_FULL_SYNC", tt.envValue)
			}

			// Create state with specified lastFullSync and set LastSyncSuccess to true for recent syncs
			state := &SyncState{
				LastFullSync:    tt.lastFullSync,
				Version:         StateVersion,
				LastSyncSuccess: tt.name == "Recent full sync",
			}

			// Test shouldPerformFullSync
			result := shouldPerformFullSync(state, tt.forceFullSync)
			if result != tt.expected {
				t.Errorf("%s: expected %v, got %v", tt.description, tt.expected, result)
			}
		})
	}
}

func TestGetStateFilePath(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected string
	}{
		{
			name:     "Default state file",
			envValue: "",
			expected: func() string {
				configDir, _ := os.UserConfigDir()
				return filepath.Join(configDir, "audiobookshelf-hardcover-sync", "sync_state.json")
			}(),
		},
		{
			name:     "Custom state file",
			envValue: "/custom/path/state.json",
			expected: "/custom/path/state.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment variable
			oldVal := os.Getenv("SYNC_STATE_FILE")
			defer os.Setenv("SYNC_STATE_FILE", oldVal)

			if tt.envValue != "" {
				os.Setenv("SYNC_STATE_FILE", tt.envValue)
			} else {
				os.Unsetenv("SYNC_STATE_FILE")
			}

			result := getStateFilePath()
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestIncrementalSyncMode(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{
			name:     "Default incremental sync mode",
			envValue: "",
			expected: true, // Default should be enabled
		},
		{
			name:     "Disabled incremental sync",
			envValue: "false",
			expected: false,
		},
		{
			name:     "Auto incremental sync",
			envValue: "true",
			expected: true,
		},
		{
			name:     "Enabled incremental sync",
			envValue: "true",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment variable
			oldVal := os.Getenv("INCREMENTAL_SYNC_MODE")
			defer os.Setenv("INCREMENTAL_SYNC_MODE", oldVal)

			if tt.envValue != "" {
				os.Setenv("INCREMENTAL_SYNC_MODE", tt.envValue)
			} else {
				os.Unsetenv("INCREMENTAL_SYNC_MODE")
			}

			result := getIncrementalSyncMode()
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestTimestampThreshold(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected int64
	}{
		{
			name:     "24 hours ago",
			duration: 24 * time.Hour,
			expected: time.Now().Add(-24 * time.Hour).UnixMilli(),
		},
		{
			name:     "1 hour ago",
			duration: time.Hour,
			expected: time.Now().Add(-time.Hour).UnixMilli(),
		},
		{
			name:     "zero duration",
			duration: 0,
			expected: time.Now().UnixMilli(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Allow for slight timing differences in test execution
			threshold := getTimestampThreshold(tt.duration)
			if tt.duration == 0 {
				// For zero duration, we just care that it's a recent timestamp
				minExpected := time.Now().Add(-time.Second).UnixMilli()
				if threshold < minExpected {
					t.Errorf("Expected threshold >= %d, got %d", minExpected, threshold)
				}
			} else {
				// For non-zero durations, allow up to 100ms of difference
				minExpected := tt.expected - 100
				maxExpected := tt.expected + 100
				if threshold < minExpected || threshold > maxExpected {
					t.Errorf("Expected threshold between %d and %d, got %d", 
						minExpected, maxExpected, threshold)
				}
			}
		})
	}
}
