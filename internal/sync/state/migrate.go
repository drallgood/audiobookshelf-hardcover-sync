package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MigrateOldState migrates the old sync state file to the new location
// It looks for the old state file in the cache directory and moves it to the new location
// if it exists.
func MigrateOldState(oldPath, newPath string) (bool, error) {
	if oldPath == "" {
		oldPath = "./cache/sync_state.json"
	}

	if newPath == "" {
		newPath = DefaultStateFile
	}

	// Check if new state already exists
	if _, err := os.Stat(newPath); err == nil {
		// New state exists, no migration needed
		return false, nil
	}

	// Check if old state exists
	oldData, err := os.ReadFile(oldPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No old state to migrate
			return false, nil
		}
		return false, fmt.Errorf("failed to read old state file: %w", err)
	}

	// Ensure new directory exists
	if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
		return false, fmt.Errorf("failed to create state directory: %w", err)
	}

	// Parse the old state to validate it
	var v1 v1State
	if err := json.Unmarshal(oldData, &v1); err != nil {
		return false, fmt.Errorf("invalid old state format: %w", err)
	}

	// Create the new state from the old one
	state := migrateV1ToV2(v1)

	// Save the new state
	if err := state.Save(newPath); err != nil {
		return false, fmt.Errorf("failed to save new state: %w", err)
	}

	// Optional: Rename the old file to mark it as migrated
	backupPath := oldPath + ".migrated"
	if err := os.Rename(oldPath, backupPath); err != nil {
		// Not fatal, just log it
		fmt.Printf("warning: failed to rename old state file: %v\n", err)
	}

	return true, nil
}
