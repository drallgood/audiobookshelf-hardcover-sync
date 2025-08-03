package sync

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// This test file tests the HandleFinishedBook function

// TestHandleFinishedBook tests the handleFinishedBook function
func TestHandleFinishedBook(t *testing.T) {
	// Initialize logger for testing
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	// Setup test cases
	testCases := []struct {
		name                    string
		userBookID              int // Changed from int64 to int to match interface expectation
		editionID               string
		book                    *TestAudiobookshelfBook
		readStatuses            []hardcover.UserBookRead
		expectUpdateCall        bool
		expectInsertCall        bool
		expectStatusUpdateCall  bool // Added for status update testing
		mockUpdateError         error
		mockInsertError         error
		mockGetReadsError       error
		mockStatusUpdateError   error // Added for status update errors
		expectedError           bool
		expectedErrorString     string
		mockUserBook            *models.HardcoverBook // Added to mock GetUserBook response
		mockGetUserBookError    error // Added for GetUserBook errors
	}{
		{
			name:                   "Update existing unfinished read",
			userBookID:             123,
			editionID:              "456",
			expectUpdateCall:       true,
			expectInsertCall:       false,
			expectStatusUpdateCall: true, // Should update status to FINISHED
			book:                   createTestFinishedBook("abs-book-1", "Test Book", "Test Author", "B123456789", "9781234567890"),
			readStatuses: []hardcover.UserBookRead{
				{
					ID:              int64(1),
					UserBookID:      int64(123),
					StartedAt:       stringPointer("2023-01-01"),
					FinishedAt:      nil,
					Progress:        50.0,
					ProgressSeconds: intPointer(1800),
				},
			},
			// Mock a book with status ID 2 (READING)
			mockUserBook: &models.HardcoverBook{
				ID:           "book-123",
				UserBookID:   "123",
				BookStatusID: 2, // READING status
			},
		},
		{
			name:                   "Already has finished read",
			userBookID:             456,
			editionID:              "789",
			expectUpdateCall:       false,
			expectInsertCall:       false,
			expectStatusUpdateCall: false, // Should not update status as it's already FINISHED
			book:                   createTestFinishedBook("abs-book-2", "Another Book", "Another Author", "B987654321", "9789876543210"),
			readStatuses: []hardcover.UserBookRead{
				{
					ID:              int64(2),
					UserBookID:      int64(456),
					StartedAt:       stringPointer("2023-01-01"),
					FinishedAt:      stringPointer("2023-01-02"),
					Progress:        100.0,
					ProgressSeconds: intPointer(7200),
				},
			},
			// Mock a book with status ID 3 (FINISHED/READ)
			mockUserBook: &models.HardcoverBook{
				ID:           "book-456",
				UserBookID:   "456",
				BookStatusID: 3, // FINISHED/READ status
			},
		},
		{
			name:                   "No read statuses, create new read",
			userBookID:             789,
			editionID:              "012",
			expectUpdateCall:       false,
			expectInsertCall:       true,
			expectStatusUpdateCall: true, // Should update status to FINISHED
			book:                   createTestFinishedBook("abs-book-3", "New Book", "New Author", "B111222333", "9781112223330"),
			readStatuses:           []hardcover.UserBookRead{},
			// Mock a book with status ID 1 (WANT_TO_READ)
			mockUserBook: &models.HardcoverBook{
				ID:           "book-789",
				UserBookID:   "789",
				BookStatusID: 1, // WANT_TO_READ status
			},
		},
		{
			name:                   "Error getting read statuses",
			userBookID:             999,
			editionID:              "999",
			expectUpdateCall:       false,
			expectInsertCall:       false,
			expectStatusUpdateCall: true, // Should try to update status
			book:                   createTestFinishedBook("abs-book-4", "Error Book", "Error Author", "B999999999", "9789999999990"),
			readStatuses:           []hardcover.UserBookRead{},
			mockGetReadsError:      fmt.Errorf("API error"),
			expectedError:          true,
			expectedErrorString:    "error getting read statuses",
			// Mock a book with status ID 2 (READING)
			mockUserBook: &models.HardcoverBook{
				ID:           "book-999",
				UserBookID:   "999",
				BookStatusID: 2, // READING status
			},
		},
		{
			name:                   "Error updating read status",
			userBookID:             123,
			editionID:              "456",
			book:                   createTestFinishedBook("abs-book-5", "Update Error Book", "Update Error Author", "B555555555", "9785555555550"),
			readStatuses: []hardcover.UserBookRead{
				{
					ID:              int64(3),
					UserBookID:      int64(123),
					StartedAt:       stringPointer("2025-06-01"),
					FinishedAt:      nil,
					Progress:        50.0,
					ProgressSeconds: intPointer(1800),
				},
			},
			expectUpdateCall:       true,
			expectInsertCall:       false,
			expectStatusUpdateCall: true, // Should update status to FINISHED
			mockUpdateError:        fmt.Errorf("API error"),
			expectedError:          true,
			expectedErrorString:    "error updating read status",
			// Mock a book with status ID 2 (READING)
			mockUserBook: &models.HardcoverBook{
				ID:           "book-123-error",
				UserBookID:   "123",
				BookStatusID: 2, // READING status
			},
		},
		{
			name:                   "Error inserting read status",
			userBookID:             123,
			editionID:              "456",
			book:                   createTestFinishedBook("abs-book-6", "Insert Error Book", "Insert Error Author", "B666666666", "9786666666660"),
			readStatuses:           []hardcover.UserBookRead{},
			expectUpdateCall:       false,
			expectInsertCall:       true,
			expectStatusUpdateCall: true, // Should update status to FINISHED
			mockInsertError:        fmt.Errorf("API error"),
			expectedError:          true,
			expectedErrorString:    "error creating new read record",
			// Mock a book with status ID 2 (READING)
			mockUserBook: &models.HardcoverBook{
				ID:           "book-123-insert-error",
				UserBookID:   "123",
				BookStatusID: 2, // READING status
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a test service with mock client
			svc, mockClient := createTestService()
			
			// Create a test configuration
			cfg := createTestConfigForTests(true)
			svc.config = cfg
			
			// Set up mock expectations
			ctx := context.Background()
			
			// Mock GetUserBook to return the book with the appropriate status
			userBookIDStr := strconv.FormatInt(int64(tc.userBookID), 10)
			mockClient.On("GetUserBook", mock.Anything, userBookIDStr).Return(tc.mockUserBook, tc.mockGetUserBookError)
			
			// Mock GetUserBookReads
			mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
				UserBookID: int64(tc.userBookID),
			}).Return(tc.readStatuses, tc.mockGetReadsError)

			// Set up insert expectation if needed
			if tc.expectInsertCall {
				mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
					// Verify the user book ID matches - convert int to int64 for comparison
					return input.UserBookID == int64(tc.userBookID)
				})).Return(0, tc.mockInsertError)
			}

			// Set up update expectation if needed
			if tc.expectUpdateCall {
				mockClient.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
					// Verify the ID matches the read status we expect to update
					return input.ID == tc.readStatuses[0].ID
				})).Return(false, tc.mockUpdateError)
			}
			
			// Set up status update expectation if needed
			if tc.expectStatusUpdateCall {
				mockClient.On("UpdateUserBookStatus", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookStatusInput) bool {
					// Verify the ID matches and status is FINISHED
					return input.ID == int64(tc.userBookID) && input.Status == "FINISHED"
				})).Return(tc.mockStatusUpdateError)
			}

			// Convert test book to models.AudiobookshelfBook
			modelBook := convertTestBookToModel(tc.book)
			
			// Call the function under test
			err := svc.HandleFinishedBook(ctx, modelBook, tc.editionID, int64(tc.userBookID))

			// Verify expectations
			mockClient.AssertExpectations(t)

			// Assert the result
			if tc.expectedError {
				assert.Error(t, err, "Expected an error")
				assert.Contains(t, err.Error(), tc.expectedErrorString, "Error message should contain expected string")
			} else {
				assert.NoError(t, err, "Should not return an error")
			}
		})
	}
}

// Helper functions for test data
func stringPointer(s string) *string {
	return &s
}

func intPointer(i int) *int {
	return &i
}

// Helper function to convert TestAudiobookshelfBook to models.AudiobookshelfBook
func convertTestBookToModel(testBook *TestAudiobookshelfBook) models.AudiobookshelfBook {
	book := models.AudiobookshelfBook{
		ID:        testBook.ID,
		LibraryID: testBook.LibraryID,
		Path:      testBook.Path,
		MediaType: testBook.MediaType,
	}
	
	// Set the media fields
	book.Media.ID = testBook.Media.ID
	book.Media.Metadata = models.AudiobookshelfMetadataStruct{
		Title:      testBook.Media.Metadata.Title,
		AuthorName: testBook.Media.Metadata.AuthorName,
		ASIN:       testBook.Media.Metadata.ASIN,
		ISBN:       testBook.Media.Metadata.ISBN,
	}
	book.Media.Duration = testBook.Media.Duration
	
	// Set the progress fields
	book.Progress.IsFinished = testBook.Progress.IsFinished
	book.Progress.FinishedAt = testBook.Progress.FinishedAt
	book.Progress.CurrentTime = testBook.Progress.CurrentTime
	
	return book
}

// createTestConfigForTests creates a test configuration with the specified sync ownership setting
// and other default test values.
func createTestConfigForTests(syncOwned bool) *config.Config {
	// Start with default config
	cfg := config.DefaultConfig()
	
	// Configure sync settings - all sync-related settings are now consolidated under Sync
	cfg.Sync.Incremental = false
	cfg.Sync.StateFile = "/tmp/sync_state_test.json"
	cfg.Sync.MinChangeThreshold = 60 // 60 seconds
	cfg.Sync.SyncInterval = 1 * time.Hour
	cfg.Sync.MinimumProgress = 0.01
	cfg.Sync.SyncWantToRead = true
	cfg.Sync.SyncOwned = syncOwned
	cfg.Sync.DryRun = false
	
	// Initialize libraries include/exclude
	cfg.Sync.Libraries.Include = []string{}
	cfg.Sync.Libraries.Exclude = []string{}
	
	// Other configuration
	cfg.RateLimit.Rate = 100 * time.Millisecond
	cfg.RateLimit.Burst = 10
	cfg.RateLimit.MaxConcurrent = 5
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "console"
	cfg.Server.Port = "8080"
	cfg.Server.ShutdownTimeout = 30 * time.Second
	
	return cfg
}






