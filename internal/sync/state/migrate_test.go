package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateOldState(t *testing.T) {
	t.Run("no old state", func(t *testing.T) {
		tempDir := t.TempDir()
		oldPath := filepath.Join(tempDir, "nonexistent.json")
		newPath := filepath.Join(tempDir, "new_state.json")

		migrated, err := MigrateOldState(oldPath, newPath)
		require.NoError(t, err)
		assert.False(t, migrated)
		assert.NoFileExists(t, newPath)
	})

	t.Run("new state exists", func(t *testing.T) {
		tempDir := t.TempDir()
		oldPath := filepath.Join(tempDir, "old_state.json")
		newPath := filepath.Join(tempDir, "existing_state.json")

		// Create existing new state
		require.NoError(t, os.WriteFile(newPath, []byte(`{"version":"2.0"}`), 0644))

		// Create old state that would be migrated
		oldState := `{"lastSyncTimestamp":1751108977166,"lastFullSync":1751108977166,"version":"1.0"}`
		require.NoError(t, os.WriteFile(oldPath, []byte(oldState), 0644))

		migrated, err := MigrateOldState(oldPath, newPath)
		require.NoError(t, err)
		assert.False(t, migrated)

		// Verify old state wasn't modified
		data, err := os.ReadFile(oldPath)
		require.NoError(t, err)
		assert.Equal(t, oldState, string(data))
	})

	t.Run("successful migration", func(t *testing.T) {
		tempDir := t.TempDir()
		oldPath := filepath.Join(tempDir, "old_state.json")
		newPath := filepath.Join(tempDir, "new_state.json")

		// Create old state
		oldState := `{"lastSyncTimestamp":1751108977166,"lastFullSync":1751108977166,"version":"1.0"}`
		require.NoError(t, os.WriteFile(oldPath, []byte(oldState), 0644))

		migrated, err := MigrateOldState(oldPath, newPath)
		require.NoError(t, err)
		assert.True(t, migrated)

		// Verify new state exists
		state, err := LoadState(newPath)
		require.NoError(t, err)
		assert.Equal(t, CurrentVersion, state.Version)
		expectedTime := int64(1751108977) // Converted from ms to s
		assert.Equal(t, expectedTime, state.LastSync)
		assert.Equal(t, expectedTime, state.LastFullSync)

		// Verify old state was renamed
		_, err = os.Stat(oldPath + ".migrated")
		assert.NoError(t, err, "old state should be renamed")
	})

	t.Run("invalid old state", func(t *testing.T) {
		tempDir := t.TempDir()
		oldPath := filepath.Join(tempDir, "invalid_state.json")
		newPath := filepath.Join(tempDir, "new_state.json")

		// Create invalid old state
		require.NoError(t, os.WriteFile(oldPath, []byte("invalid json"), 0644))

		_, err := MigrateOldState(oldPath, newPath)
		assert.Error(t, err)
		assert.NoFileExists(t, newPath)
	})
}
