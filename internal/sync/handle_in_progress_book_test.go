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

	// Mock the UpdateUserBookStatus call
	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2, // 2 = Currently Reading
	}).Return(nil).Once()

	// Call the function
	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.NoError(t, err, "Should not return an error when updating existing read")
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

	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2,
	}).Return(nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, audiobookEditionID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == physicalReadID
	}))
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

	// Second-chance read fetch (no status filter) should also return no reads, allowing creation
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Twice()

	// Mock the InsertUserBookRead call
	progressSeconds := 300
	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		// Verify the input has the correct user book ID and progress
		return input.UserBookID == userBookID &&
			input.DatesRead.ProgressSeconds != nil &&
			*input.DatesRead.ProgressSeconds == progressSeconds &&
			input.DatesRead.EditionID == nil // EditionID removed to prevent edition switching
	})).Return(789, nil).Once()

	// Mock the UpdateUserBookStatus call
	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2, // 2 = Currently Reading
	}).Return(nil).Once()

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

	// Second-chance read fetches (no status filter) return no reads
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Twice()

	// Expect InsertUserBookRead to not use edition ID to prevent edition switching
	progressSeconds := 300
	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		return input.UserBookID == userBookID &&
			input.DatesRead.ProgressSeconds != nil &&
			*input.DatesRead.ProgressSeconds == progressSeconds &&
			input.DatesRead.EditionID == nil // EditionID removed to prevent edition switching
	})).Return(789, nil).Once()

	// Mock the UpdateUserBookStatus call
	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2, // 2 = Currently Reading
	}).Return(nil).Once()

	// Call the function with stateKey encoding the editionID (456)
	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, 456)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.NoError(t, err, "Should not return an error when creating new read with stateKey edition")
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

	// Note: Second GetUserBook call is now served from cache, so no additional mock needed

	// Second-chance read fetch (no status filter) is performed twice: once after initial failure and once pre-insert
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Twice()

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

	// Second full read fetch happens after stale close to discover unfinished entries before insert.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{
		{
			ID:              2901494,
			StartedAt:       &priorFinishedStartedAt,
			FinishedAt:      &finishedAt,
			EditionID:       &editionID,
			ProgressSeconds: &priorFinishedProgressSeconds,
			Progress:        priorFinishedProgress,
		},
	}, nil).Once()

	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		return input.UserBookID == userBookID &&
			input.DatesRead.ProgressSeconds != nil &&
			*input.DatesRead.ProgressSeconds == 20000
	})).Return(999999, nil).Once()

	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2,
	}).Return(nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, editionID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

// TestHandleInProgressBook_SecondChancePreservesStartedAt verifies that when the
// second-chance fetch returns an unfinished read (for example, auto-created by Hardcover),
// we preserve that read's started_at instead of overwriting it with stale ABS started_at.
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
	newReadID := int64(5048973)
	editionID := int64(32058625)
	staleStartedAt := "2025-06-02"
	latestFinishedAt := "2025-06-10"
	newStartedAt := "2026-04-08"
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

	// Status set to IN_PROGRESS before second-chance fetch.
	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2,
	}).Return(nil).Once()

	// Second-chance fetch finds the newly created unfinished read with a fresh start date.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{
		{
			ID:              newReadID,
			StartedAt:       &newStartedAt,
			FinishedAt:      nil,
			EditionID:       &editionID,
			ProgressSeconds: nil,
			Progress:        0,
		},
	}, nil).Once()

	// Verify second-chance update preserves new started_at instead of using stale ABS date.
	mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		if input.ID != newReadID {
			return false
		}
		startedAt, ok := input.Object["started_at"]
		if !ok {
			return false
		}
		return startedAt == newStartedAt
	})).Return(true, nil).Once()

	stateKey := fmt.Sprintf("%s:%d", audiobook.ID, editionID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockClient.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
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

	// After close, second-chance fetch finds no unfinished read, so insert path is used.
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{
		{
			ID:              2901494,
			StartedAt:       &historicalFinishedAt,
			FinishedAt:      &historicalFinishedAt,
			EditionID:       &editionID,
			ProgressSeconds: &zeroProgressSeconds,
			Progress:        0,
		},
	}, nil).Once()

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
