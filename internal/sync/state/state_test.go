package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewState(t *testing.T) {
	t.Parallel()

	state := NewState()
	assert.Equal(t, CurrentVersion, state.Version)
	assert.NotZero(t, state.Libraries)
	assert.NotZero(t, state.Books)
}

func TestLoadState_NewFile(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "nonexistent.json")

	state, err := LoadState(statePath)
	require.NoError(t, err)
	assert.Equal(t, CurrentVersion, state.Version)
}

func TestLoadState_V1(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "state_v1.json")

	// Create a v1 state file
	v1State := `{
		"lastSyncTimestamp": 1751108977166,
		"lastFullSync": 1751108977166,
		"version": "1.0"
	}`
	require.NoError(t, os.WriteFile(statePath, []byte(v1State), 0644))

	// Load and migrate
	state, err := LoadState(statePath)
	require.NoError(t, err)

	// Verify migration
	expectedTime := int64(1751108977) // Converted from ms to s
	assert.Equal(t, CurrentVersion, state.Version)
	assert.Equal(t, expectedTime, state.LastSync)
	assert.Equal(t, expectedTime, state.LastFullSync)
}

func TestLoadState_InvalidJSON(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "invalid.json")

	require.NoError(t, os.WriteFile(statePath, []byte("invalid json"), 0644))

	_, err := LoadState(statePath)
	assert.Error(t, err)
}

func TestSaveAndLoad(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "test_state.json")

	// Create and save state
	state1 := NewState()
	state1.UpdateBook("book1", 0.5, "IN_PROGRESS")
	state1.UpdateLibrary("lib1")
	state1.SetFullSync()

	require.NoError(t, state1.Save(statePath))

	// Load state
	state2, err := LoadState(statePath)
	require.NoError(t, err)

	// Verify data
	assert.Equal(t, state1.Version, state2.Version)
	assert.Equal(t, state1.LastSync, state2.LastSync)
	assert.Equal(t, state1.LastFullSync, state2.LastFullSync)
	assert.Len(t, state2.Libraries, 1)
	assert.Len(t, state2.Books, 1)

	// Verify book data
	book, exists := state2.Books["book1"]
	require.True(t, exists)
	assert.Equal(t, 0.5, book.LastProgress)
	assert.Equal(t, "IN_PROGRESS", book.Status)
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()

	state := NewState()
	done := make(chan bool)

	// Start multiple goroutines that update the state
	for i := 0; i < 10; i++ {
		go func(i int) {
			for j := 0; j < 100; j++ {
				bookID := string(rune('A' + (i % 26)))
				state.UpdateBook(bookID, float64(j)/100.0, "IN_PROGRESS")
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to finish
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify no data races occurred
	assert.True(t, len(state.Books) > 0)
}

func TestBookUpdates(t *testing.T) {
	t.Parallel()

	state := NewState()
	now := time.Now().Unix()

	// First update
	state.UpdateBook("book1", 0.25, "IN_PROGRESS")
	book, exists := state.Books["book1"]
	require.True(t, exists)
	assert.Equal(t, 0.25, book.LastProgress)
	assert.Equal(t, "IN_PROGRESS", book.Status)
	assert.GreaterOrEqual(t, book.LastUpdated, now)

	// Update again
	time.Sleep(10 * time.Millisecond) // Ensure timestamps are different
	state.UpdateBook("book1", 0.5, "IN_PROGRESS")
	book = state.Books["book1"]
	assert.Equal(t, 0.5, book.LastProgress)
	assert.GreaterOrEqual(t, book.LastUpdated, now, "timestamp should be greater than or equal to the previous one")
}

func TestLibraryUpdates(t *testing.T) {
	t.Parallel()

	state := NewState()
	now := time.Now().Unix()

	// First update
	state.UpdateLibrary("lib1")
	lib, exists := state.Libraries["lib1"]
	require.True(t, exists)
	assert.GreaterOrEqual(t, lib.LastUpdated, now)

	// Update again
	time.Sleep(10 * time.Millisecond) // Ensure timestamps are different
	state.UpdateLibrary("lib1")
	lib = state.Libraries["lib1"]
	assert.GreaterOrEqual(t, lib.LastUpdated, now, "timestamp should be greater than or equal to the previous one")
}

func TestSetFullSync(t *testing.T) {
	t.Parallel()

	state := NewState()
	now := time.Now().Unix()

	state.SetFullSync()
	assert.GreaterOrEqual(t, state.LastFullSync, now)
}
