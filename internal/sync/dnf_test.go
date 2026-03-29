package sync

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestHandleInProgressBook_DNFStatus tests that books marked as DNF in Hardcover are not updated
func TestHandleInProgressBook_DNFStatus(t *testing.T) {
	// Create test service with DNF preservation enabled
	svc, mockClient := createTestService()
	svc.config.Sync.PreserveDNF = true

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 300 // Some progress
	testAudiobook.Media.Duration = 1000      // Duration
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call to return a book with DNF status
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:           "book-123",
		Title:        "Test Book",
		BookStatusID: 5, // DNF status
	}, nil).Once()

	// Call the function
	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.NoError(t, err, "Should not return an error when book is DNF")
	mockClient.AssertExpectations(t)
}

// TestHandleFinishedBook_DNFStatus tests that books marked as DNF in Hardcover are not updated
func TestHandleFinishedBook_DNFStatus(t *testing.T) {
	// Create test service with DNF preservation enabled
	svc, mockClient := createTestService()
	svc.config.Sync.PreserveDNF = true

	// Create a test finished book
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 1000 // Finished
	testAudiobook.Progress.IsFinished = true
	testAudiobook.Progress.FinishedAt = time.Now().Unix()
	testAudiobook.Media.Duration = 1000
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call to return a book with DNF status
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:           "book-123",
		Title:        "Test Book",
		BookStatusID: 5, // DNF status
	}, nil).Once()

	// Call the function
	err := svc.HandleFinishedBook(context.Background(), *audiobook, "456", userBookID)

	// Verify results
	assert.NoError(t, err, "Should not return an error when book is DNF")
	mockClient.AssertExpectations(t)
}

// TestIsBookDNF tests the isBookDNF helper method
func TestIsBookDNF(t *testing.T) {
	svc := createTestServiceWithoutMocks()

	tests := []struct {
		name     string
		book     *models.HardcoverBook
		expected bool
	}{
		{
			name: "Book is DNF",
			book: &models.HardcoverBook{
				ID:           "book-1",
				BookStatusID: 5, // DNF
			},
			expected: true,
		},
		{
			name: "Book is reading",
			book: &models.HardcoverBook{
				ID:           "book-2",
				BookStatusID: 2, // Currently Reading
			},
			expected: false,
		},
		{
			name: "Book is finished",
			book: &models.HardcoverBook{
				ID:           "book-3",
				BookStatusID: 3, // Finished
			},
			expected: false,
		},
		{
			name: "Book is want to read",
			book: &models.HardcoverBook{
				ID:           "book-4",
				BookStatusID: 1, // Want to Read
			},
			expected: false,
		},
		{
			name:     "Nil book",
			book:     nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.isBookDNF(tt.book)
			assert.Equal(t, tt.expected, result, "isBookDNF should return expected value")
		})
	}
}

// TestHandleInProgressBook_DNFDisabled tests that DNF books are updated when preservation is disabled
func TestHandleInProgressBook_DNFDisabled(t *testing.T) {
	// Create test service with DNF preservation disabled
	svc, mockClient := createTestService()
	svc.config.Sync.PreserveDNF = false

	// Create a test book with progress
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Progress.CurrentTime = 300 // Some progress
	testAudiobook.Media.Duration = 1000      // Duration
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Mock the GetUserBook call to return a book with DNF status
	userBookID := int64(123)
	mockClient.On("GetUserBook", mock.Anything, "123").Return(&models.HardcoverBook{
		ID:           "book-123",
		Title:        "Test Book",
		EditionID:    "456",
		BookStatusID: 5, // DNF status
	}, nil).Maybe()

	// Mock the GetUserBookReads calls
	mockClient.On("GetUserBookReads", mock.Anything, mock.AnythingOfType("hardcover.GetUserBookReadsInput")).Return([]hardcover.UserBookRead{}, nil).Maybe()

	// Mock the InsertUserBookRead call
	mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		return input.UserBookID == userBookID
	})).Return(789, nil).Maybe()

	// Mock the UpdateUserBookStatus call - should update from DNF to READING
	mockClient.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
		ID:       userBookID,
		StatusID: 2, // Currently Reading
	}).Return(nil).Maybe()

	// Call the function
	stateKey := fmt.Sprintf("%s:test-edition", audiobook.ID)
	err := svc.handleInProgressBook(context.Background(), userBookID, *audiobook, stateKey)

	// Verify results
	assert.NoError(t, err, "Should not return an error when DNF preservation is disabled")
}

// Helper function to create a test service without mocks
func createTestServiceWithoutMocks() *Service {
	// Setup logger
	logger.Setup(logger.Config{Level: "debug", Format: "json"})
	
	// Create test config
	cfg := config.DefaultConfig()
	cfg.Sync.PreserveDNF = true
	
	// Create service without initializing clients
	return &Service{
		config: cfg,
		log:    logger.Get(),
	}
}
