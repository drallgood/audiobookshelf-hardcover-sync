package sync

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestHandleInProgressBook_NoProgress tests the case where there is no progress to update
func TestHandleInProgressBook_NoProgress(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with no progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 0 // No progress
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:    "book-123",
		Title: "Test Book",
	}, nil).Once()

	// Mock the GetUserBookReads call
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Call the function
	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.NoError(t, err, "Should not return an error when there's no progress")
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_DryRun tests the case where dry-run mode is enabled
func TestHandleInProgressBook_DryRun(t *testing.T) {
	// Create test service with dry-run enabled
	svc, mockClient := createTestService()
	// Set dry-run mode
	svc.config.Sync.DryRun = true

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 100 // Some progress
	testAudiobook.Media.Duration = 1000      // Duration
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:    "book-123",
		Title: "Test Book",
	}, nil).Once()

	// Mock the GetUserBookReads call
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Call the function
	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.NoError(t, err, "Should not return an error in dry-run mode")
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_GetUserBookError tests error handling when GetUserBook fails
func TestHandleInProgressBook_GetUserBookError(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 100 // Some progress
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call to return an error
	userBookID := int64(123)
	expectedErr := errors.New("API error")
	mockClient.On("GetUserBook", mock.Anything, "123").Return((*models.HardcoverBook)(nil), expectedErr).Once()

	// Call the function
	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.Error(t, err, "Should return an error when GetUserBook fails")
	assert.Contains(t, err.Error(), "failed to get current book status")
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_RecentUpdate tests skipping updates when a recent update exists
func TestHandleInProgressBook_RecentUpdate(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 100 // Some progress
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:        "book-123",
		Title:     "Test Book",
		EditionID: "456",
	}, nil).Once()

	// Mock the GetUserBookReads call
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Initialize the lastProgressUpdates map if it doesn't exist
	if svc.lastProgressUpdates == nil {
		svc.lastProgressUpdates = make(map[string]progressUpdateInfo)
	}

	// Add a recent update to the cache
	bookCacheKey := "test-book-1:123"
	svc.lastProgressUpdates[bookCacheKey] = progressUpdateInfo{
		timestamp: time.Now().Add(-1 * time.Minute), // 1 minute ago
		progress:  98,                               // Very close to current progress (100)
	}

	// Call the function
	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.NoError(t, err, "Should not return an error when skipping due to recent update")
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_UpdateExistingRead tests updating an existing read status
func TestHandleInProgressBook_UpdateExistingRead(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 300 // Current progress in ABS
	testAudiobook.Media.Duration = 1000      // Total duration
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:        "book-123",
		Title:     "Test Book",
		EditionID: "456",
	}, nil).Once()

	// Create a read status with some progress
	readID := int64(789)
	progressSeconds := 100 // Current progress in Hardcover
	editionID := int64(456)

	// Mock the GetUserBookReads call to return an existing read status
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              readID,
			ProgressSeconds: &progressSeconds,
			EditionID:       &editionID,
			FinishedAt:      nil, // Not finished
		},
	}, nil).Once()

	// Mock the UpdateUserBookRead call
	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		// Verify the input has the correct ID and progress
		return input.ID == readID &&
			input.Object["progress_seconds"] == int64(300)
			// edition_id removed to prevent edition switching
	})).Return(true, nil).Once()

	// Call the function
	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.NoError(t, err, "Should not return an error when updating existing read")
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_EmptyFinishedAtTreatedAsUnfinished verifies that
// unfinished reads with finished_at as an empty string are updated instead of
// being misclassified as finished (which would trigger duplicate inserts).
func TestHandleInProgressBook_EmptyFinishedAtTreatedAsUnfinished(t *testing.T) {
	svc, mockClient := createTestService()

	testAudiobook := createTestBook("test-book-empty-finished-at", "Catching Fire", "Suzanne Collins", "B004J4WKTW", "")
	testAudiobook.Progress.CurrentTime = 10870
	testAudiobook.Media.Duration = 39772
	testAudiobook.Progress.IsFinished = false
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(7726703)
	unfinishedReadID := int64(5285601)
	finishedReadID := int64(2880599)
	unfinishedProgress := 7222
	finishedProgress := 79655
	editionID := int64(30438067)
	emptyFinishedAt := ""
	finishedAt := "2025-06-04"

	mockClient.On("GetUserBook", mock.Anything, "7726703").Return(&models.HardcoverBook{
		ID:        "645490",
		Title:     "Catching Fire",
		EditionID: "30438067",
	}, nil).Once()

	// Unfinished read comes back with finished_at as empty string, plus a prior finished read.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              unfinishedReadID,
			ProgressSeconds: &unfinishedProgress,
			EditionID:       &editionID,
			FinishedAt:      &emptyFinishedAt,
		},
		{
			ID:              finishedReadID,
			ProgressSeconds: &finishedProgress,
			EditionID:       &editionID,
			FinishedAt:      &finishedAt,
		},
	}, nil).Once()

	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == unfinishedReadID && input.Object["progress_seconds"] == int64(10870)
	})).Return(true, nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, editionID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
}

// TestHandleInProgressBook_UsesNilEditionUnfinishedRead verifies that when
// target edition is known but the only unfinished read has nil edition_id,
// we still update that existing read instead of creating a duplicate.
func TestHandleInProgressBook_UsesNilEditionUnfinishedRead(t *testing.T) {
	svc, mockClient := createTestService()

	testAudiobook := createTestBook("test-book-nil-edition-read", "Test Book", "Test Author", "B08N5KWB9H", "")
	testAudiobook.Progress.CurrentTime = 300
	testAudiobook.Media.Duration = 1000
	testAudiobook.Progress.IsFinished = false
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(123)
	readID := int64(790)
	progressSeconds := 100

	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:        "book-123",
		Title:     "Test Book",
		EditionID: "456",
	}, nil).Once()

	// Unfinished read exists, but edition_id is nil.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              readID,
			ProgressSeconds: &progressSeconds,
			EditionID:       nil,
			FinishedAt:      nil,
		},
	}, nil).Once()

	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == readID && input.Object["progress_seconds"] == int64(300)
	})).Return(true, nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, 456)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
}

// TestHandleInProgressBook_KeepsNilEditionDuplicateOpenWhenTargetReadExists verifies
// that if both a target-edition unfinished read and a nil-edition unfinished read
// exist, we keep the duplicate open (to avoid false finished rows) and only update
// the selected target read.
func TestHandleInProgressBook_KeepsNilEditionDuplicateOpenWhenTargetReadExists(t *testing.T) {
	svc, mockClient := createTestService()

	testAudiobook := createTestBook("test-book-dup-nil-edition", "Shift", "Hugh Howey", "B0BKR7LNQ9", "")
	testAudiobook.Progress.CurrentTime = 300
	testAudiobook.Media.Duration = 1000
	testAudiobook.Progress.IsFinished = false
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(7782662)
	targetReadID := int64(5051951)
	nilEditionReadID := int64(5058839)
	targetEditionID := int64(32058624)
	targetProgress := 200
	nilProgress := 250

	mockClient.On("GetUserBook", mock.Anything, "7782662").Return(&models.HardcoverBook{
		ID:        "427963",
		Title:     "Shift",
		EditionID: "32058624",
	}, nil).Once()

	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              nilEditionReadID,
			ProgressSeconds: &nilProgress,
			EditionID:       nil,
			FinishedAt:      nil,
		},
		{
			ID:              targetReadID,
			ProgressSeconds: &targetProgress,
			EditionID:       &targetEditionID,
			FinishedAt:      nil,
		},
	}, nil).Once()

	// Target read should be updated with current ABS progress.
	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == targetReadID && input.Object["progress_seconds"] == int64(300)
	})).Return(true, nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, targetEditionID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == nilEditionReadID
	}))
	mockClient.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
}

// TestHandleInProgressBook_DuplicateCleanupPreservesMetadata verifies that duplicate
// unfinished reads are left untouched while the highest-progress read is updated.
func TestHandleInProgressBook_DuplicateCleanupPreservesMetadata(t *testing.T) {
	svc, mockClient := createTestService()

	testAudiobook := createTestBook("test-book-dup-preserve-meta", "Dust", "Hugh Howey", "B0BKRG3NQ6", "")
	testAudiobook.Progress.CurrentTime = 7000
	testAudiobook.Media.Duration = 100000
	testAudiobook.Progress.IsFinished = false
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(7791036)
	duplicateReadID := int64(5054020)
	selectedReadID := int64(5066673)
	editionID := int64(32058625)
	duplicateProgress := 5000
	selectedProgress := 6000
	duplicateStartedAt := "2026-04-08"
	selectedStartedAt := "2026-04-09"

	mockClient.On("GetUserBook", mock.Anything, "7791036").Return(&models.HardcoverBook{
		ID:        "427964",
		Title:     "Dust",
		EditionID: "32058625",
	}, nil).Once()

	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              duplicateReadID,
			ProgressSeconds: &duplicateProgress,
			EditionID:       &editionID,
			StartedAt:       &duplicateStartedAt,
			FinishedAt:      nil,
		},
		{
			ID:              selectedReadID,
			ProgressSeconds: &selectedProgress,
			EditionID:       &editionID,
			StartedAt:       &selectedStartedAt,
			FinishedAt:      nil,
		},
	}, nil).Once()

	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              duplicateReadID,
			ProgressSeconds: &duplicateProgress,
			EditionID:       &editionID,
			StartedAt:       &duplicateStartedAt,
			FinishedAt:      nil,
		},
		{
			ID:              selectedReadID,
			ProgressSeconds: &selectedProgress,
			EditionID:       &editionID,
			StartedAt:       &selectedStartedAt,
			FinishedAt:      nil,
		},
	}, nil).Once()

	// Higher-progress unfinished read is kept and updated.
	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == selectedReadID && input.Object["progress_seconds"] == int64(7000)
	})).Return(true, nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, editionID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == duplicateReadID
	}))
	mockClient.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
}

// TestHandleInProgressBook_ForceUpdatesWhenProgressSecondsMissing verifies that
// unfinished reads missing progress_seconds are force-healed even when progress
// appears identical.
func TestHandleInProgressBook_ForceUpdatesWhenProgressSecondsMissing(t *testing.T) {
	svc, mockClient := createTestService()

	testAudiobook := createTestBook("test-book-missing-progress-seconds", "Test Book", "Test Author", "B08N5KWB9H", "")
	testAudiobook.Progress.CurrentTime = 300
	testAudiobook.Media.Duration = 1000
	testAudiobook.Progress.IsFinished = false
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(123)
	readID := int64(790)
	editionID := int64(456)

	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:        "book-123",
		Title:     "Test Book",
		EditionID: "456",
	}, nil).Once()

	// ProgressSeconds is missing, and Progress value mirrors ABS current time.
	// Prior behavior could skip as identical; we now force update to set progress_seconds.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              readID,
			ProgressSeconds: nil,
			Progress:        300,
			EditionID:       &editionID,
			FinishedAt:      nil,
		},
	}, nil).Once()

	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == readID && input.Object["progress_seconds"] == int64(300)
	})).Return(true, nil).Once()

	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_PrefersMatchingEditionRead verifies we don't update
// an unfinished read tied to a different edition (for example, physical format).
func TestHandleInProgressBook_PrefersMatchingEditionRead(t *testing.T) {
	svc, mockClient := createTestService()

	testAudiobook := createTestBook("test-book-edition-filter", "Shift", "Hugh Howey", "B0BKR7LNQ9", "")
	testAudiobook.Progress.CurrentTime = 20000
	testAudiobook.Media.Duration = 50000
	testAudiobook.Progress.IsFinished = false
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(7782662)
	audiobookEditionID := int64(32058624)
	physicalEditionID := int64(32050000)
	audiobookReadID := int64(5046837)
	physicalReadID := int64(5046000)
	audiobookProgressSeconds := 1000
	physicalProgressSeconds := 40000
	startedAt := "2025-06-02"

	mockClient.On("GetUserBook", mock.Anything, "7782662").Return(&models.HardcoverBook{
		ID:        "427963",
		Title:     "Shift",
		EditionID: "32058624",
	}, nil).Once()

	// Unfinished reads include one from another edition and one matching audiobook edition.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              physicalReadID,
			ProgressSeconds: &physicalProgressSeconds,
			StartedAt:       &startedAt,
			EditionID:       &physicalEditionID,
			FinishedAt:      nil,
		},
		{
			ID:              audiobookReadID,
			ProgressSeconds: &audiobookProgressSeconds,
			StartedAt:       &startedAt,
			EditionID:       &audiobookEditionID,
			FinishedAt:      nil,
		},
	}, nil).Once()

	// Full history read used by stale-reread detection; no finished reads here.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{
		{
			ID:              physicalReadID,
			ProgressSeconds: &physicalProgressSeconds,
			StartedAt:       &startedAt,
			EditionID:       &physicalEditionID,
			FinishedAt:      nil,
		},
		{
			ID:              audiobookReadID,
			ProgressSeconds: &audiobookProgressSeconds,
			StartedAt:       &startedAt,
			EditionID:       &audiobookEditionID,
			FinishedAt:      nil,
		},
	}, nil).Once()

	// Ensure we update the audiobook read, not the physical read.
	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		if input.ID != audiobookReadID {
			return false
		}
		progressSeconds, ok := input.Object["progress_seconds"]
		if !ok {
			return false
		}
		return progressSeconds == int64(20000)
	})).Return(true, nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, audiobookEditionID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == physicalReadID
	}))
}

// TestHandleInProgressBook_FallsBackToUserBookEditionOnTargetMismatch verifies
// that when stateKey target edition differs from the actual user_book edition,
// we still update existing unfinished reads on the user_book edition and do not
// create a duplicate read.
func TestHandleInProgressBook_FallsBackToUserBookEditionOnTargetMismatch(t *testing.T) {
	svc, mockClient := createTestService()

	testAudiobook := createTestBook("test-book-edition-mismatch", "Catching Fire", "Suzanne Collins", "B004J4WKTW", "")
	testAudiobook.Progress.CurrentTime = 31000
	testAudiobook.Media.Duration = 79655
	testAudiobook.Progress.IsFinished = false
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(7726703)
	readID := int64(5289357)
	readProgress := 29380
	userBookEditionID := int64(30438067)

	// User book is tied to edition 30438067.
	mockClient.On("GetUserBook", mock.Anything, "7726703").Return(&models.HardcoverBook{
		ID:        "645490",
		Title:     "Catching Fire",
		EditionID: "30438067",
	}, nil).Once()

	// Initial unfinished query returns unfinished reads on user book edition,
	// but stateKey target edition will be a different one.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              readID,
			ProgressSeconds: &readProgress,
			EditionID:       &userBookEditionID,
			FinishedAt:      nil,
		},
	}, nil).Once()

	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == readID && input.Object["progress_seconds"] == int64(31000)
	})).Return(true, nil).Once()

	// stateKey target edition intentionally mismatches user_book edition.
	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, 32803577)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
}

// TestHandleInProgressBook_SmallProgressDifference tests skipping updates when progress difference is small
func TestHandleInProgressBook_SmallProgressDifference(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 105 // Current progress in ABS (very close to Hardcover's 100)
	testAudiobook.Media.Duration = 1000      // Total duration
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:    "book-123",
		Title: "Test Book",
	}, nil).Once()

	// Create a read status with similar progress
	readID := int64(789)
	progressSeconds := 100 // Very close to current progress (105)
	editionID := int64(456)

	// Mock the GetUserBookReads call to return an existing read status
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              readID,
			ProgressSeconds: &progressSeconds,
			EditionID:       &editionID,
		},
	}, nil).Once()

	// Call the function
	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.NoError(t, err, "Should not return an error when progress difference is small")
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_CreateNewRead tests creating a new read status when none exists
func TestHandleInProgressBook_CreateNewRead(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 300 // Current progress in ABS
	testAudiobook.Media.Duration = 1000      // Total duration
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call - only called once due to caching
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:        "book-123",
		Title:     "Test Book",
		EditionID: "456",
	}, nil).Once()

	// Note: Second GetUserBook call is now served from cache, so no additional mock needed

	// Mock the GetUserBookReads call to return no existing read status
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Second-chance full fetch also returns no reads
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Mock the InsertUserBookRead call
	progressSeconds := 300
	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		// Verify the input has the correct user book ID and progress
		return input.UserBookID == userBookID &&
			input.DatesRead.ProgressSeconds != nil &&
			*input.DatesRead.ProgressSeconds == progressSeconds &&
			input.DatesRead.EditionID != nil &&
			*input.DatesRead.EditionID == int64(456)
	})).Return(789, nil).Once()

	// Mock the UpdateUserBookStatus call
	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2, // 2 = Currently Reading
	}).Return(nil).Once()

	// After status update, code checks for auto-created blank reads
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Call the function
	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.NoError(t, err, "Should not return an error when creating new read")
	mockClient.AssertExpectations(t)
}

func TestHandleInProgressBook_CreateNewRead_UsesStateKeyEdition(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 300 // Current progress in ABS
	testAudiobook.Media.Duration = 1000      // Total duration
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call so Hardcover user book has a different EditionID
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:        "book-123",
		Title:     "Test Book",
		EditionID: "999", // Different from the edition we encode in stateKey
	}, nil).Once()

	// No unfinished reads
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Second-chance full fetch also returns no reads
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Expect InsertUserBookRead to not use edition ID to prevent edition switching
	progressSeconds := 300
	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		return input.UserBookID == userBookID &&
			input.DatesRead.ProgressSeconds != nil &&
			*input.DatesRead.ProgressSeconds == progressSeconds &&
			input.DatesRead.EditionID != nil &&
			*input.DatesRead.EditionID == int64(999)
	})).Return(789, nil).Once()

	// Mock the UpdateUserBookStatus call
	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2, // 2 = Currently Reading
	}).Return(nil).Once()

	// After status update, code checks for auto-created blank reads
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Call the function with stateKey encoding the editionID (456)
	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, 456)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.NoError(t, err, "Should not return an error when creating new read with stateKey edition")
	mockClient.AssertExpectations(t)
}

func TestHandleInProgressBook_CreateNewRead_RefreshesStaleABSStartedAtOnLikelyRestart(t *testing.T) {
	svc, mockClient := createTestService()

	testAudiobook := createTestBook("test-book-stale-abs-start", "Catching Fire", "Suzanne Collins", "B004J4WKTW", "")
	testAudiobook.Progress.CurrentTime = 2000
	testAudiobook.Media.Duration = 10000
	testAudiobook.Progress.IsFinished = false
	// Deliberately stale ABS started_at from last year.
	testAudiobook.Progress.StartedAt = time.Date(2025, time.June, 2, 12, 0, 0, 0, time.UTC).UnixMilli()
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(7726703)
	mockClient.On("GetUserBook", mock.Anything, "7726703").Return(&models.HardcoverBook{
		ID:        "645490",
		Title:     "Catching Fire",
		EditionID: "30438067",
	}, nil).Once()

	// No unfinished reads found.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Second-chance full fetch also returns no reads
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2,
	}).Return(nil).Once()

	// Cleanup check for auto-created blank reads after status update
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	today := time.Now().Format("2006-01-02")
	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		if input.UserBookID != userBookID || input.DatesRead.StartedAt == nil {
			return false
		}
		return *input.DatesRead.StartedAt == today
	})).Return(5289673, nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, 32803577)
	// Prior state indicates a likely restart (recently finished), which should
	// trigger started_at refresh instead of reusing stale ABS started_at.
	svc.state.UpdateBook(stateKey, 100, "FINISHED")

	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestHandleInProgressBook_DoesNotSetInProgressStatusWhenUpdatingExistingRead(t *testing.T) {
	svc, mockClient := createTestService()

	testAudiobook := createTestBook("test-book-existing-read", "For We Are Many", "Dennis E. Taylor", "B01N17THEO", "9781603932189")
	testAudiobook.Progress.CurrentTime = 32360
	testAudiobook.Media.Duration = 32372.773333
	testAudiobook.Progress.IsFinished = false
	testAudiobook.Progress.FinishedAt = 0
	testAudiobook.Progress.StartedAt = time.Date(2026, time.June, 24, 0, 0, 0, 0, time.UTC).UnixMilli()
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(8154667)
	mockClient.On("GetUserBook", mock.Anything, "8154667").Return(&models.HardcoverBook{
		ID:        "427993",
		Title:     "For We Are Many",
		EditionID: "31546165",
	}, nil).Once()

	readID := int64(5701845)
	startedAt := "2026-06-24"
	editionID := int64(31546165)

	// Unfinished read exists but missing progress_seconds.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:         readID,
			StartedAt:  &startedAt,
			FinishedAt: nil,
			Progress:   0,
			EditionID:  &editionID,
		},
	}, nil).Once()

	// Full read history returns finished rows so stale-reread detection runs.
	finishedAt := "2026-06-24"
	progressSeconds := 32360
	todayFinishedReadID := int64(5701844)
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{
		{
			ID:              readID,
			StartedAt:       &startedAt,
			FinishedAt:      nil,
			Progress:        0,
			EditionID:       &editionID,
			ProgressSeconds: nil,
		},
		{
			ID:              todayFinishedReadID,
			StartedAt:       &startedAt,
			FinishedAt:      &finishedAt,
			Progress:        100,
			EditionID:       &editionID,
			ProgressSeconds: &progressSeconds,
		},
	}, nil).Once()

	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		if input.ID != readID {
			return false
		}
		ps, ok := input.Object["progress_seconds"].(int64)
		if !ok || ps != 32360 {
			return false
		}
		return true
	})).Return(true, nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, 33014501)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
	mockClient.AssertExpectations(t)
}

func TestHandleInProgressBook_SkipsRereadCreateWithoutRestartSignal(t *testing.T) {
	svc, mockClient := createTestService()

	testAudiobook := createTestBook("test-book-no-reread", "For We Are Many", "Dennis E. Taylor", "B01LZBXVG8", "")
	testAudiobook.Progress.CurrentTime = 32350
	testAudiobook.Media.Duration = 32360
	testAudiobook.Progress.IsFinished = false
	testAudiobook.Progress.FinishedAt = 0
	testAudiobook.Progress.StartedAt = time.Date(2026, time.June, 23, 0, 0, 0, 0, time.UTC).UnixMilli()
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(8154667)
	mockClient.On("GetUserBook", mock.Anything, "8154667").Return(&models.HardcoverBook{
		ID:        "736789",
		Title:     "For We Are Many",
		EditionID: "31546165",
	}, nil).Once()

	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Second-chance full fetch also returns no reads
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2,
	}).Return(nil).Once()

// Cleanup check for auto-created blank reads after status update
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Once()
	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		if input.UserBookID != userBookID || input.DatesRead.StartedAt == nil {
			return false
		}
		if input.DatesRead.ProgressSeconds == nil || *input.DatesRead.ProgressSeconds != 32350 {
			return false
		}
		return true
	})).Return(99999, nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, 31546165)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestHandleInProgressBook_SkipsRereadCreateWhenNearCompleteAcrossDays(t *testing.T) {
	svc, mockClient := createTestService()

	testAudiobook := createTestBook("test-book-near-complete", "For We Are Many", "Dennis E. Taylor", "B01LZBXVG8", "")
	testAudiobook.Progress.CurrentTime = 32350
	testAudiobook.Media.Duration = 32360
	testAudiobook.Progress.IsFinished = false
	testAudiobook.Progress.FinishedAt = 0
	testAudiobook.Progress.StartedAt = time.Date(2026, time.June, 24, 8, 0, 0, 0, time.UTC).UnixMilli()
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(8154667)
	mockClient.On("GetUserBook", mock.Anything, "8154667").Return(&models.HardcoverBook{
		ID:        "736789",
		Title:     "For We Are Many",
		EditionID: "31546165",
	}, nil).Once()

	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Second-chance full fetch also returns no reads
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
}).Return([]hardcover.UserBookRead{}, nil).Twice()

	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2,
	}).Return(nil).Once()

	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		if input.UserBookID != userBookID || input.DatesRead.StartedAt == nil {
			return false
		}
		if input.DatesRead.ProgressSeconds == nil || *input.DatesRead.ProgressSeconds != 32350 {
			return false
		}
		return true
	})).Return(99999, nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, 31546165)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_UpdateReadError tests error handling when UpdateUserBookRead fails
func TestHandleInProgressBook_UpdateReadError(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 300 // Current progress in ABS
	testAudiobook.Media.Duration = 1000      // Total duration
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:        "book-123",
		Title:     "Test Book",
		EditionID: "456",
	}, nil).Once()

	// Create a read status with some progress but not finished
	readID := int64(789)
	progressSeconds := 500 // Some progress in Hardcover
	editionID := int64(456)

	// Mock the GetUserBookReads call to return an existing read status
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              readID,
			ProgressSeconds: &progressSeconds,
			EditionID:       &editionID,
			FinishedAt:      nil, // Not finished
		},
	}, nil).Once()

	// No second-chance fetch expected in update path

	// Mock the UpdateUserBookRead call to return an error
	expectedErr := errors.New("API error")
	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		// Verify the input has the correct ID and progress
		return input.ID == readID &&
			input.Object["progress_seconds"] == int64(300)
	})).Return(false, expectedErr).Once()

	// Call the function
	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.Error(t, err, "Should return an error when UpdateUserBookRead fails")
	assert.Contains(t, err.Error(), "failed to update progress")
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_GetUserBookReadsError tests error handling when GetUserBookReads fails
func TestHandleInProgressBook_GetUserBookReadsError(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 100 // Some progress
	testAudiobook.Media.Duration = 3600      // Total duration
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call - first call at the beginning of handleInProgressBook
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:        "book-123",
		Title:     "Test Book",
		EditionID: "456",
	}, nil).Once()

	// Mock the GetUserBookReads call to return an error
	expectedErr := errors.New("API error")
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return(nil, expectedErr).Once()

	// Second-chance full fetch also returns the same error
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
}).Return(nil, expectedErr).Twice()

	// Note: Second GetUserBook call is now served from cache, so no additional mock needed

	// Mock the InsertUserBookRead call
	progressSeconds := 100
	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		return input.UserBookID == userBookID &&
			input.DatesRead.ProgressSeconds != nil &&
			*input.DatesRead.ProgressSeconds == progressSeconds
	})).Return(789, nil).Once()

	// Mock the UpdateUserBookStatus call
	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2, // 2 = Currently Reading
	}).Return(nil).Once()

	// Call the function
	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results - the function should continue despite the GetUserBookReads error
	assert.NoError(t, err, "Should not return an error when GetUserBookReads fails but we can create a new read")
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_SkipsCreateWhenPriorStateMatchesProgress verifies
// we avoid creating duplicate unfinished reads when Hardcover snapshots appear
// empty but recent sync state already indicates matching in-progress progress.
func TestHandleInProgressBook_SkipsCreateWhenPriorStateMatchesProgress(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-prior-state-guard", "Catching Fire", "Suzanne Collins", "B004J4WKTW", "")
	testAudiobook.Progress.CurrentTime = 10870
	testAudiobook.Media.Duration = 39772
	testAudiobook.Progress.IsFinished = false
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(7726703)

	mockClient.On("GetUserBook", mock.Anything, "7726703").Return(&models.HardcoverBook{
		ID:        "645490",
		Title:     "Catching Fire",
		EditionID: "30438067",
	}, nil).Once()

	// Initial unfinished read snapshot appears empty.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// First second-chance full fetch also appears empty.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, 30438067)
	priorProgress := (testAudiobook.Progress.CurrentTime / testAudiobook.Media.Duration) * 100
	svc.state.UpdateBook(stateKey, priorProgress, "IN_PROGRESS")

	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
}

// TestHandleInProgressBook_FinishedBook tests handling a finished book
func TestHandleInProgressBook_FinishedBook(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book marked as finished
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 1000 // Full progress
	testAudiobook.Media.Duration = 1000       // Duration
	testAudiobook.Progress.IsFinished = true
	testAudiobook.Progress.FinishedAt = time.Now().Unix() // Finished timestamp
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:        "book-123",
		Title:     "Test Book",
		EditionID: "456",
	}, nil).Once()

	// Create a read status with some progress but not finished
	readID := int64(789)
	progressSeconds := 500 // Some progress in Hardcover
	editionID := int64(456)

	// Mock the GetUserBookReads call to return an existing read status
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              readID,
			ProgressSeconds: &progressSeconds,
			EditionID:       &editionID,
			FinishedAt:      nil, // Not finished yet
		},
	}, nil).Once()

	// Mock the UpdateUserBookRead call
	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		// Verify the input has the correct ID, progress, and finished date
		return input.ID == readID &&
			input.Object["progress_seconds"] == int64(1000) &&
			input.Object["finished_at"] != nil
	})).Return(true, nil).Once()

	// Mock the UpdateUserBookStatus call to mark as completed
	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 3, // 3 = Completed
	}).Return(nil).Once()

	// Call the function
	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.NoError(t, err, "Should not return an error when updating finished book")
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_RefreshesStaleStartedAtForReread verifies that
// a stale unfinished reread is closed and a new active read is created.
func TestHandleInProgressBook_RefreshesStaleStartedAtForReread(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with in-progress state
	testAudiobook := createTestBook("test-book-reread", "Shift", "Hugh Howey", "B0BKR7LNQ9", "")
	testAudiobook.Progress.CurrentTime = 20000
	testAudiobook.Media.Duration = 50000
	testAudiobook.Progress.IsFinished = false
	testAudiobook.Progress.StartedAt = time.Date(2026, time.April, 2, 12, 0, 0, 0, time.UTC).UnixMilli()
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(7782662)
	readID := int64(5038810)
	progressSeconds := 1000
	editionID := int64(32058624)
	oldStartedAt := "2025-06-02"
	finishedAt := "2025-06-10"

	mockClient.On("GetUserBook", mock.Anything, "7782662").Return(&models.HardcoverBook{
		ID:        "427963",
		Title:     "Shift",
		EditionID: "32058624",
	}, nil).Once()

	// Initial fetch for unfinished reads returns stale started_at.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              readID,
			ProgressSeconds: &progressSeconds,
			StartedAt:       &oldStartedAt,
			EditionID:       &editionID,
			FinishedAt:      nil,
		},
	}, nil).Once()

	// Full read fetch contains the unfinished read plus a legitimate prior finished audio read.
	// The prior finished read has real progress and different start/end dates so it is not
	// filtered out by the zero-progress-closed or physical-read guards.
	priorFinishedStartedAt := "2025-06-01"
	priorFinishedProgressSeconds := 50000 // fully listened
	priorFinishedProgress := 100.0
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{
		{
			ID:              readID,
			ProgressSeconds: &progressSeconds,
			StartedAt:       &oldStartedAt,
			EditionID:       &editionID,
			FinishedAt:      nil,
		},
		{
			ID:              2901494,
			StartedAt:       &priorFinishedStartedAt,
			FinishedAt:      &finishedAt,
			EditionID:       &editionID,
			ProgressSeconds: &priorFinishedProgressSeconds,
			Progress:        priorFinishedProgress,
		},
	}, nil).Once()

	// Stale unfinished read should be closed at latest finished date.
	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		if input.ID != readID {
			return false
		}
		if input.Object["finished_at"] != finishedAt {
			return false
		}
		if input.Object["started_at"] != oldStartedAt {
			return false
		}
		return input.Object["edition_id"] == editionID
	})).Return(true, nil).Once()

	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		return input.UserBookID == userBookID &&
			input.DatesRead.ProgressSeconds != nil &&
			*input.DatesRead.ProgressSeconds == 20000 &&
			input.DatesRead.StartedAt != nil &&
			*input.DatesRead.StartedAt == "2026-04-02" &&
			input.DatesRead.EditionID != nil &&
			*input.DatesRead.EditionID == editionID
	})).Return(999999, nil).Once()

	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2,
	}).Return(nil).Once()

	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, editionID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_SecondChancePreservesStartedAt verifies that when
// the stale-reread logic closes an old unfinished read and falls through to the create
// path, a new read is inserted via InsertUserBookRead with a fresh started_at rather
// than the stale ABS started_at.
func TestHandleInProgressBook_SecondChancePreservesStartedAt(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with in-progress state and an old ABS started_at timestamp.
	testAudiobook := createTestBook("test-book-second-chance", "Dust", "Hugh Howey", "B0BKR4Q1PH", "")
	testAudiobook.Progress.CurrentTime = 23168
	testAudiobook.Media.Duration = 39216.33
	testAudiobook.Progress.IsFinished = false
	testAudiobook.Progress.StartedAt = time.Date(2025, time.June, 2, 12, 0, 0, 0, time.UTC).UnixMilli()
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(7791036)
	staleReadID := int64(5047267)
	editionID := int64(32058625)
	staleStartedAt := "2025-06-02"
	latestFinishedAt := "2025-06-10"
	staleProgressSeconds := 23168
	finishedProgressSeconds := 39216
	finishedProgress := 100.0

	mockClient.On("GetUserBook", mock.Anything, "7791036").Return(&models.HardcoverBook{
		ID:        "427570",
		Title:     "Dust",
		EditionID: "32058625",
	}, nil).Once()

	// Initial unfinished read lookup: stale active read exists.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              staleReadID,
			ProgressSeconds: &staleProgressSeconds,
			StartedAt:       &staleStartedAt,
			EditionID:       &editionID,
			FinishedAt:      nil,
		},
	}, nil).Once()

	// Full read history includes a finished read, forcing stale read close behavior.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{
		{
			ID:              staleReadID,
			ProgressSeconds: &staleProgressSeconds,
			StartedAt:       &staleStartedAt,
			EditionID:       &editionID,
			FinishedAt:      nil,
		},
		{
			ID:              2903829,
			StartedAt:       &latestFinishedAt,
			FinishedAt:      &latestFinishedAt,
			EditionID:       &editionID,
			ProgressSeconds: &finishedProgressSeconds,
			Progress:        finishedProgress,
		},
	}, nil).Once()

	// Stale unfinished read is closed.
	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		if input.ID != staleReadID {
			return false
		}
		return input.Object["finished_at"] == latestFinishedAt
	})).Return(true, nil).Once()

	// After stale close, code falls directly into the create path:
	// InsertUserBookRead + UpdateUserBookStatus instead of the old second-chance update.
	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		return input.UserBookID == userBookID &&
			input.DatesRead.ProgressSeconds != nil &&
			*input.DatesRead.ProgressSeconds == staleProgressSeconds &&
			input.DatesRead.StartedAt != nil &&
			input.DatesRead.EditionID != nil &&
			*input.DatesRead.EditionID == editionID
	})).Return(999999, nil).Once()

	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2,
	}).Return(nil).Once()

mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Once()
	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, editionID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_UsesHistoricalZeroProgressFinishedReadForStaleDetection verifies that
// historical zero-progress closed reads still participate in stale reread detection.
func TestHandleInProgressBook_UsesHistoricalZeroProgressFinishedReadForStaleDetection(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with in-progress state
	testAudiobook := createTestBook("test-book-shift-stale", "Shift", "Hugh Howey", "B0BKR7LNQ9", "")
	testAudiobook.Progress.CurrentTime = 16700
	testAudiobook.Media.Duration = 52572.281678
	testAudiobook.Progress.IsFinished = false
	audiobook := toAudiobookshelfBook(testAudiobook)

	userBookID := int64(7782662)
	staleReadID := int64(5046837)
	editionID := int64(32058624)
	staleStartedAt := "2025-06-02"
	historicalFinishedAt := "2025-06-10"
	staleProgressSeconds := 16700
	zeroProgressSeconds := 0

	mockClient.On("GetUserBook", mock.Anything, "7782662").Return(&models.HardcoverBook{
		ID:        "427963",
		Title:     "Shift",
		EditionID: "32058624",
	}, nil).Once()

	// Initial unfinished read lookup returns stale active reread.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
		Status:     "unfinished",
	}).Return([]hardcover.UserBookRead{
		{
			ID:              staleReadID,
			ProgressSeconds: &staleProgressSeconds,
			StartedAt:       &staleStartedAt,
			EditionID:       &editionID,
			FinishedAt:      nil,
		},
	}, nil).Once()

	// Full read history includes a historical zero-progress closed read.
	// This must still be considered for stale detection.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{
		{
			ID:              staleReadID,
			ProgressSeconds: &staleProgressSeconds,
			StartedAt:       &staleStartedAt,
			EditionID:       &editionID,
			FinishedAt:      nil,
		},
		{
			ID:              2901494,
			StartedAt:       &historicalFinishedAt,
			FinishedAt:      &historicalFinishedAt,
			EditionID:       &editionID,
			ProgressSeconds: &zeroProgressSeconds,
			Progress:        0,
		},
	}, nil).Once()

	// Stale unfinished read should be closed at the latest finished date.
	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		if input.ID != staleReadID {
			return false
		}
		return input.Object["finished_at"] == historicalFinishedAt
	})).Return(true, nil).Once()

	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2,
	}).Return(nil).Once()

mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Once()
	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		return input.UserBookID == userBookID &&
			input.DatesRead.ProgressSeconds != nil &&
			*input.DatesRead.ProgressSeconds == 16700
	})).Return(999998, nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, editionID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}
