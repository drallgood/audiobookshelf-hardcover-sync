package main

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
		name           string
		forceFullSync  string
		lastFullSync   int64
		expected       bool
		description    string
	}{
		{
			name:          "Force full sync enabled",
			forceFullSync: "true",
			lastFullSync:  time.Now().UnixMilli(),
			expected:      true,
			description:   "Should perform full sync when FORCE_FULL_SYNC=true",
		},
		{
			name:          "Never synced before",
			forceFullSync: "false",
			lastFullSync:  0,
			expected:      true,
			description:   "Should perform full sync when never synced before",
		},
		{
			name:          "Recent full sync",
			forceFullSync: "false",
			lastFullSync:  time.Now().Add(-1 * time.Hour).UnixMilli(),
			expected:      false,
			description:   "Should not perform full sync when recently synced",
		},
		{
			name:          "Old full sync",
			forceFullSync: "false",
			lastFullSync:  time.Now().Add(-8 * 24 * time.Hour).UnixMilli(), // 8 days ago
			expected:      true,
			description:   "Should perform full sync when last sync was more than 7 days ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment variable
			oldVal := os.Getenv("FORCE_FULL_SYNC")
			defer os.Setenv("FORCE_FULL_SYNC", oldVal)
			os.Setenv("FORCE_FULL_SYNC", tt.forceFullSync)

			// Create state with specified lastFullSync
			state := &SyncState{
				LastFullSync: tt.lastFullSync,
				Version:      StateVersion,
			}

			// Test shouldPerformFullSync
			result := shouldPerformFullSync(state)
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
			expected: DefaultStateFile,
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
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestIncrementalSyncMode(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected string
	}{
		{
			name:     "Default incremental sync mode",
			envValue: "",
			expected: "enabled",
		},
		{
			name:     "Disabled incremental sync",
			envValue: "disabled",
			expected: "disabled",
		},
		{
			name:     "Auto incremental sync",
			envValue: "auto",
			expected: "auto",
		},
		{
			name:     "Enabled incremental sync",
			envValue: "enabled",
			expected: "enabled",
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
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestTimestampThreshold(t *testing.T) {
	now := time.Now()
	baseTimestamp := now.Add(-24 * time.Hour).UnixMilli() // 24 hours ago
	
	state := &SyncState{
		LastSyncTimestamp: baseTimestamp,
		Version:          StateVersion,
	}
	
	threshold := getTimestampThreshold(state)
	
	// The threshold should be slightly before the base timestamp (buffer of 5 minutes)
	expectedThreshold := baseTimestamp - (5 * 60 * 1000) // 5 minutes in milliseconds
	
	if threshold != expectedThreshold {
		t.Errorf("Expected threshold %d, got %d", expectedThreshold, threshold)
	}
	
	// Test with zero timestamp
	zeroState := &SyncState{
		LastSyncTimestamp: 0,
		Version:          StateVersion,
	}
	zeroThreshold := getTimestampThreshold(zeroState)
	if zeroThreshold != 0 {
		t.Errorf("Expected zero threshold for zero input, got %d", zeroThreshold)
	}
}
