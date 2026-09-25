package state

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const windowsSymlinkPrivilegeNotHeld = 1314 // ERROR_PRIVILEGE_NOT_HELD

func createTestSymlink(t *testing.T, oldname, newname string) {
	t.Helper()

	err := os.Symlink(oldname, newname)
	if err == nil {
		return
	}

	var linkErr *os.LinkError
	var errno syscall.Errno
	if runtime.GOOS == "windows" &&
		errors.As(err, &linkErr) &&
		linkErr.Op == "symlink" &&
		errors.As(linkErr.Err, &errno) &&
		errno == windowsSymlinkPrivilegeNotHeld {
		t.Skipf("skipping symlink test: Windows symlink privilege is unavailable: %v", err)
	}

	require.NoError(t, err)
}

func TestNewState(t *testing.T) {
	t.Parallel()

	state := NewState()
	assert.NotZero(t, state.Books)
	assert.Equal(t, CurrentVersion, state.Version)
}

func TestLoadState_NewFile(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "nonexistent.json")

	state, err := LoadState(statePath)
	require.NoError(t, err)
	assert.NotNil(t, state.Books)
}

func TestLoadState_IgnoresRetiredTimestampMetadata(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "state_v1.json")

	legacyState := `{
		"lastSyncTimestamp": 1751108977166,
		"lastFullSync": 1751108977166,
		"version": "1.0"
	}`
	require.NoError(t, os.WriteFile(statePath, []byte(legacyState), 0644))

	state, err := LoadState(statePath)
	require.NoError(t, err)
	assert.Empty(t, state.Books)
	assert.Equal(t, CurrentVersion, state.Version)
}

func TestLoadState_NormalizesUnversionedAndV2Books(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		version string
	}{
		{name: "unversioned", version: ""},
		{name: "v2", version: "2.0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			contents := `{"books":{"book":{"lastProgress":0.5,"lastUpdated":42}}}`
			if test.version != "" {
				contents = `{"version":"` + test.version + `","books":{"book":{"lastProgress":0.5,"lastUpdated":42}}}`
			}
			require.NoError(t, os.WriteFile(path, []byte(contents), 0644))

			state, err := LoadState(path)
			require.NoError(t, err)
			require.Equal(t, CurrentVersion, state.Version)
			require.Equal(t, Book{LastProgress: 0.5, LastUpdated: 42}, state.Books["book"])
			require.NoError(t, state.Save(path))
			saved, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(saved), `"version": "`+CurrentVersion+`"`)
		})
	}
}

func TestLoadState_RejectsUnknownVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":"5.0","books":{}}`), 0644))
	_, err := LoadState(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported state version")
}

func TestLoadState_V3PreservesCheckpointsWhenMigrated(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	contents := `{"version":"3.0","books":{"item":{"lastProgress":0.75,"lastUpdated":42,"status":"IN_PROGRESS","userBookID":"123","hasProgressSeconds":true},"item:456":{"lastProgress":0.75,"lastUpdated":42,"status":"IN_PROGRESS"}}}`
	require.NoError(t, os.WriteFile(path, []byte(contents), 0644))

	state, err := LoadState(path)
	require.NoError(t, err)
	assert.Equal(t, CurrentVersion, state.Version)
	assert.Equal(t, Book{
		LastProgress:       0.75,
		LastUpdated:        42,
		Status:             "IN_PROGRESS",
		UserBookID:         "123",
		HasProgressSeconds: true,
	}, state.Books["item"])
	assert.Equal(t, 0.75, state.Books["item:456"].LastProgress)

	require.NoError(t, state.Save(path))
	reloaded, err := LoadState(path)
	require.NoError(t, err)
	assert.Equal(t, state.Books, reloaded.Books)
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

	require.NoError(t, state1.Save(statePath))
	saved, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.NotContains(t, string(saved), "lastSync")
	assert.NotContains(t, string(saved), "lastFullSync")
	assert.NotContains(t, string(saved), "libraries")
	assert.Contains(t, string(saved), `"version": "`+CurrentVersion+`"`)

	// Load state
	state2, err := LoadState(statePath)
	require.NoError(t, err)

	// Verify data
	assert.Len(t, state2.Books, 1)

	// Verify book data
	book, exists := state2.Books["book1"]
	require.True(t, exists)
	assert.Equal(t, 0.5, book.LastProgress)
	assert.Equal(t, "IN_PROGRESS", book.Status)
}

func TestAssociationPersistsWithCheckpointAndSurvivesCheckpointUpdates(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	state := NewState()
	state.UpdateBook("item", 0.4, "IN_PROGRESS")
	association := Association{
		ABSItemID:          "item",
		SourceASIN:         " B012345678 ",
		SourceISBN10:       "0-306-40615-2",
		SourceISBN13:       "978-0-306-40615-7",
		Correction:         "B012345678@UK",
		RegionalExternalID: "B012345678:UK",
		HardcoverBookID:    "101",
		HardcoverEditionID: "202",
		ReadingFormat:      "audiobook",
		Provenance:         "audible_mapping",
	}
	require.NoError(t, state.SetAssociation(association))
	state.UpdateBook("item:202", 0.5, "IN_PROGRESS")
	state.SetHasProgressSeconds("item:202")
	require.Equal(t, association, mustGetAssociation(t, state, "item"))

	require.NoError(t, state.Save(path))
	reloaded, err := LoadState(path)
	require.NoError(t, err)
	got, ok := reloaded.GetAssociation("item")
	require.True(t, ok)
	assert.Equal(t, association, got)
	assert.Equal(t, 0.5, reloaded.Books["item"].LastProgress)
	assert.True(t, reloaded.Books["item"].HasProgressSeconds)
}

func mustGetAssociation(t *testing.T, state *State, itemID string) Association {
	t.Helper()
	association, ok := state.GetAssociation(itemID)
	require.True(t, ok)
	return association
}

func TestRemoveAssociationClearsOnlyAssociatedCheckpoints(t *testing.T) {
	t.Parallel()

	state := NewState()
	state.UpdateBook("item", 0.4, "IN_PROGRESS")
	state.UpdateBook("item:202", 0.4, "IN_PROGRESS")
	state.UpdateBook("item-other:202", 0.8, "IN_PROGRESS")
	association := Association{ABSItemID: "item", HardcoverBookID: "101", HardcoverEditionID: "202"}
	require.NoError(t, state.SetAssociation(association))

	previous, removed := state.RemoveAssociation("item")
	require.True(t, removed)
	assert.Equal(t, association, previous)
	assert.NotContains(t, state.Books, "item")
	assert.NotContains(t, state.Books, "item:202")
	assert.Contains(t, state.Books, "item-other:202")
	assert.True(t, state.IsDirty())

	state.UpdateBook("item", 0.9, "IN_PROGRESS")
	require.NoError(t, state.Save(filepath.Join(t.TempDir(), "state.json")))
	assert.False(t, state.IsDirty())
	_, removed = state.RemoveAssociation("item")
	assert.False(t, removed)
	assert.Equal(t, 0.9, state.Books["item"].LastProgress)
}

func TestSetAssociationRequiresItemAndHardcoverIDs(t *testing.T) {
	t.Parallel()

	state := NewState()
	assert.Error(t, state.SetAssociation(Association{HardcoverBookID: "1", HardcoverEditionID: "2"}))
	assert.Error(t, state.SetAssociation(Association{ABSItemID: "item", HardcoverBookID: "1"}))
	assert.False(t, state.IsDirty())
}

func TestSaveFailurePreservesExistingTargetAndDirtyState(t *testing.T) {
	t.Parallel()

	targetDir := filepath.Join(t.TempDir(), "existing-directory")
	require.NoError(t, os.MkdirAll(targetDir, 0755))
	sentinelPath := filepath.Join(targetDir, "sentinel")
	require.NoError(t, os.WriteFile(sentinelPath, []byte("untouched"), 0600))
	state := NewState()
	state.UpdateBook("item", 0.5, "IN_PROGRESS")

	err := state.Save(targetDir)
	require.Error(t, err)
	assert.True(t, state.IsDirty())
	sentinel, readErr := os.ReadFile(sentinelPath)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("untouched"), sentinel)
}

func TestSavePreservesSymlinkTarget(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	targetDir := filepath.Join(tempDir, "target")
	linkDir := filepath.Join(tempDir, "config")
	require.NoError(t, os.MkdirAll(targetDir, 0755))
	require.NoError(t, os.MkdirAll(linkDir, 0755))

	targetPath := filepath.Join(targetDir, "state.json")
	linkPath := filepath.Join(linkDir, "sync_state.json")
	state := NewState()
	require.NoError(t, state.Save(targetPath))

	relativeTarget, err := filepath.Rel(linkDir, targetPath)
	require.NoError(t, err)
	createTestSymlink(t, relativeTarget, linkPath)

	state.UpdateBook("book1", 0.5, "IN_PROGRESS")
	require.NoError(t, state.Save(linkPath))

	linkInfo, err := os.Lstat(linkPath)
	require.NoError(t, err)
	assert.NotEqual(t, 0, linkInfo.Mode()&os.ModeSymlink)

	loaded, err := LoadState(linkPath)
	require.NoError(t, err)
	assert.Equal(t, 0.5, loaded.Books["book1"].LastProgress)
}

func TestSaveResolvesRelativeSymlinkTargetFromResolvedParent(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	configDir := filepath.Join(dataDir, "config")
	parentLink := filepath.Join(tempDir, "config")
	targetPath := filepath.Join(dataDir, "state.json")
	childLink := filepath.Join(configDir, "sync_state.json")
	wrongTargetPath := filepath.Join(tempDir, "state.json")
	require.NoError(t, os.MkdirAll(configDir, 0755))
	createTestSymlink(t, filepath.Join("data", "config"), parentLink)
	require.NoError(t, os.WriteFile(wrongTargetPath, []byte("sentinel"), 0600))
	createTestSymlink(t, filepath.Join("..", "state.json"), childLink)

	state := NewState()
	state.UpdateBook("book1", 0.5, "IN_PROGRESS")
	require.NoError(t, state.Save(filepath.Join(parentLink, "sync_state.json")))

	loaded, err := LoadState(targetPath)
	require.NoError(t, err)
	assert.Equal(t, 0.5, loaded.Books["book1"].LastProgress)

	wrongTarget, err := os.ReadFile(wrongTargetPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("sentinel"), wrongTarget)

	linkInfo, err := os.Lstat(childLink)
	require.NoError(t, err)
	assert.NotEqual(t, 0, linkInfo.Mode()&os.ModeSymlink)
}

func TestSaveResolvesDotDotAfterSymlinkComponent(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "config")
	dataDir := filepath.Join(tempDir, "data")
	linkDir := filepath.Join(configDir, "linkdir")
	targetPath := filepath.Join(dataDir, "state.json")
	wrongTargetPath := filepath.Join(configDir, "state.json")
	configuredPath := configDir + string(filepath.Separator) + "linkdir" +
		string(filepath.Separator) + ".." + string(filepath.Separator) + "state.json"

	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "nested"), 0755))
	require.NoError(t, os.MkdirAll(configDir, 0755))
	createTestSymlink(t, filepath.Join("..", "data", "nested"), linkDir)
	require.NoError(t, os.WriteFile(wrongTargetPath, []byte("sentinel"), 0600))

	state := NewState()
	state.UpdateBook("book1", 0.5, "IN_PROGRESS")
	require.NoError(t, state.Save(configuredPath))

	loaded, err := LoadState(targetPath)
	require.NoError(t, err)
	assert.Equal(t, 0.5, loaded.Books["book1"].LastProgress)

	wrongTarget, err := os.ReadFile(wrongTargetPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("sentinel"), wrongTarget)

	linkInfo, err := os.Lstat(linkDir)
	require.NoError(t, err)
	assert.NotEqual(t, 0, linkInfo.Mode()&os.ModeSymlink)
}

func TestSaveAllowsRepeatedSymlinkAfterDotDot(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	realDir := filepath.Join(dataDir, "real")
	linkPath := filepath.Join(dataDir, "link")
	targetPath := filepath.Join(realDir, "state.json")
	wrongTargetPath := filepath.Join(dataDir, "state.json")
	configuredPath := dataDir + string(filepath.Separator) + "link" +
		string(filepath.Separator) + ".." + string(filepath.Separator) + "link" +
		string(filepath.Separator) + "state.json"

	require.NoError(t, os.MkdirAll(realDir, 0755))
	createTestSymlink(t, "real", linkPath)

	state := NewState()
	state.UpdateBook("book1", 0.5, "IN_PROGRESS")
	require.NoError(t, state.Save(configuredPath))

	loaded, err := LoadState(targetPath)
	require.NoError(t, err)
	assert.Equal(t, 0.5, loaded.Books["book1"].LastProgress)

	_, err = os.Stat(wrongTargetPath)
	assert.ErrorIs(t, err, os.ErrNotExist)

	linkInfo, err := os.Lstat(linkPath)
	require.NoError(t, err)
	assert.NotEqual(t, 0, linkInfo.Mode()&os.ModeSymlink)
}

func TestSaveRejectsFileSymlinkBeforeRemainingPath(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	filePath := filepath.Join(dataDir, "file.json")
	linkPath := filepath.Join(dataDir, "link")
	wrongTargetPath := filepath.Join(dataDir, "state.json")
	configuredPath := dataDir + string(filepath.Separator) + "link" +
		string(filepath.Separator) + ".." + string(filepath.Separator) + "state.json"

	require.NoError(t, os.MkdirAll(dataDir, 0755))
	require.NoError(t, os.WriteFile(filePath, []byte("source"), 0600))
	require.NoError(t, os.WriteFile(wrongTargetPath, []byte("sentinel"), 0600))
	createTestSymlink(t, "file.json", linkPath)

	err := NewState().Save(configuredPath)
	require.Error(t, err)

	wrongTarget, err := os.ReadFile(wrongTargetPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("sentinel"), wrongTarget)
}

func TestSaveRejectsDanglingSymlinkBeforeParentTraversal(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	linkPath := filepath.Join(dataDir, "link")
	wrongTargetPath := filepath.Join(dataDir, "state.json")
	configuredPath := dataDir + string(filepath.Separator) + "link" +
		string(filepath.Separator) + ".." + string(filepath.Separator) + "state.json"

	require.NoError(t, os.MkdirAll(dataDir, 0755))
	require.NoError(t, os.WriteFile(wrongTargetPath, []byte("sentinel"), 0600))
	createTestSymlink(t, "missing.json", linkPath)

	err := NewState().Save(configuredPath)
	require.Error(t, err)

	wrongTarget, err := os.ReadFile(wrongTargetPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("sentinel"), wrongTarget)
}

func TestSaveResolvesDanglingFinalSymlink(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	targetDir := filepath.Join(tempDir, "target")
	configDir := filepath.Join(tempDir, "config")
	linkPath := filepath.Join(configDir, "sync_state.json")
	targetPath := filepath.Join(targetDir, "state.json")
	require.NoError(t, os.MkdirAll(targetDir, 0755))
	require.NoError(t, os.MkdirAll(configDir, 0755))
	createTestSymlink(t, filepath.Join("..", "target", "state.json"), linkPath)

	state := NewState()
	state.UpdateBook("book1", 0.5, "IN_PROGRESS")
	require.NoError(t, state.Save(linkPath))

	loaded, err := LoadState(linkPath)
	require.NoError(t, err)
	assert.Equal(t, 0.5, loaded.Books["book1"].LastProgress)

	linkInfo, err := os.Lstat(linkPath)
	require.NoError(t, err)
	assert.NotEqual(t, 0, linkInfo.Mode()&os.ModeSymlink)
	_, err = os.Stat(targetPath)
	require.NoError(t, err)
}

func TestSaveRejectsSymlinkLoop(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	firstPath := filepath.Join(tempDir, "first.json")
	secondPath := filepath.Join(tempDir, "second.json")
	createTestSymlink(t, filepath.Base(secondPath), firstPath)
	createTestSymlink(t, filepath.Base(firstPath), secondPath)

	err := NewState().Save(firstPath)
	require.Error(t, err)
}

func TestStateDirtyTracking(t *testing.T) {
	t.Parallel()

	state := NewState()
	assert.False(t, state.IsDirty())

	assert.True(t, state.UpdateBook("book1", 0.5, "IN_PROGRESS"))
	assert.True(t, state.IsDirty())
	require.NoError(t, state.Save(filepath.Join(t.TempDir(), "state.json")))
	assert.False(t, state.IsDirty())

	assert.False(t, state.UpdateBook("book1", 0.5, "IN_PROGRESS"))
	assert.False(t, state.IsDirty())

	state.UpdateBookWithUserBookID("book1", 0.5, "IN_PROGRESS", "user-book")
	assert.True(t, state.IsDirty())
	require.NoError(t, state.Save(filepath.Join(t.TempDir(), "state.json")))
	state.SetHasProgressSeconds("book1")
	assert.True(t, state.IsDirty())
	require.NoError(t, state.Save(filepath.Join(t.TempDir(), "state.json")))
	state.SetHasProgressSeconds("book1")
	assert.False(t, state.IsDirty())
}

func TestUpdateBookWithUserBookIDNoOpPreservesTimestamps(t *testing.T) {
	t.Parallel()

	const initialTimestamp int64 = 1234
	state := NewState()
	state.Books["book1"] = Book{
		LastProgress:       0.5,
		LastUpdated:        initialTimestamp,
		Status:             "IN_PROGRESS",
		UserBookID:         "user-book",
		HasProgressSeconds: true,
	}
	state.UpdateBookWithUserBookID("book1", 0.5, "IN_PROGRESS", "user-book")

	book, exists := state.Books["book1"]
	require.True(t, exists)
	assert.Equal(t, initialTimestamp, book.LastUpdated)
	assert.False(t, state.IsDirty())

	state.UpdateBookWithUserBookID("book1", 0.5, "FINISHED", "user-book")

	book, exists = state.Books["book1"]
	require.True(t, exists)
	assert.Equal(t, "FINISHED", book.Status)
	assert.Greater(t, book.LastUpdated, initialTimestamp)
	assert.True(t, state.IsDirty())
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

func TestCustomStatePath(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "nested", "dir", "for", "state", "sync_state.json")
	state := NewState()
	state.UpdateBook("test:123", 0.5, "IN_PROGRESS")
	require.NoError(t, state.Save(statePath))

	loadedState, err := LoadState(statePath)
	require.NoError(t, err)
	book, exists := loadedState.GetBookState("test:123")
	require.True(t, exists)
	assert.Equal(t, 0.5, book.LastProgress)
}
