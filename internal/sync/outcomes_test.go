package sync

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func recordedOutcome(svc *Service, bookID string) BookOutcomeRecord {
	for _, record := range svc.GetSnapshot().BookOutcomes {
		if record.BookID == bookID {
			return record
		}
	}
	return BookOutcomeRecord{}
}

func expectASINMatch(mockClient *MockHardcoverClient, asin string, bookID, editionID string, userBookID int) {
	mockClient.On("SearchBookByASIN", mock.Anything, asin).Return(&models.HardcoverBook{
		ID: bookID, EditionID: editionID,
	}, nil).Once()
	mockClient.On("GetEdition", mock.Anything, editionID).Return(&models.Edition{
		ID: editionID, BookID: bookID,
	}, nil)
	mockClient.On("GetUserBookID", mock.Anything, mock.AnythingOfType("int")).Return(userBookID, nil)
}

func assertNoHardcoverBookSearches(t *testing.T, mockClient *MockHardcoverClient) {
	t.Helper()
	mockClient.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "SearchBooks", mock.Anything, mock.Anything, mock.Anything)
}

func TestProcessBookRecordsSkipAndIncrementalNoChange(t *testing.T) {
	t.Run("skip", func(t *testing.T) {
		svc, hc := createTestService()
		svc.config.Sync.ProcessUnreadBooks = false
		book := toAudiobookshelfBook(createTestBook("outcome-skip", "Unread", "Author", "skip-asin", ""))
		err := svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{})
		require.NoError(t, err)
		assert.Equal(t, OutcomeSkipped, recordedOutcome(svc, book.ID).Outcome)
		assert.Equal(t, int32(1), svc.outcomeCounts.Total())
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

func TestProcessBookFinishedWithoutFinishedAtSkipsHardcoverMatching(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false

	book := toAudiobookshelfBook(createTestFinishedBook(
		"outcome-finished-without-date", "Finished Without Date", "Author", "missing-date-asin", "",
	))
	book.Progress.FinishedAt = 0

	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))

	record := recordedOutcome(svc, book.ID)
	assert.Equal(t, OutcomeSkipped, record.Outcome)
	assert.Equal(t, "finished book has no Audiobookshelf finished_at", record.Reason)
	assertNoHardcoverBookSearches(t, hc)
	hc.AssertNotCalled(t, "GetUserBookID", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "CreateUserBook", mock.Anything, mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
	_, exists := svc.state.GetBookState(book.ID + ":200")
	assert.False(t, exists, "a skipped missing date must remain eligible for a later sync")
	hc.AssertExpectations(t)
}

func TestProcessBookDryRunFinishedWithoutFinishedAtSkipsHardcoverMatching(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.DryRun = true
	svc.config.Sync.SyncOwned = false

	book := toAudiobookshelfBook(createTestFinishedBook(
		"outcome-dry-run-finished-without-date", "Dry Run Finished Without Date", "Author", "", "9781234567890",
	))
	book.Progress.FinishedAt = 0

	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))

	record := recordedOutcome(svc, book.ID)
	assert.Equal(t, OutcomeSkipped, record.Outcome)
	assert.Equal(t, "finished book has no Audiobookshelf finished_at", record.Reason)
	assertNoHardcoverBookSearches(t, hc)
	for _, call := range []struct {
		method string
		args   []interface{}
	}{
		{method: "MarkEditionAsOwned", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "GetUserBookID", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "CreateUserBook", args: []interface{}{mock.Anything, mock.Anything, mock.Anything}},
		{method: "UpdateUserBookEdition", args: []interface{}{mock.Anything, mock.Anything, mock.Anything}},
		{method: "GetUserBook", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "GetUserBookReads", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "CheckExistingUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "InsertUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "UpdateUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "DeleteUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "UpdateUserBookStatus", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "UpdateReadingProgress", args: []interface{}{mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything}},
	} {
		hc.AssertNotCalled(t, call.method, call.args...)
	}
	_, compositeStateExists := svc.state.GetBookState(book.ID + ":204")
	assert.False(t, compositeStateExists, "a skipped missing date must not checkpoint composite sync state")
	_, baseStateExists := svc.state.GetBookState(book.ID)
	assert.False(t, baseStateExists, "a skipped missing date must not checkpoint base sync state")
	hc.AssertExpectations(t)
}

func TestProcessBookFinishedWithoutFinishedAtSkipsBeforeEditionDiagnostics(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false

	book := toAudiobookshelfBook(createTestBook(
		"outcome-finished-without-date-no-edition", "Finished Without Date", "Author", "", "9781234567890",
	))
	book.Progress.CurrentTime = book.Media.Duration
	book.Progress.FinishedAt = 0

	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))

	record := recordedOutcome(svc, book.ID)
	assert.Equal(t, OutcomeSkipped, record.Outcome)
	assert.Equal(t, "finished book has no Audiobookshelf finished_at", record.Reason)
	assertNoHardcoverBookSearches(t, hc)
	assert.Empty(t, svc.mismatchCollector.GetAll())
	_, baseStateExists := svc.state.GetBookState(book.ID)
	assert.False(t, baseStateExists, "an undated finished mismatch must remain eligible for a later sync")
	hc.AssertNotCalled(t, "GetUserBookID", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
	hc.AssertExpectations(t)
}

func TestProcessBookExplicitlyFinishedWithoutFinishedAtAndDurationSkipsHardcoverMatching(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = true

	book := toAudiobookshelfBook(createTestBook(
		"outcome-finished-without-date-zero-duration", "Finished Without Date", "Author", "missing-date-zero-duration-asin", "",
	))
	book.Media.Duration = 0
	book.Progress.IsFinished = true
	book.Progress.FinishedAt = 0

	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))

	record := recordedOutcome(svc, book.ID)
	assert.Equal(t, OutcomeSkipped, record.Outcome)
	assert.Equal(t, "finished book has no Audiobookshelf finished_at", record.Reason)
	assertNoHardcoverBookSearches(t, hc)
	for _, call := range []struct {
		method string
		args   []interface{}
	}{
		{method: "GetUserBookID", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "CreateUserBook", args: []interface{}{mock.Anything, mock.Anything, mock.Anything}},
		{method: "UpdateUserBookEdition", args: []interface{}{mock.Anything, mock.Anything, mock.Anything}},
		{method: "GetUserBook", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "GetUserBookReads", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "CheckExistingUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "InsertUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "UpdateUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "DeleteUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "UpdateUserBookStatus", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "UpdateReadingProgress", args: []interface{}{mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything}},
	} {
		hc.AssertNotCalled(t, call.method, call.args...)
	}
	_, compositeStateExists := svc.state.GetBookState(book.ID + ":203")
	assert.False(t, compositeStateExists, "a skipped missing date must not checkpoint composite sync state")
	_, baseStateExists := svc.state.GetBookState(book.ID)
	assert.False(t, baseStateExists, "a skipped missing date must not checkpoint base sync state")
	hc.AssertExpectations(t)
}

func TestProcessBookSkipsComputedFinishedWithoutFinishedAt(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false

	book := toAudiobookshelfBook(createTestBook(
		"outcome-computed-finished-without-date", "Computed Finished Without Date", "Author", "computed-finished-asin", "",
	))
	book.Media.Duration = 1000
	book.Progress.CurrentTime = 1000
	book.Progress.IsFinished = false
	book.Progress.FinishedAt = 0

	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))

	record := recordedOutcome(svc, book.ID)
	assert.Equal(t, OutcomeSkipped, record.Outcome)
	assert.Equal(t, "finished book has no Audiobookshelf finished_at", record.Reason)
	assertNoHardcoverBookSearches(t, hc)
	hc.AssertNotCalled(t, "GetEdition", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "GetUserBookID", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "CreateUserBook", mock.Anything, mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "GetUserBook", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "GetUserBookReads", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
	_, compositeStateExists := svc.state.GetBookState(book.ID + ":201")
	assert.False(t, compositeStateExists, "a skipped missing date must not checkpoint sync state")
	_, baseStateExists := svc.state.GetBookState(book.ID)
	assert.False(t, baseStateExists, "a skipped missing date must not checkpoint base sync state")
	hc.AssertExpectations(t)
}

func TestProcessBookEnhancedComputedFinishedWithoutFinishedAtSkipsHardcoverMatching(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = true

	book := toAudiobookshelfBook(createTestBook(
		"outcome-enhanced-computed-finished-without-date", "Enhanced Computed Finished Without Date", "Author", "enhanced-computed-finished-asin", "",
	))
	userProgress := &models.AudiobookshelfUserProgress{}
	userProgress.MediaProgress = append(userProgress.MediaProgress, struct {
		ID            string  `json:"id"`
		LibraryItemID string  `json:"libraryItemId"`
		UserID        string  `json:"userId"`
		IsFinished    bool    `json:"isFinished"`
		Progress      float64 `json:"progress"`
		CurrentTime   float64 `json:"currentTime"`
		Duration      float64 `json:"duration"`
		StartedAt     int64   `json:"startedAt"`
		FinishedAt    int64   `json:"finishedAt"`
		LastUpdate    int64   `json:"lastUpdate"`
		TimeListening float64 `json:"timeListening"`
	}{
		LibraryItemID: book.ID,
		CurrentTime:   book.Media.Duration,
		LastUpdate:    1,
	})

	require.NoError(t, svc.processBook(context.Background(), *book, userProgress))

	record := recordedOutcome(svc, book.ID)
	assert.Equal(t, OutcomeSkipped, record.Outcome)
	assert.Equal(t, "finished book has no Audiobookshelf finished_at", record.Reason)
	assertNoHardcoverBookSearches(t, hc)
	hc.AssertNotCalled(t, "CheckBookOwnership", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "MarkEditionAsOwned", mock.Anything, mock.Anything)
	_, stateExists := svc.state.GetBookState(book.ID)
	assert.False(t, stateExists, "an enhanced missing date must not checkpoint sync state")
	hc.AssertExpectations(t)
}

func TestProcessBookComputedFinishedWithoutFinishedAtSkipsBeforeISBNOwnership(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = true

	book := toAudiobookshelfBook(createTestBook(
		"outcome-isbn-computed-finished-without-date", "Computed Finished Without Date", "Author", "", "9781234567890",
	))
	book.Media.Duration = 1000
	book.Progress.CurrentTime = book.Media.Duration
	book.Progress.IsFinished = false
	book.Progress.FinishedAt = 0

	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))

	record := recordedOutcome(svc, book.ID)
	assert.Equal(t, OutcomeSkipped, record.Outcome)
	assert.Equal(t, "finished book has no Audiobookshelf finished_at", record.Reason)
	assertNoHardcoverBookSearches(t, hc)
	for _, call := range []struct {
		method string
		args   []interface{}
	}{
		{method: "CheckBookOwnership", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "MarkEditionAsOwned", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "GetUserBookID", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "CreateUserBook", args: []interface{}{mock.Anything, mock.Anything, mock.Anything}},
		{method: "UpdateUserBookEdition", args: []interface{}{mock.Anything, mock.Anything, mock.Anything}},
		{method: "GetUserBook", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "GetUserBookReads", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "CheckExistingUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "InsertUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "UpdateUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "DeleteUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "UpdateUserBookStatus", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "UpdateReadingProgress", args: []interface{}{mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything}},
	} {
		hc.AssertNotCalled(t, call.method, call.args...)
	}
	_, compositeStateExists := svc.state.GetBookState(book.ID + ":202")
	assert.False(t, compositeStateExists, "a skipped missing date must not checkpoint sync state")
	_, baseStateExists := svc.state.GetBookState(book.ID)
	assert.False(t, baseStateExists, "a skipped missing date must not checkpoint base sync state")
	hc.AssertExpectations(t)
}

func TestProcessBookClassifiesProgressMutationOutcomes(t *testing.T) {
	readErr := errors.New("progress endpoint unavailable")
	writeErr := errors.New("progress mutation rejected")
	for _, tt := range []struct {
		name            string
		dryRun          bool
		readErr         error
		writeErr        error
		expectedOutcome SyncOutcome
		expectedErr     error
	}{
		{name: "synced", expectedOutcome: OutcomeSynced},
		{name: "read failure", readErr: readErr, expectedOutcome: OutcomeFailed, expectedErr: readErr},
		{name: "write failure", writeErr: writeErr, expectedOutcome: OutcomeFailed, expectedErr: writeErr},
		{name: "dry run", dryRun: true, expectedOutcome: OutcomeWouldSync},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, hc := createTestService()
			svc.config.Sync.SyncOwned = false
			svc.config.Sync.DryRun = tt.dryRun
			book := toAudiobookshelfBook(createTestBook(
				"outcome-"+tt.name, "Progress", "Author", "outcome-"+tt.name+"-asin", ""))
			book.Media.Duration = 1000
			book.Progress.CurrentTime = 300
			expectASINMatch(hc, book.Media.Metadata.ASIN, "100", "200", 300)
			hc.On("GetUserBook", mock.Anything, "300").Return(&models.HardcoverBook{
				ID: "100", EditionID: "200", BookStatusID: 2,
			}, nil).Once()
			if tt.readErr != nil {
				hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 300}).Return(nil, tt.readErr).Once()
			} else {
				editionID := int64(200)
				progress := 100
				hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 300}).Return([]hardcover.UserBookRead{{
					ID: 400, EditionID: &editionID, ProgressSeconds: &progress,
				}}, nil).Once()
				if !tt.dryRun {
					hc.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
						return input.ID == 400 && input.Object["progress_seconds"] == int64(300)
					})).Return(tt.writeErr == nil, tt.writeErr).Once()
				}
			}

			err := svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{})
			if tt.expectedErr != nil {
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.expectedOutcome, recordedOutcome(svc, book.ID).Outcome)
			assert.Equal(t, int32(1), svc.outcomeCounts.Total())
			switch tt.expectedOutcome {
			case OutcomeSynced:
				assert.Equal(t, int32(1), svc.outcomeCounts.Synced)
			case OutcomeFailed:
				assert.Equal(t, int32(1), svc.outcomeCounts.Failed)
			case OutcomeWouldSync:
				assert.Equal(t, int32(1), svc.outcomeCounts.WouldSync)
			}
			if tt.expectedOutcome != OutcomeSynced {
				assert.NotContains(t, svc.state.Books, book.ID)
			}
			if tt.dryRun {
				hc.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.Anything)
				hc.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
				hc.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
			}
			hc.AssertExpectations(t)
		})
	}
}

func TestProcessBookRecordsEbookOutcomeFormat(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	svc.config.Sync.IncludeEbooks = true

	testBook := createTestBook("outcome-ebook", "Ebook", "Author", "outcome-ebook-asin", "")
	testBook.MediaType = "ebook"
	testBook.Media.Duration = 1000
	testBook.Progress.CurrentTime = 300
	book := toAudiobookshelfBook(testBook)
	expectASINMatch(hc, testBook.Media.Metadata.ASIN, "100", "200", 300)
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
	record := recordedOutcome(svc, book.ID)
	assert.Equal(t, OutcomeSynced, record.Outcome)
	assert.Equal(t, "Ebook", record.Format)
	hc.AssertExpectations(t)
}

func TestProcessBookSkipsAudiobookshelfEbooksUnlessIncluded(t *testing.T) {
	// Audiobookshelf reports mediaType "book" for ebooks; only the media
	// payload distinguishes them.
	var book models.AudiobookshelfBook
	require.NoError(t, json.Unmarshal([]byte(`{
		"id": "outcome-ebook-excluded",
		"mediaType": "book",
		"media": {"metadata": {"title": "Ebook", "authorName": "Author", "asin": "outcome-ebook-excluded-asin"}, "ebookFile": {"ebookFormat": "epub"}}
	}`), &book))

	svc, hc := createTestService()
	svc.config.Sync.IncludeEbooks = false

	err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})
	require.ErrorIs(t, err, ErrSkippedBook)
	assert.Equal(t, OutcomeSkipped, recordedOutcome(svc, book.ID).Outcome)
	assert.Equal(t, "Ebook", recordedOutcome(svc, book.ID).Format)
	hc.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
}

func TestProcessBookThresholdSkipRecordsSkipped(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	testBook := createTestBook("outcome-threshold", "Progress", "Author", "outcome-threshold-asin", "")
	testBook.Media.Duration = 1000
	testBook.Progress.CurrentTime = 300
	book := toAudiobookshelfBook(testBook)
	expectASINMatch(hc, "outcome-threshold-asin", "110", "210", 310)
	hc.On("GetUserBook", mock.Anything, "310").Return(&models.HardcoverBook{
		ID: "110", EditionID: "210", BookStatusID: 2,
	}, nil).Once()
	editionID := int64(210)
	progress := 250
	hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 310}).Return([]hardcover.UserBookRead{{
		ID: 410, EditionID: &editionID, ProgressSeconds: &progress,
	}}, nil).Once()

	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))
	assert.Equal(t, OutcomeSkipped, recordedOutcome(svc, book.ID).Outcome)
	assert.Equal(t, int32(0), svc.outcomeCounts.Synced)
	assert.Equal(t, int32(1), svc.outcomeCounts.Skipped)
	hc.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
	hc.AssertExpectations(t)
}

func TestRecordBookOutcomeReplacementClearsPreviousError(t *testing.T) {
	svc, _ := createTestService()
	book := *toAudiobookshelfBook(createTestBook("outcome-replacement", "Replacement", "Author", "", ""))

	svc.recordBookOutcomeWithMatchMethod(book, OutcomeFailed, "temporary failure", errors.New("temporary failure"), nil, "")
	svc.recordBookOutcomeWithMatchMethod(book, OutcomeSynced, "completed", nil, nil, "")

	record := recordedOutcome(svc, book.ID)
	assert.Equal(t, OutcomeSynced, record.Outcome)
	assert.Empty(t, record.Error)
	assert.Equal(t, int32(1), svc.outcomeCounts.Synced)
	assert.Equal(t, int32(0), svc.outcomeCounts.Failed)
	assert.Equal(t, int32(1), svc.outcomeCounts.Synced)
	assert.Equal(t, int32(1), svc.outcomeCounts.Total())
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

func TestProcessBookKeepsIdentifierFailureWhenTitleSearchFindsCandidate(t *testing.T) {
	svc, hc := createTestService()
	book := createTestBook("outcome-incomplete-lookup", "Possible Match", "Author", "failed-asin", "")
	book.Progress.CurrentTime = 300
	absBook := toAudiobookshelfBook(book)
	lookupErr := errors.New("identifier lookup unavailable")
	hc.On("SearchBookByASIN", mock.Anything, "failed-asin").Return((*models.HardcoverBook)(nil), lookupErr).Once()
	hc.On("SearchBooks", mock.Anything, "Possible Match Author", "").Return([]models.HardcoverBook{{
		ID: "901", Title: "Possible Match", Slug: "possible-match",
	}}, nil).Once()
	hc.On("GetBookByID", mock.Anything, "901").Return(&models.HardcoverBook{
		ID: "901", Title: "Possible Match", Slug: "possible-match",
	}, nil).Once()

	require.NoError(t, svc.processBook(context.Background(), *absBook, &models.AudiobookshelfUserProgress{}))
	record := recordedOutcome(svc, absBook.ID)
	assert.Equal(t, OutcomeFailed, record.Outcome)
	assert.Contains(t, record.Error, lookupErr.Error())
	assert.Empty(t, record.MatchMethod, "title candidate cannot verify an incomplete identifier search")
	matches := svc.mismatchCollector.GetAll()
	require.Len(t, matches, 1)
	assert.Equal(t, absBook.ID, matches[0].BookID)
	assert.Equal(t, "901", matches[0].HardcoverBookID)
	assert.Equal(t, "Possible Match", matches[0].HardcoverTitle)
	assert.Equal(t, "possible-match", matches[0].HardcoverSlug)
	assert.Contains(t, matches[0].Reason, lookupErr.Error())
	snapshot := svc.GetSnapshot()
	require.Len(t, snapshot.BookOutcomes, 1)
	assert.Contains(t, snapshot.BookOutcomes[0].Reason, lookupErr.Error())
	hc.AssertExpectations(t)
}

func TestProcessBookIdentifierFailureMismatchExportsByReadingFormat(t *testing.T) {
	tests := []struct {
		name           string
		ebook          bool
		wantReading    string
		wantEdition    string
		wantAudioTotal int
	}{
		{name: "ebook is exported as an ebook edition", ebook: true, wantReading: models.ReadingFormatEbook, wantEdition: "Ebook", wantAudioTotal: 0},
		{name: "audiobook keeps the audiobook shape", ebook: false, wantReading: "", wantEdition: "Audible Audio", wantAudioTotal: 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, hc := createTestService()
			svc.config.Sync.IncludeEbooks = true
			testBook := createTestBook("lookup-failed-format", "Possible Match", "Author", "failed-asin", "")
			testBook.Media.Duration = 1000
			testBook.Progress.CurrentTime = 300
			if tt.ebook {
				testBook.MediaType = "ebook"
			}
			absBook := toAudiobookshelfBook(testBook)
			require.Equal(t, tt.ebook, absBook.IsEbook())
			lookupErr := errors.New("identifier lookup unavailable")
			hc.On("SearchBookByASIN", mock.Anything, "failed-asin").Return((*models.HardcoverBook)(nil), lookupErr).Once()
			hc.On("SearchBooks", mock.Anything, "Possible Match Author", "").Return([]models.HardcoverBook{{
				ID: "901", Title: "Possible Match", Slug: "possible-match",
			}}, nil).Once()
			hc.On("GetBookByID", mock.Anything, "901").Return(&models.HardcoverBook{
				ID: "901", Title: "Possible Match", Slug: "possible-match",
			}, nil).Once()

			require.NoError(t, svc.processBook(context.Background(), *absBook, &models.AudiobookshelfUserProgress{}))
			require.Equal(t, OutcomeFailed, recordedOutcome(svc, absBook.ID).Outcome)
			records := svc.mismatchCollector.GetAll()
			require.Len(t, records, 1)
			assert.Equal(t, tt.wantReading, records[0].ReadingFormat)

			// The export only resolves people, which are irrelevant to the format.
			hc.On("SearchAuthors", mock.Anything, mock.Anything, mock.Anything).Return([]models.Author{}, nil).Maybe()
			hc.On("SearchNarrators", mock.Anything, mock.Anything, mock.Anything).Return([]models.Author{}, nil).Maybe()
			export := records[0].ToEditionExport(context.Background(), hc)
			require.NotNil(t, export)
			assert.Equal(t, tt.wantEdition, export.EditionFormat)
			assert.Equal(t, tt.wantAudioTotal, export.AudioSeconds)
		})
	}
}

func TestProcessBookSnapshotKeepsTitleOnlyEnrichment(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	book := createTestBook("snapshot-title-only", "Title Only", "Author", "", "9781234567890")
	book.Progress.CurrentTime = 300
	absBook := toAudiobookshelfBook(book)

	hc.On("SearchBookByISBN13", mock.Anything, book.Media.Metadata.ISBN).Return((*models.HardcoverBook)(nil), nil).Once()
	hc.On("SearchBookByISBN10", mock.Anything, book.Media.Metadata.ISBN).Return((*models.HardcoverBook)(nil), nil).Once()
	hc.On("SearchBooks", mock.Anything, "Title Only Author", "").Return([]models.HardcoverBook{{
		ID: "901", Title: "Title Only Candidate", Slug: "candidate-slug",
		Authors: []models.Author{{Name: "Candidate Author"}},
	}}, nil).Once()
	hc.On("GetBookByID", mock.Anything, "901").Return(&models.HardcoverBook{
		ID: "901", Title: "Title Only Candidate", Slug: "candidate-slug",
		Authors: []models.Author{{Name: "Candidate Author"}},
	}, nil).Once()
	// AddWithMetadata enriches the local record with identifier-derived fields.
	hc.On("SearchBookByISBN13", mock.Anything, book.Media.Metadata.ISBN).Return(&models.HardcoverBook{
		ID: "904", Title: "Enriched Hardcover", Authors: []models.Author{{Name: "Enriched Author"}},
		EditionISBN13: book.Media.Metadata.ISBN,
	}, nil).Once()

	require.NoError(t, svc.processBook(context.Background(), *absBook, &models.AudiobookshelfUserProgress{}))

	snapshot := svc.GetSnapshot()
	require.Len(t, snapshot.BookOutcomes, 1)
	got := snapshot.BookOutcomes[0]
	assert.Equal(t, absBook.ID, got.BookID)
	assert.Equal(t, "904", got.HardcoverBookID)
	assert.Empty(t, got.HardcoverSlug, "candidate metadata from a different Hardcover book must not be mixed")
	assert.Equal(t, "Enriched Author", got.HardcoverAuthor)
	assert.Equal(t, OutcomeNeedsReview, snapshot.BookOutcomes[0].Outcome)
	hc.AssertExpectations(t)
}

func TestProcessBookSnapshotKeepsEnrichedSecondLookupFailure(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	book := createTestBook("snapshot-second-lookup", "Second Lookup", "Author", "", "9781234567890")
	book.Progress.CurrentTime = 300
	absBook := toAudiobookshelfBook(book)
	absBook.Media.Metadata.PublishedYear = "2023"
	absBook.Media.Metadata.Publisher = "Test Publisher"
	lookupErr := errors.New("temporary identifier failure")

	hc.On("SearchBookByISBN13", mock.Anything, book.Media.Metadata.ISBN).Return(&models.HardcoverBook{
		ID: "901", EditionID: "902",
	}, nil).Once()
	hc.On("GetEdition", mock.Anything, "902").Return(&models.Edition{
		ID: "902", BookID: "901",
	}, nil).Once()
	hc.On("GetUserBookID", mock.Anything, 902).Return(0, nil)
	hc.On("CreateUserBook", mock.Anything, "902", "IN_PROGRESS").Return("903", nil).Once()
	hc.On("SearchBookByISBN13", mock.Anything, book.Media.Metadata.ISBN).Return((*models.HardcoverBook)(nil), lookupErr).Once()
	hc.On("SearchBookByISBN10", mock.Anything, book.Media.Metadata.ISBN).Return((*models.HardcoverBook)(nil), lookupErr).Once()
	hc.On("SearchBooks", mock.Anything, "Second Lookup Author", "").Return([]models.HardcoverBook{}, nil).Once()
	// AddWithMetadata reuses the same Hardcover client to enrich the mismatch.
	hc.On("SearchPublishers", mock.Anything, "Test Publisher", 5).Return([]models.Publisher{{ID: "777", Name: "Test Publisher"}}, nil).Once()
	hc.On("SearchBookByISBN13", mock.Anything, book.Media.Metadata.ISBN).Return(&models.HardcoverBook{
		ID: "904", Title: "Hardcover Second Lookup", ReleaseDate: "2021-04-05",
		CoverImageURL: "https://example.test/cover.jpg", Authors: []models.Author{{Name: "Hardcover Author"}},
		EditionISBN13: book.Media.Metadata.ISBN,
	}, nil).Once()

	err := svc.processBook(context.Background(), *absBook, &models.AudiobookshelfUserProgress{})
	require.ErrorIs(t, err, ErrSkippedBook)

	snapshot := svc.GetSnapshot()
	require.Len(t, snapshot.BookOutcomes, 1)
	got := snapshot.BookOutcomes[0]
	assert.Equal(t, absBook.ID, got.BookID)
	assert.Equal(t, "Hardcover Second Lookup", got.HardcoverTitle)
	assert.Equal(t, "Hardcover Author", got.HardcoverAuthor)
	assert.Equal(t, "https://example.test/cover.jpg", got.HardcoverCoverURL)
	assert.Equal(t, "2021", got.HardcoverPublishedYear)
	assert.Equal(t, OutcomeFailed, snapshot.BookOutcomes[0].Outcome)
	hc.AssertExpectations(t)
}

func TestProcessBookSnapshotKeepsSecondLookupNotFoundOutOfMismatches(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	book := createTestBook("snapshot-second-lookup-not-found", "Second Lookup Not Found", "Author", "", "9781234567890")
	book.Progress.CurrentTime = 300
	absBook := toAudiobookshelfBook(book)
	absBook.Media.Metadata.Publisher = "Test Publisher"

	// The first lookup succeeds, but the later lookup used before mutation no
	// longer finds the book. The mismatch export still runs for
	// that conclusive result.
	hc.On("SearchBookByISBN13", mock.Anything, book.Media.Metadata.ISBN).Return(&models.HardcoverBook{
		ID: "901", EditionID: "902",
	}, nil).Once()
	hc.On("GetEdition", mock.Anything, "902").Return(&models.Edition{
		ID: "902", BookID: "901",
	}, nil).Once()
	hc.On("GetUserBookID", mock.Anything, 902).Return(903, nil).Once()
	hc.On("SearchBookByISBN13", mock.Anything, book.Media.Metadata.ISBN).Return((*models.HardcoverBook)(nil), nil).Once()
	hc.On("SearchBookByISBN10", mock.Anything, book.Media.Metadata.ISBN).Return((*models.HardcoverBook)(nil), nil).Once()
	hc.On("SearchBooks", mock.Anything, "Second Lookup Not Found Author", "").Return([]models.HardcoverBook{}, nil).Once()
	// AddWithMetadata enriches the run-local mismatch export after the not-found
	// outcome has been published.
	hc.On("SearchBookByISBN13", mock.Anything, book.Media.Metadata.ISBN).Return(&models.HardcoverBook{
		ID: "904", Title: "Hardcover Second Lookup Not Found", Authors: []models.Author{{Name: "Hardcover Author"}},
	}, nil).Once()

	require.ErrorIs(t, svc.processBook(context.Background(), *absBook, &models.AudiobookshelfUserProgress{}), ErrSkippedBook)

	snapshot := svc.GetSnapshot()
	require.Len(t, snapshot.BookOutcomes, 1)
	assert.Equal(t, absBook.ID, snapshot.BookOutcomes[0].BookID)
	assert.Equal(t, OutcomeNotFound, snapshot.BookOutcomes[0].Outcome)

	records := svc.mismatchCollector.GetAll()
	require.Len(t, records, 1)
	assert.Equal(t, absBook.ID, records[0].BookID)
	hc.AssertExpectations(t)
}

func TestProcessBookSnapshotKeepsEnrichedNoEditionMismatch(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	book := createTestBook("snapshot-no-edition", "No Edition", "Author", "", "9781234567890")
	book.Progress.CurrentTime = 300
	absBook := toAudiobookshelfBook(book)
	absBook.Media.Metadata.PublishedYear = "2023"

	hc.On("SearchBookByISBN13", mock.Anything, book.Media.Metadata.ISBN).Return(&models.HardcoverBook{
		ID: "905",
	}, nil).Twice()
	hc.On("GetEdition", mock.Anything, "905").Return((*models.Edition)(nil), nil)
	hc.On("SearchBookByISBN13", mock.Anything, book.Media.Metadata.ISBN).Return(&models.HardcoverBook{
		ID: "905", Title: "Hardcover No Edition", ReleaseDate: "2020-02-03",
		CoverImageURL: "https://example.test/no-edition-cover.jpg", Authors: []models.Author{{Name: "Hardcover Author"}},
	}, nil).Once()

	err := svc.processBook(context.Background(), *absBook, &models.AudiobookshelfUserProgress{})
	require.ErrorIs(t, err, ErrSkippedBook)

	snapshot := svc.GetSnapshot()
	require.Len(t, snapshot.BookOutcomes, 1)
	got := snapshot.BookOutcomes[0]
	assert.Equal(t, absBook.ID, got.BookID)
	assert.Equal(t, "Hardcover No Edition", got.HardcoverTitle)
	assert.Equal(t, "Hardcover Author", got.HardcoverAuthor)
	assert.Equal(t, "https://example.test/no-edition-cover.jpg", got.HardcoverCoverURL)
	assert.Equal(t, "2020", got.HardcoverPublishedYear)
	assert.Equal(t, OutcomeNeedsReview, snapshot.BookOutcomes[0].Outcome)
	records := svc.mismatchCollector.GetAll()
	require.Len(t, records, 1)
	assert.Equal(t, "905", records[0].BookID, "mismatch export keeps its established Hardcover identifier")
	assert.Equal(t, 1, records[0].Attempts)
	hc.AssertExpectations(t)
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
	assert.Equal(t, int32(2), svc.GetSnapshotStatus().BooksTotal)
	assert.Equal(t, int32(1), svc.outcomeCounts.Total())
	hc.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
}

func TestProcessLibrarySkipsMissingIDAndReconcilesOutcomes(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.ProcessUnreadBooks = false
	mockABS := new(MockAudiobookshelfClient)
	svc.audiobookshelf = mockABS
	unread := toAudiobookshelfBook(createTestBook("unread-item", "Unread", "Author", "", ""))
	missing := toAudiobookshelfBook(createTestBook("missing-item", "Missing", "Author", "", ""))
	missing.Progress.CurrentTime = 300
	missing.Media.Duration = 1000
	items := []models.AudiobookshelfBook{*unread, {LibraryID: "library"}, *missing}
	// A second fetch can observe more items than the early pre-count.
	svc.recordLibraryCandidateTotal("library", 2)
	mockABS.On("GetLibraryItems", mock.Anything, "library").Return(items, nil).Once()
	hc.On("SearchBooks", mock.Anything, "Missing Author", "").Return([]models.HardcoverBook{}, nil).Once()

	processed, err := svc.processLibrary(context.Background(), &audiobookshelf.AudiobookshelfLibrary{ID: "library", Name: "Library"}, 0, &models.AudiobookshelfUserProgress{})
	require.NoError(t, err)
	assert.Equal(t, 2, processed)
	assert.Equal(t, int32(3), svc.GetSnapshotStatus().BooksTotal)
	assert.Equal(t, int32(2), svc.outcomeCounts.Total())
	assert.Equal(t, int32(2), svc.outcomeCounts.Total())
	assert.Equal(t, int32(1), svc.outcomeCounts.Skipped)
	assert.Equal(t, int32(1), svc.outcomeCounts.NotFound)
	assert.NotContains(t, svc.outcomeRecords, "")
	hc.AssertExpectations(t)
}

func TestSyncTestBookLimitIgnoresUnattemptedLibraryPrecountError(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.ProcessUnreadBooks = false
	svc.config.Sync.TestBookLimit = 1
	svc.statePath = filepath.Join(t.TempDir(), "sync_state.json")
	svc.config.Paths.MismatchOutputDir = t.TempDir()

	book := toAudiobookshelfBook(createTestBook("limited-book", "Unread", "Author", "", ""))
	mockABS := new(MockAudiobookshelfClient)
	mockABS.On("GetUserProgress", mock.Anything).Return(&models.AudiobookshelfUserProgress{}, nil).Once()
	mockABS.On("GetLibraries", mock.Anything).Return([]audiobookshelf.AudiobookshelfLibrary{
		{ID: "library-a", Name: "Library A"},
		{ID: "library-b", Name: "Library B"},
	}, nil).Once()
	mockABS.On("GetLibraryItems", mock.Anything, "library-a").Return([]models.AudiobookshelfBook{*book}, nil)
	mockABS.On("GetLibraryItems", mock.Anything, "library-b").Return(nil, errors.New("library B unavailable")).Once()
	hc.On("ClearUserBookCache").Return().Once()
	svc.audiobookshelf = mockABS

	require.NoError(t, svc.Sync(context.Background()))
	assert.Equal(t, int32(1), svc.outcomeCounts.Total())
	assert.Equal(t, int32(0), svc.outcomeCounts.Synced)
	mockABS.AssertExpectations(t)
	hc.AssertExpectations(t)
}

func TestSyncTestBookLimitCountsFailedBookAttempt(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.ProcessUnreadBooks = true
	svc.config.Sync.SyncOwned = false
	svc.config.Sync.TestBookLimit = 1
	svc.config.Sync.StateFile = filepath.Join(t.TempDir(), "sync_state.json")
	svc.statePath = svc.config.Sync.StateFile
	svc.config.Paths.MismatchOutputDir = t.TempDir()
	cacheDir := t.TempDir()
	svc.persistentCache = NewPersistentASINCache(cacheDir)
	require.NoError(t, svc.persistentCache.Load())
	svc.userBookCache = NewPersistentUserBookCache(cacheDir)
	require.NoError(t, svc.userBookCache.Load())

	failedBook := toAudiobookshelfBook(createTestBook("limit-failed", "Failed Book", "Author", "limit-failed-asin", ""))
	failedBook.Progress.CurrentTime = 300
	secondBook := toAudiobookshelfBook(createTestBook("limit-second", "Second Book", "Author", "limit-second-asin", ""))
	secondBook.Progress.CurrentTime = 300

	mockABS := new(MockAudiobookshelfClient)
	mockABS.On("GetUserProgress", mock.Anything).Return(&models.AudiobookshelfUserProgress{}, nil).Once()
	mockABS.On("GetLibraries", mock.Anything).Return([]audiobookshelf.AudiobookshelfLibrary{
		{ID: "library-a", Name: "Library A"},
		{ID: "library-b", Name: "Library B"},
	}, nil).Once()
	// The first library is pre-counted and then processed. The second library
	// is only pre-counted because the failed first attempt consumes the limit.
	mockABS.On("GetLibraryItems", mock.Anything, "library-a").Return([]models.AudiobookshelfBook{*failedBook}, nil).Once()
	mockABS.On("GetLibraryItems", mock.Anything, "library-b").Return([]models.AudiobookshelfBook{*secondBook}, nil).Once()
	hc.On("ClearUserBookCache").Return().Once()
	hc.On("SearchBookByASIN", mock.Anything, "limit-failed-asin").Return(&models.HardcoverBook{
		ID: "101", EditionID: "not-a-number",
	}, nil).Once()
	svc.audiobookshelf = mockABS

	err := svc.Sync(context.Background())

	require.NoError(t, err)
	assert.Equal(t, int32(2), svc.GetSnapshotStatus().BooksTotal)
	assert.Equal(t, int32(1), svc.outcomeCounts.Total())
	assert.Equal(t, int32(1), svc.outcomeCounts.Failed)
	mockABS.AssertExpectations(t)
	hc.AssertExpectations(t)
}

func TestSyncRetriesLibraryFetchAfterPrecountFailure(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.ProcessUnreadBooks = false
	svc.statePath = filepath.Join(t.TempDir(), "sync_state.json")
	svc.config.Paths.MismatchOutputDir = t.TempDir()

	book := toAudiobookshelfBook(createTestBook("retried-library-book", "Unread Book", "Author", "", ""))
	precountErr := errors.New("temporary library failure")
	mockABS := new(MockAudiobookshelfClient)
	mockABS.On("GetUserProgress", mock.Anything).Return(&models.AudiobookshelfUserProgress{}, nil)
	mockABS.On("GetLibraries", mock.Anything).Return([]audiobookshelf.AudiobookshelfLibrary{
		{ID: "library", Name: "Library"},
	}, nil)
	mockABS.On("GetLibraryItems", mock.Anything, "library").Return(nil, precountErr).Once()
	mockABS.On("GetLibraryItems", mock.Anything, "library").Return([]models.AudiobookshelfBook{*book}, nil).Once()
	hc.On("ClearUserBookCache").Return()
	svc.audiobookshelf = mockABS

	require.NoError(t, svc.Sync(context.Background()))
	assert.Equal(t, int32(1), svc.GetSnapshotStatus().BooksTotal)
	assert.Equal(t, int32(1), svc.outcomeCounts.Total())
	mockABS.AssertExpectations(t)
	hc.AssertExpectations(t)
}

func TestSyncReportsRetriedLibraryFetchFailure(t *testing.T) {
	svc, hc := createTestService()
	svc.statePath = filepath.Join(t.TempDir(), "sync_state.json")
	svc.config.Paths.MismatchOutputDir = t.TempDir()

	precountErr := errors.New("temporary library failure")
	retryErr := errors.New("library still unavailable")
	mockABS := new(MockAudiobookshelfClient)
	mockABS.On("GetUserProgress", mock.Anything).Return(&models.AudiobookshelfUserProgress{}, nil)
	mockABS.On("GetLibraries", mock.Anything).Return([]audiobookshelf.AudiobookshelfLibrary{
		{ID: "library", Name: "Library"},
	}, nil)
	mockABS.On("GetLibraryItems", mock.Anything, "library").Return(nil, precountErr).Once()
	mockABS.On("GetLibraryItems", mock.Anything, "library").Return(nil, retryErr).Once()
	hc.On("ClearUserBookCache").Return()
	svc.audiobookshelf = mockABS

	err := svc.Sync(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, retryErr)
	mockABS.AssertExpectations(t)
	hc.AssertExpectations(t)
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
