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
	svc.config.App.DryRun = true

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
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Initialize the lastProgressUpdates map if it doesn't exist
	if svc.lastProgressUpdates == nil {
		svc.lastProgressUpdates = make(map[string]progressUpdateInfo)
	}

	// Add a recent update to the cache
	bookCacheKey := "test-book-1:123"
	svc.lastProgressUpdates[bookCacheKey] = progressUpdateInfo{
		timestamp: time.Now().Add(-1 * time.Minute), // 1 minute ago
		progress:  98, // Very close to current progress (100)
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
	testAudiobook.Media.Duration = 1000     // Total duration
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
			   input.Object["progress_seconds"] == int64(300) && 
			   input.Object["edition_id"] == editionID
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

// TestHandleInProgressBook_SmallProgressDifference tests skipping updates when progress difference is small
func TestHandleInProgressBook_SmallProgressDifference(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 105 // Current progress in ABS (very close to Hardcover's 100)
	testAudiobook.Media.Duration = 1000     // Total duration
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
	testAudiobook.Media.Duration = 1000     // Total duration
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call - will be called twice
	// First call at the beginning of handleInProgressBook
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:        "book-123",
		Title:     "Test Book",
		EditionID: "456",
	}, nil).Once()

	// Second call when creating a new read status
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:        "book-123",
		Title:     "Test Book",
		EditionID: "456",
	}, nil).Once()

	// Mock the GetUserBookReads call to return no existing read status
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Once()

	// Mock the InsertUserBookRead call
	editionID := int64(456)
	progressSeconds := 300
	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		// Verify the input has the correct user book ID and progress
		return input.UserBookID == userBookID && 
			   input.DatesRead.ProgressSeconds != nil && 
			   *input.DatesRead.ProgressSeconds == progressSeconds && 
			   input.DatesRead.EditionID != nil && 
			   *input.DatesRead.EditionID == editionID
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

// TestHandleInProgressBook_UpdateReadError tests error handling when UpdateUserBookRead fails
func TestHandleInProgressBook_UpdateReadError(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 300 // Current progress in ABS
	testAudiobook.Media.Duration = 1000     // Total duration
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
	}).Return([]hardcover.UserBookRead{
		{
			ID:              readID,
			ProgressSeconds: &progressSeconds,
			EditionID:       &editionID,
			FinishedAt:      nil, // Not finished
		},
	}, nil).Once()

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
	testAudiobook.Media.Duration = 3600     // Total duration
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
	}).Return(nil, expectedErr).Once()

	// Mock the second GetUserBook call when creating a new read status
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:        "book-123",
		Title:     "Test Book",
		EditionID: "456",
	}, nil).Once()

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
