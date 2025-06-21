package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SyncState represents the persistent sync state
type SyncState struct {
	LastSyncTimestamp int64  `json:"lastSyncTimestamp"` // Unix timestamp in milliseconds
	LastFullSync      int64  `json:"lastFullSync"`      // Unix timestamp of last full sync
	Version           string `json:"version"`           // State file version for compatibility
}

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
func loadSyncState() (*SyncState, error) {
	stateFile := getStateFilePath()

	// If file doesn't exist, return a new state (first run)
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		debugLog("No sync state file found, starting with fresh state")
		return &SyncState{
			LastSyncTimestamp: 0,
			LastFullSync:      0,
			Version:           StateVersion,
		}, nil
	}

	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read sync state file: %v", err)
	}

	var state SyncState
	if err := json.Unmarshal(data, &state); err != nil {
		debugLog("Failed to parse sync state file, starting fresh: %v", err)
		return &SyncState{
			LastSyncTimestamp: 0,
			LastFullSync:      0,
			Version:           StateVersion,
		}, nil
	}

	debugLog("Loaded sync state: LastSync=%d, LastFullSync=%d",
		state.LastSyncTimestamp, state.LastFullSync)

	return &state, nil
}

// saveSyncState saves the sync state to persistent storage
func saveSyncState(state *SyncState) error {
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
func shouldPerformFullSync(state *SyncState) bool {
	now := time.Now()

	// Force full sync if we've never done one
	if state.LastFullSync == 0 {
		debugLog("No previous full sync found, performing full sync")
		return true
	}

	// Force full sync if it's been too long since last full sync
	lastFullSync := time.Unix(state.LastFullSync/1000, 0)
	daysSinceFullSync := now.Sub(lastFullSync).Hours() / 24

	if daysSinceFullSync >= MaxDaysBetweenFullSync {
		debugLog("Last full sync was %.1f days ago, performing full sync", daysSinceFullSync)
		return true
	}

	// Check environment variable for forcing full sync
	if os.Getenv("FORCE_FULL_SYNC") == "true" {
		debugLog("FORCE_FULL_SYNC environment variable set, performing full sync")
		return true
	}

	// Check if incremental sync is disabled
	if getIncrementalSyncMode() == "disabled" {
		debugLog("Incremental sync disabled, performing full sync")
		return true
	}

	debugLog("Using incremental sync (last full sync: %.1f days ago)", daysSinceFullSync)
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
func updateSyncTimestamp(state *SyncState, isFullSync bool) {
	now := time.Now().UnixMilli()
	state.LastSyncTimestamp = now

	if isFullSync {
		state.LastFullSync = now
	}

	state.Version = StateVersion
}

// getTimestampThreshold returns the timestamp threshold for incremental sync
func getTimestampThreshold(state *SyncState) int64 {
	// Use last sync timestamp as the threshold for incremental updates
	// We subtract a small buffer (5 minutes) to handle clock skew and ensure we don't miss updates
	bufferMs := int64(5 * 60 * 1000) // 5 minutes in milliseconds
	threshold := state.LastSyncTimestamp - bufferMs

	// Ensure threshold is not negative
	if threshold < 0 {
		threshold = 0
	}

	debugLog("Using timestamp threshold: %d (last sync: %d, buffer: %dms)",
		threshold, state.LastSyncTimestamp, bufferMs)

	return threshold
}
