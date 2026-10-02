package sync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
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
	book := toAudiobookshelfBook(createTestBook(id, "Resync", "Author", "B0SYNC0001", ""))
	book.Media.Duration = 1000
	book.Progress.CurrentTime = 300
	return book
}

func TestSyncBookWithEditionRejectsSnapshotThatDoesNotMatchCurrentItem(t *testing.T) {
	svc, hc, abs := newSyncBookService(t)
	book := inProgressBook("resync-edition-mismatch")
	syncState := state.NewState()
	asin, isbn10, isbn13 := state.SourceIdentifiers(book.Media.Metadata.ASIN, book.Media.Metadata.ISBN)
	require.NoError(t, syncState.SetAssociation(state.Association{
		ABSItemID: book.ID, SourceASIN: asin, SourceISBN10: isbn10, SourceISBN13: isbn13,
		HardcoverBookID: "100", HardcoverEditionID: "200", ReadingFormat: book.ReadingFormat(),
	}))
	book.Media.Metadata.ASIN = "changed-after-association"
	abs.AssertNotCalled(t, "GetUserProgress", mock.Anything)

	_, err := svc.SyncBookWithEdition(context.Background(), *book, &models.Edition{
		ID: "200", BookID: "100", ReadingFormatID: "2",
	}, syncState, filepath.Join(t.TempDir(), "state.json"))

	require.ErrorContains(t, err, "matching saved association")
	hc.AssertNotCalled(t, "ClearUserBookCache")
	hc.AssertExpectations(t)
	abs.AssertExpectations(t)
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

	t.Run("reliable embedded progress is used when progress fetch fails", func(t *testing.T) {
		svc, hc, abs := newSyncBookService(t)
		book := inProgressBook("resync-embedded-progress")
		abs.On("GetUserProgress", mock.Anything).Return((*models.AudiobookshelfUserProgress)(nil), errors.New("abs down")).Once()
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

	t.Run("already current with a persisted association", func(t *testing.T) {
		svc, hc, abs := newSyncBookService(t)
		svc.config.Sync.Incremental = true
		svc.config.Sync.ProcessUnreadBooks = true
		book := toAudiobookshelfBook(createTestBook("resync-current", "Current", "Author", "", ""))
		current := state.NewState()
		current.UpdateBook(book.ID, 0, "WANT_TO_READ")
		current.SetHasProgressSeconds(book.ID)
		// Without a source identifier or persisted association, this item must
		// discard its old checkpoint and attempt a fresh title lookup instead.
		require.NoError(t, current.SetAssociation(state.Association{
			ABSItemID: book.ID, HardcoverBookID: "hc-book-1", HardcoverEditionID: "hc-edition-1",
		}))
		abs.On("GetUserProgress", mock.Anything).Return(&models.AudiobookshelfUserProgress{}, nil).Once()

		result, err := svc.SyncBook(context.Background(), *book, current, filepath.Join(t.TempDir(), "state.json"))

		require.NoError(t, err)
		assert.Equal(t, OutcomeAlreadyCurrent, result.Outcome)
		assertNoHardcoverBookSearches(t, hc)
	})

	for _, checkpointKeys := range []struct {
		name      string
		base      bool
		composite bool
	}{
		{name: "base only", base: true},
		{name: "composite only", composite: true},
		{name: "base and composite", base: true, composite: true},
	} {
		t.Run("unassociated ISBN checkpoint cannot suppress rematching/"+checkpointKeys.name, func(t *testing.T) {
			svc, hc, abs := newSyncBookService(t)
			svc.config.Sync.Incremental = true
			svc.config.Sync.ProcessUnreadBooks = true
			book := toAudiobookshelfBook(createTestBook("resync-current-migrated", "Current", "Author", "", "9780306406157"))
			current := state.NewState()
			for key, enabled := range map[string]bool{
				book.ID:          checkpointKeys.base,
				book.ID + ":200": checkpointKeys.composite,
			} {
				if enabled {
					current.Books[key] = state.Book{Status: "WANT_TO_READ", HasProgressSeconds: true}
				}
			}
			current.UpdateBook("other-item:201", 0.5, "IN_PROGRESS")
			abs.On("GetUserProgress", mock.Anything).Return(&models.AudiobookshelfUserProgress{}, nil).Once()
			hc.On("SearchBookByISBN13", mock.Anything, "9780306406157").Return((*models.HardcoverBook)(nil), nil).Once()
			hc.On("SearchBookByISBN10", mock.Anything, "0306406152").Return((*models.HardcoverBook)(nil), nil).Once()
			hc.On("SearchBooks", mock.Anything, "Current Author", "").Return(nil, nil).Once()
			statePath := filepath.Join(t.TempDir(), "state.json")

			result, err := svc.SyncBook(context.Background(), *book, current, statePath)

			require.NoError(t, err)
			assert.Equal(t, OutcomeNotFound, result.Outcome)
			stored, loadErr := state.LoadState(statePath)
			require.NoError(t, loadErr)
			for key, enabled := range map[string]bool{
				book.ID:          checkpointKeys.base,
				book.ID + ":200": checkpointKeys.composite,
			} {
				checkpoint, exists := stored.GetBookState(key)
				assert.Equal(t, enabled, exists)
				if enabled {
					assert.Equal(t, state.Book{Status: "WANT_TO_READ", HasProgressSeconds: true}, checkpoint)
				}
			}
			assert.Contains(t, stored.Books, "other-item:201", "preserve other items' checkpoints")
			// Only read-only lookups are configured. Retained checkpoints must
			// neither suppress the not-found result nor cause any mutation.
			hc.AssertExpectations(t)
		})
	}

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
		book := toAudiobookshelfBook(createTestBook("resync-no-progress", "No Progress", "Author", "no-progress-asin", ""))
		abs.On("GetUserProgress", mock.Anything).Return((*models.AudiobookshelfUserProgress)(nil), errors.New("abs down")).Once()
		current := state.NewState()
		current.UpdateBook("existing-book", 0.25, "READING")
		statePath := filepath.Join(t.TempDir(), "state.json")
		require.NoError(t, current.Save(statePath))
		before, err := os.ReadFile(statePath)
		require.NoError(t, err)

		_, err = svc.SyncBook(context.Background(), *book, current, statePath)

		require.ErrorContains(t, err, "abs down")
		after, readErr := os.ReadFile(statePath)
		require.NoError(t, readErr)
		assert.Equal(t, before, after, "an unreliable progress failure must not checkpoint state")
		assert.NotContains(t, current.Books, book.ID)
		assertNoHardcoverBookSearches(t, hc)
	})

	t.Run("cancellation during failed progress fetch wins over embedded fallback", func(t *testing.T) {
		svc, hc, abs := newSyncBookService(t)
		book := inProgressBook("resync-canceled-progress-error")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		abs.On("GetUserProgress", mock.Anything).Run(func(mock.Arguments) { cancel() }).
			Return((*models.AudiobookshelfUserProgress)(nil), errors.New("request canceled")).Once()
		statePath := filepath.Join(t.TempDir(), "state.json")

		_, err := svc.SyncBook(ctx, *book, state.NewState(), statePath)

		require.ErrorIs(t, err, context.Canceled)
		_, statErr := os.Stat(statePath)
		assert.True(t, os.IsNotExist(statErr), "canceled resync must not checkpoint state")
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

// Model the completion-date default implicated by the Salvage Merc One logs.
// Verify the concrete client sends the historical date with the final status,
// rather than relying on the preceding read insertion to supply it.
func TestSyncBookWithEditionPreservesFinishedDateAcrossStatusTransition(t *testing.T) {
	const finishedDate = "2025-06-01"
	svc, _, abs := newSyncBookService(t)
	book := toAudiobookshelfBook(createTestFinishedBook("resync-finished-date", "Salvage Merc One", "Jake Bible", "B0FRJZ7HJY", ""))
	book.Progress.StartedAt = 1748790312628
	book.Progress.FinishedAt = 1748790312628
	abs.On("GetUserProgress", mock.Anything).Return(&models.AudiobookshelfUserProgress{}, nil).Once()
	current := state.NewState()
	require.NoError(t, current.SetAssociation(state.Association{
		ABSItemID: book.ID, SourceASIN: book.Media.Metadata.ASIN,
		HardcoverBookID: "1852014", HardcoverEditionID: "33360390", ReadingFormat: models.ReadingFormatAudiobook,
	}))
	var finalDate string
	var created, transitioned bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		response := `{"data":{"user_books":[]}}`
		switch {
		case strings.Contains(req.Query, "GetCurrentUserID"):
			response = `{"data":{"me":[{"id":99}]}}`
		case strings.Contains(req.Query, "InsertUserBookRead"):
			dates := req.Variables["user_book_read"].(map[string]interface{})
			finalDate, _ = dates["finished_at"].(string)
			assert.Equal(t, finishedDate, finalDate)
			response = `{"data":{"insert_user_book_read":{"id":6998539,"error":null}}}`
		case strings.Contains(req.Query, "InsertUserBook"):
			object := req.Variables["object"].(map[string]interface{})
			assert.Equal(t, float64(1), object["status_id"], "create as WANT_TO_READ")
			created = true
			response = `{"data":{"insert_user_book":{"id":19149476,"user_book":{"id":19149476,"status_id":1},"error":null}}}`
		case strings.Contains(req.Query, "UpdateUserBookStatus"):
			transitioned = true
			finalDate = "2026-10-01"
			if object, ok := req.Variables["object"].(map[string]interface{}); ok {
				if date, ok := object["last_read_date"].(string); ok {
					finalDate = date
				}
			}
			response = `{"data":{"update_user_book":{"id":19149476,"error":null}}}`
		case strings.Contains(req.Query, "GetUserBookReads"):
			if finalDate != "" {
				response = `{"data":{"user_book_reads":[{"id":6998539,"started_at":"2025-06-01","finished_at":"` + finalDate + `","progress":100,"progress_seconds":24960}]}}`
			} else {
				response = `{"data":{"user_book_reads":[]}}`
			}
		case strings.Contains(req.Query, "GetUserBook("):
			response = `{"data":{"user_books":[{"id":19149476,"book_id":1852014,"edition_id":33360390,"status_id":1}]}}`
		case strings.Contains(req.Query, "GetUserBookByBookOnly"), strings.Contains(req.Query, "GetUserBookByBook("), strings.Contains(req.Query, "GetUserBookByEdition"):
			if created {
				response = `{"data":{"user_books":[{"id":19149476,"book_id":1852014,"edition_id":33360390}]}}`
			}
		default:
			t.Errorf("unexpected operation: %s", req.Query)
			http.Error(w, "unexpected operation", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()
	cfg := hardcover.DefaultClientConfig()
	cfg.BaseURL, cfg.RateLimit, cfg.MaxRetries = server.URL, time.Nanosecond, 0
	svc.hardcover = hardcover.NewClientWithConfig(cfg, "test-token", logger.Get())
	statePath := filepath.Join(t.TempDir(), "state.json")
	result, err := svc.SyncBookWithEdition(context.Background(), *book, &models.Edition{ID: "33360390", BookID: "1852014", ReadingFormatID: "2"}, current, statePath)
	require.NoError(t, err)
	require.Equal(t, OutcomeSynced, result.Outcome, result.Error)
	assert.True(t, transitioned)
	assert.Equal(t, finishedDate, finalDate)
	stored, err := state.LoadState(statePath)
	require.NoError(t, err)
	assert.Equal(t, "FINISHED", stored.Books[book.ID+":33360390"].Status)
	abs.AssertExpectations(t)
}
