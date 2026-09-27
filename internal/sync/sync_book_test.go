package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newSyncBookService(t *testing.T) (*Service, *MockHardcoverClient, *MockAudiobookshelfClient) {
	t.Helper()
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	svc.userBookCache = NewPersistentUserBookCache(t.TempDir())
	require.NoError(t, svc.userBookCache.Load())
	abs := new(MockAudiobookshelfClient)
	svc.audiobookshelf = abs
	hc.On("ClearUserBookCache").Return().Maybe()
	return svc, hc, abs
}

func expectProgressUpdate(hc *MockHardcoverClient, book *models.AudiobookshelfBook, dryRun bool) {
	expectASINMatch(hc, book.Media.Metadata.ASIN, "100", "200", 300)
	hc.On("GetUserBook", mock.Anything, "300").Return(&models.HardcoverBook{
		ID: "100", EditionID: "200", BookStatusID: 2,
	}, nil).Once()
	editionID := int64(200)
	progress := 100
	hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 300}).Return([]hardcover.UserBookRead{{
		ID: 400, EditionID: &editionID, ProgressSeconds: &progress,
	}}, nil).Once()
	if !dryRun {
		hc.On("UpdateUserBookRead", mock.Anything, mock.Anything).Return(true, nil).Once()
	}
}

func inProgressBook(id string) *models.AudiobookshelfBook {
	book := toAudiobookshelfBook(createTestBook(id, "Resync", "Author", id+"-asin", ""))
	book.Media.Duration = 1000
	book.Progress.CurrentTime = 300
	return book
}

func TestSyncBookOutcomes(t *testing.T) {
	t.Run("synced persists state for a later sync", func(t *testing.T) {
		svc, hc, abs := newSyncBookService(t)
		book := inProgressBook("resync-synced")
		abs.On("GetUserProgress", mock.Anything).Return(&models.AudiobookshelfUserProgress{}, nil).Once()
		expectProgressUpdate(hc, book, false)
		statePath := filepath.Join(t.TempDir(), "state.json")

		result, err := svc.SyncBook(context.Background(), *book, state.NewState(), statePath)

		require.NoError(t, err)
		assert.Equal(t, OutcomeSynced, result.Outcome)
		stored, loadErr := state.LoadState(statePath)
		require.NoError(t, loadErr)
		assert.Contains(t, stored.Books, book.ID+":200")
		hc.AssertExpectations(t)
	})

	t.Run("dry run mutates and persists nothing", func(t *testing.T) {
		svc, hc, abs := newSyncBookService(t)
		svc.config.Sync.DryRun = true
		book := inProgressBook("resync-dry")
		abs.On("GetUserProgress", mock.Anything).Return(&models.AudiobookshelfUserProgress{}, nil).Once()
		expectProgressUpdate(hc, book, true)
		statePath := filepath.Join(t.TempDir(), "state.json")

		result, err := svc.SyncBook(context.Background(), *book, state.NewState(), statePath)

		require.NoError(t, err)
		assert.Equal(t, OutcomeWouldSync, result.Outcome)
		_, statErr := os.Stat(statePath)
		assert.True(t, os.IsNotExist(statErr), "dry run must not write sync state")
		hc.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.Anything)
	})

	t.Run("already current", func(t *testing.T) {
		svc, hc, abs := newSyncBookService(t)
		svc.config.Sync.Incremental = true
		svc.config.Sync.ProcessUnreadBooks = true
		book := toAudiobookshelfBook(createTestBook("resync-current", "Current", "Author", "", ""))
		current := state.NewState()
		current.UpdateBook(book.ID, 0, "WANT_TO_READ")
		current.SetHasProgressSeconds(book.ID)
		abs.On("GetUserProgress", mock.Anything).Return(&models.AudiobookshelfUserProgress{}, nil).Once()

		result, err := svc.SyncBook(context.Background(), *book, current, filepath.Join(t.TempDir(), "state.json"))

		require.NoError(t, err)
		assert.Equal(t, OutcomeAlreadyCurrent, result.Outcome)
		assertNoHardcoverBookSearches(t, hc)
	})

	t.Run("skipped", func(t *testing.T) {
		svc, _, abs := newSyncBookService(t)
		svc.config.Sync.ProcessUnreadBooks = false
		book := toAudiobookshelfBook(createTestBook("resync-skipped", "Unread", "Author", "skip-asin", ""))
		abs.On("GetUserProgress", mock.Anything).Return(&models.AudiobookshelfUserProgress{}, nil).Once()

		result, err := svc.SyncBook(context.Background(), *book, state.NewState(), filepath.Join(t.TempDir(), "state.json"))

		require.NoError(t, err)
		assert.Equal(t, OutcomeSkipped, result.Outcome)
	})

	t.Run("hardcover failure is reported as an outcome", func(t *testing.T) {
		svc, hc, abs := newSyncBookService(t)
		book := inProgressBook("resync-failed")
		abs.On("GetUserProgress", mock.Anything).Return(&models.AudiobookshelfUserProgress{}, nil).Once()
		expectASINMatch(hc, book.Media.Metadata.ASIN, "100", "200", 300)
		hc.On("GetUserBook", mock.Anything, "300").Return(&models.HardcoverBook{
			ID: "100", EditionID: "200", BookStatusID: 2,
		}, nil).Once()
		hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 300}).
			Return(nil, errors.New("reads unavailable")).Once()

		result, err := svc.SyncBook(context.Background(), *book, state.NewState(), filepath.Join(t.TempDir(), "state.json"))

		require.NoError(t, err)
		assert.Equal(t, OutcomeFailed, result.Outcome)
		assert.Contains(t, result.Error, "reads unavailable")
	})
}

func TestSyncBookErrors(t *testing.T) {
	t.Run("progress fetch failure", func(t *testing.T) {
		svc, hc, abs := newSyncBookService(t)
		book := inProgressBook("resync-no-progress")
		abs.On("GetUserProgress", mock.Anything).Return((*models.AudiobookshelfUserProgress)(nil), errors.New("abs down")).Once()

		_, err := svc.SyncBook(context.Background(), *book, state.NewState(), filepath.Join(t.TempDir(), "state.json"))

		require.ErrorContains(t, err, "abs down")
		assertNoHardcoverBookSearches(t, hc)
	})

	t.Run("canceled before start", func(t *testing.T) {
		svc, hc, _ := newSyncBookService(t)
		book := inProgressBook("resync-canceled")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := svc.SyncBook(ctx, *book, state.NewState(), filepath.Join(t.TempDir(), "state.json"))

		require.ErrorIs(t, err, context.Canceled)
		assertNoHardcoverBookSearches(t, hc)
	})

	t.Run("canceled during processing", func(t *testing.T) {
		svc, hc, abs := newSyncBookService(t)
		book := inProgressBook("resync-canceled-mid")
		ctx, cancel := context.WithCancel(context.Background())
		abs.On("GetUserProgress", mock.Anything).Run(func(mock.Arguments) { cancel() }).
			Return(&models.AudiobookshelfUserProgress{}, nil).Once()

		_, err := svc.SyncBook(ctx, *book, state.NewState(), filepath.Join(t.TempDir(), "state.json"))

		require.ErrorIs(t, err, context.Canceled)
		assertNoHardcoverBookSearches(t, hc)
	})

	t.Run("missing inputs", func(t *testing.T) {
		svc, _, _ := newSyncBookService(t)
		_, err := svc.SyncBook(context.Background(), models.AudiobookshelfBook{}, state.NewState(), "")
		require.Error(t, err)
		_, err = svc.SyncBook(context.Background(), *inProgressBook("x"), nil, "")
		require.Error(t, err)
	})
}
