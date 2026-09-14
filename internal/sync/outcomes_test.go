package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func recordedOutcome(svc *Service, bookID string) BookOutcomeRecord {
	svc.summary.RLock()
	defer svc.summary.RUnlock()
	return svc.outcomeRecords[bookID]
}

func expectASINMatch(mockClient *MockHardcoverClient, asin string, bookID, editionID string, userBookID int) {
	mockClient.On("SearchBookByASIN", mock.Anything, asin).Return(&models.HardcoverBook{
		ID: bookID, EditionID: editionID,
	}, nil).Once()
	mockClient.On("GetEdition", mock.Anything, editionID).Return(&models.Edition{
		ID: editionID, BookID: bookID,
	}, nil).Times(3)
	mockClient.On("GetUserBookID", mock.Anything, mock.AnythingOfType("int")).Return(userBookID, nil).Times(3)
}

func TestProcessBookRecordsSkipAndIncrementalNoChange(t *testing.T) {
	t.Run("skip", func(t *testing.T) {
		svc, hc := createTestService()
		svc.config.Sync.ProcessUnreadBooks = false
		book := toAudiobookshelfBook(createTestBook("outcome-skip", "Unread", "Author", "skip-asin", ""))
		err := svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{})
		require.NoError(t, err)
		assert.Equal(t, OutcomeSkipped, recordedOutcome(svc, book.ID).Outcome)
		assert.Equal(t, int32(1), svc.summary.TotalBooksProcessed)
		hc.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
	})

	t.Run("incremental no change", func(t *testing.T) {
		svc, hc := createTestService()
		svc.config.Sync.Incremental = true
		svc.config.Sync.ProcessUnreadBooks = true
		book := toAudiobookshelfBook(createTestBook("outcome-current", "Current", "Author", "", ""))
		svc.state.UpdateBook(book.ID, 0, "WANT_TO_READ")
		svc.state.SetHasProgressSeconds(book.ID)
		require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))
		assert.Equal(t, OutcomeAlreadyCurrent, recordedOutcome(svc, book.ID).Outcome)
		hc.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
	})
}

func TestProcessBookRecordsSuccessfulMutation(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	testBook := createTestBook("outcome-success", "Progress", "Author", "outcome-success-asin", "")
	testBook.Media.Duration = 1000
	testBook.Progress.CurrentTime = 300
	book := toAudiobookshelfBook(testBook)
	expectASINMatch(hc, "outcome-success-asin", "100", "200", 300)
	hc.On("GetUserBook", mock.Anything, "300").Return(&models.HardcoverBook{
		ID: "100", EditionID: "200", BookStatusID: 2,
	}, nil).Once()
	editionID := int64(200)
	progress := 100
	hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 300}).Return([]hardcover.UserBookRead{{
		ID: 400, EditionID: &editionID, ProgressSeconds: &progress,
	}}, nil).Once()
	hc.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == 400 && input.Object["progress_seconds"] == int64(300)
	})).Return(true, nil).Once()

	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))
	assert.Equal(t, OutcomeSynced, recordedOutcome(svc, book.ID).Outcome)
	assert.Equal(t, int32(1), svc.outcomeCounts.Synced)
	hc.AssertExpectations(t)
}

func TestProcessBookSeparatesNotFoundAndTechnicalLookupFailure(t *testing.T) {
	newBook := func(id string) models.AudiobookshelfBook {
		book := createTestBook(id, id, "Author", id+"-asin", id+"-isbn")
		book.Progress.CurrentTime = 300
		return *toAudiobookshelfBook(book)
	}

	t.Run("conclusive no result", func(t *testing.T) {
		svc, hc := createTestService()
		book := newBook("outcome-not-found")
		hc.On("SearchBookByASIN", mock.Anything, "outcome-not-found-asin").Return((*models.HardcoverBook)(nil), nil).Once()
		hc.On("SearchBookByISBN13", mock.Anything, "outcome-not-found-isbn").Return((*models.HardcoverBook)(nil), nil).Once()
		hc.On("SearchBookByISBN10", mock.Anything, "outcome-not-found-isbn").Return((*models.HardcoverBook)(nil), nil).Once()
		hc.On("SearchBooks", mock.Anything, "outcome-not-found Author", "").Return([]models.HardcoverBook{}, nil).Once()
		require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
		assert.Equal(t, OutcomeNotFound, recordedOutcome(svc, book.ID).Outcome)
	})

	t.Run("technical lookup failure", func(t *testing.T) {
		svc, hc := createTestService()
		book := newBook("outcome-lookup-failed")
		hc.On("SearchBookByASIN", mock.Anything, "outcome-lookup-failed-asin").Return((*models.HardcoverBook)(nil), errors.New("temporary API failure")).Once()
		hc.On("SearchBookByISBN13", mock.Anything, "outcome-lookup-failed-isbn").Return((*models.HardcoverBook)(nil), errors.New("temporary API failure")).Once()
		hc.On("SearchBookByISBN10", mock.Anything, "outcome-lookup-failed-isbn").Return((*models.HardcoverBook)(nil), nil).Once()
		hc.On("SearchBooks", mock.Anything, "outcome-lookup-failed Author", "").Return([]models.HardcoverBook{}, nil).Once()
		require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
		record := recordedOutcome(svc, book.ID)
		assert.Equal(t, OutcomeFailed, record.Outcome)
		assert.Contains(t, record.Error, "temporary API failure")
	})
}

func TestProcessBookProgressReadFailureIsTechnicalFailure(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	book := createTestBook("outcome-progress-failed", "Progress Failure", "Author", "progress-failed-asin", "")
	book.Media.Duration = 1000
	book.Progress.CurrentTime = 300
	absBook := toAudiobookshelfBook(book)
	expectASINMatch(hc, "progress-failed-asin", "101", "201", 301)
	hc.On("GetUserBook", mock.Anything, "301").Return(&models.HardcoverBook{
		ID: "101", EditionID: "201", BookStatusID: 2,
	}, nil).Once()
	progressErr := errors.New("progress endpoint unavailable")
	hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 301}).Return(nil, progressErr).Once()

	err := svc.processBook(context.Background(), *absBook, &models.AudiobookshelfUserProgress{})
	assert.ErrorIs(t, err, progressErr)
	assert.Equal(t, OutcomeFailed, recordedOutcome(svc, absBook.ID).Outcome)
	assert.NotContains(t, svc.state.Books, absBook.ID)
}

func TestProcessBookProgressWriteFailureIsTechnicalFailure(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	book := createTestBook("outcome-progress-write-failed", "Progress Write Failure", "Author", "progress-write-failed-asin", "")
	book.Media.Duration = 1000
	book.Progress.CurrentTime = 300
	absBook := toAudiobookshelfBook(book)
	expectASINMatch(hc, "progress-write-failed-asin", "104", "204", 304)
	hc.On("GetUserBook", mock.Anything, "304").Return(&models.HardcoverBook{
		ID: "104", EditionID: "204", BookStatusID: 2,
	}, nil).Once()
	editionID := int64(204)
	progress := 100
	hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 304}).Return([]hardcover.UserBookRead{{
		ID: 404, EditionID: &editionID, ProgressSeconds: &progress,
	}}, nil).Once()
	writeErr := errors.New("progress mutation rejected")
	hc.On("UpdateUserBookRead", mock.Anything, mock.Anything).Return(false, writeErr).Once()

	err := svc.processBook(context.Background(), *absBook, &models.AudiobookshelfUserProgress{})
	assert.ErrorIs(t, err, writeErr)
	assert.Equal(t, OutcomeFailed, recordedOutcome(svc, absBook.ID).Outcome)
	assert.NotContains(t, svc.state.Books, absBook.ID)
}

func TestProcessBookDryRunRecordsActionWithoutMutationOrState(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.DryRun = true
	svc.config.Sync.SyncOwned = false
	book := createTestBook("outcome-dry-run", "Dry Run", "Author", "dry-run-asin", "")
	book.Media.Duration = 1000
	book.Progress.CurrentTime = 300
	absBook := toAudiobookshelfBook(book)
	expectASINMatch(hc, "dry-run-asin", "102", "202", 302)
	hc.On("GetUserBook", mock.Anything, "302").Return(&models.HardcoverBook{
		ID: "102", EditionID: "202", BookStatusID: 2,
	}, nil).Once()
	editionID := int64(202)
	progress := 100
	hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 302}).Return([]hardcover.UserBookRead{{
		ID: 402, EditionID: &editionID, ProgressSeconds: &progress,
	}}, nil).Once()

	require.NoError(t, svc.processBook(context.Background(), *absBook, &models.AudiobookshelfUserProgress{}))
	assert.Equal(t, OutcomeWouldSync, recordedOutcome(svc, absBook.ID).Outcome)
	assert.NotContains(t, svc.state.Books, absBook.ID)
	hc.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
}

func TestProcessLibraryCandidateDenominatorIgnoresLimit(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.ProcessUnreadBooks = false
	mockABS := new(MockAudiobookshelfClient)
	svc.audiobookshelf = mockABS
	first := toAudiobookshelfBook(createTestBook("candidate-one", "One", "Author", "", ""))
	second := toAudiobookshelfBook(createTestBook("candidate-two", "Two", "Author", "", ""))
	mockABS.On("GetLibraryItems", mock.Anything, "library").Return([]models.AudiobookshelfBook{*first, *second}, nil).Once()

	processed, err := svc.processLibrary(context.Background(), &audiobookshelf.AudiobookshelfLibrary{ID: "library", Name: "Library"}, 1, &models.AudiobookshelfUserProgress{})
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.Equal(t, int32(2), svc.summary.BooksTotal)
	assert.Equal(t, int32(1), svc.summary.TotalBooksProcessed)
	hc.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
}

func TestProcessBookOutcomeStaleRereadMutationWinsOverNoOp(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	book := createTestBook("outcome-stale-reread", "Stale Reread", "Author", "stale-reread-asin", "")
	book.Media.Duration = 1000
	book.Progress.CurrentTime = 500
	book.Progress.StartedAt = 1767225600000 // 2026-01-01
	absBook := toAudiobookshelfBook(book)
	expectASINMatch(hc, "stale-reread-asin", "103", "203", 303)
	hc.On("GetUserBook", mock.Anything, "303").Return(&models.HardcoverBook{
		ID: "103", EditionID: "203", BookStatusID: 2,
	}, nil).Once()
	editionID := int64(203)
	oldStart := "2025-01-01"
	finished := "2025-02-01"
	oldProgress := 200
	hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 303}).Return([]hardcover.UserBookRead{
		{ID: 403, EditionID: &editionID, StartedAt: &oldStart, ProgressSeconds: &oldProgress},
		{ID: 404, EditionID: &editionID, StartedAt: &finished, FinishedAt: &finished, ProgressSeconds: &oldProgress},
	}, nil).Once()
	hc.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == 403 && input.Object["finished_at"] == finished
	})).Return(true, nil).Once()
	hc.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		return input.UserBookID == 303 && input.DatesRead.ProgressSeconds != nil && *input.DatesRead.ProgressSeconds == 500
	})).Return(405, nil).Once()

	require.NoError(t, svc.processBook(context.Background(), *absBook, &models.AudiobookshelfUserProgress{}))
	assert.Equal(t, OutcomeSynced, recordedOutcome(svc, absBook.ID).Outcome)
	assert.Equal(t, int32(1), svc.outcomeCounts.Synced)
}
