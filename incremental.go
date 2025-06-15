package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/types"
)

const (
	// State file location (configurable via environment variable)
	DefaultStateFile = "sync_state.json"
	StateVersion     = "1.0"

	// Force full sync after this many days without a full sync
	MaxDaysBetweenFullSync = 7
)

// getStateFilePath returns the path to the sync state file
func getStateFilePath() string {
	if path := os.Getenv("SYNC_STATE_FILE"); path != "" {
		return path
	}
	return DefaultStateFile
}

// loadSyncState loads the sync state from persistent storage
func loadSyncState() (*types.SyncState, error) {
	stateFile := getStateFilePath()

	// If file doesn't exist, return a new state (first run)
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		debugLog("No sync state file found, starting with fresh state")
		return &types.SyncState{
			LastSyncTimestamp: 0,
			LastFullSync:      0,
			Version:           StateVersion,
		}, nil
	}

	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("error reading state file: %v", err)
	}

	state := &types.SyncState{}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("error parsing state file: %v", err)
	}

	// Initialize zero values if needed
	if state.Version == "" {
		state.Version = StateVersion
	}
	if state.LastSync.IsZero() {
		state.LastSync = time.Now()
	}
	if state.LastSyncTimestamp == 0 {
		state.LastSyncTimestamp = time.Now().UnixMilli()
	}

	debugLog("Loaded sync state: LastSync=%d, LastFullSync=%d",
		state.LastSyncTimestamp, state.LastFullSync)

	return state, nil
}

// saveSyncState saves the sync state to persistent storage
func saveSyncState(state *types.SyncState) error {
	stateFile := getStateFilePath()

	// Create directory if it doesn't exist
	if dir := filepath.Dir(stateFile); dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create state directory: %v", err)
		}
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal sync state: %v", err)
	}

	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write sync state file: %v", err)
	}

	debugLog("Saved sync state: LastSync=%d, LastFullSync=%d",
		state.LastSyncTimestamp, state.LastFullSync)

	return nil
}

// shouldPerformFullSync determines if a full sync should be performed
func shouldPerformFullSync(state *types.SyncState) bool {
	now := time.Now()

	// Check if full sync is forced via environment variable
	if os.Getenv("FORCE_FULL_SYNC") == "true" {
		debugLog("Performing full sync: FORCE_FULL_SYNC is true")
		return true
	}

	// Force full sync if we've never done one
	if state.LastFullSync == 0 {
		debugLog("Performing full sync: first run")
		return true
	}

	// Check if it's been more than MaxDaysBetweenFullSync days since last full sync
	lastFullSync := time.UnixMilli(state.LastFullSync)
	daysSinceFullSync := now.Sub(lastFullSync).Hours() / 24

	if daysSinceFullSync >= MaxDaysBetweenFullSync {
		debugLog("Performing full sync: last full sync was %.1f days ago", daysSinceFullSync)
		return true
	}

	// Check if incremental sync is explicitly disabled
	switch getIncrementalSyncMode() {
	case "disabled":
		debugLog("Performing full sync: incremental sync is disabled")
		return true
	case "enabled":
		debugLog("Performing incremental sync: explicitly enabled")
		return false
	}

	// Default to full sync if we can't determine the last sync time
	if state.LastSyncTimestamp == 0 {
		debugLog("Performing full sync: no previous sync timestamp")
		return true
	}

	// Default to incremental sync
	debugLog("Performing incremental sync: last full sync was %.1f days ago", daysSinceFullSync)
	return false
}

// getIncrementalSyncMode returns the incremental sync mode from environment
// Options: "enabled" (default), "disabled", "auto"
func getIncrementalSyncMode() string {
	mode := os.Getenv("INCREMENTAL_SYNC_MODE")
	switch mode {
	case "disabled", "auto":
		return mode
	default:
		return "enabled" // Default mode
	}
}

// updateSyncTimestamp updates the sync state with the current timestamp
func updateSyncTimestamp(state *types.SyncState, isFullSync bool) {
	now := time.Now()
	state.LastSync = now
	state.LastSyncTimestamp = now.UnixMilli()

	if isFullSync {
		state.LastFullSync = now.UnixMilli()
	}
	
	// Update sync metadata
	state.SyncCount++
	state.SyncStatus = "success"
	if isFullSync {
		state.SyncMode = "full"
	} else {
		state.SyncMode = "incremental"
	}
	state.Version = StateVersion
}

// getTimestampThreshold returns the timestamp threshold for incremental sync
func getTimestampThreshold(state *types.SyncState) int64 {
	// If we've never synced, return 0 to get all books
	if state.LastSyncTimestamp == 0 {
		return 0
	}

	// Use last sync timestamp as the threshold for incremental updates
	// We subtract a small buffer (5 minutes) to handle clock skew and ensure we don't miss updates
	bufferMs := int64(5 * 60 * 1000) // 5 minutes in milliseconds
	
	// Return the threshold with buffer
	threshold := state.LastSyncTimestamp - bufferMs
	if threshold < 0 {
		return 0
	}
	
	debugLog("Using incremental sync threshold: %d (last sync: %s)", 
		threshold, time.UnixMilli(state.LastSyncTimestamp).Format(time.RFC3339))
		
	return threshold
}
