package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestFindBookInHardcoverByTitleAuthor(t *testing.T) {
	// Setup logger
	logger.Setup(logger.Config{Level: "debug", Format: "json"})
	log := logger.Get()

	// Test cases
	tests := []struct {
		name           string
		book           *TestAudiobookshelfBook
		searchResults  []*TestHardcoverBook
		searchError    error
		edition        *models.Edition
		editionError   error
		expectedBook   *models.HardcoverBook
		expectedError  bool
		errorSubstring string
	}{
		{
			name: "Success - Book found with title/author search",
			book: &TestAudiobookshelfBook{
				ID: "abs-book-1",
				Media: struct {
					ID        string
					Metadata  TestAudiobookshelfMetadata
					CoverPath string
					Duration  float64
				}{
					ID: "media-1",
					Metadata: TestAudiobookshelfMetadata{
						Title:      "Test Book",
						AuthorName: "Test Author",
					},
				},
			},
			searchResults: []*TestHardcoverBook{
				{
					ID:    "hc-book-1",
					Title: "Test Book",
				},
			},
			// No longer used in new implementation
			edition: &models.Edition{
				ID:     "edition-1",
				ASIN:   "B12345",
				ISBN13: "9781234567890",
				ISBN10: "1234567890",
			},
			expectedBook: &models.HardcoverBook{
				ID:    "hc-book-1",
				Title: "Test Book",
				// No longer set in new implementation
				// EditionID:    "edition-1",
				// EditionASIN:  "B12345",
				// EditionISBN13: "9781234567890",
				// EditionISBN10: "1234567890",
			},
			expectedError: false,
		},
		{
			name: "Error - Search API error",
			book: &TestAudiobookshelfBook{
				ID: "abs-book-2",
				Media: struct {
					ID        string
					Metadata  TestAudiobookshelfMetadata
					CoverPath string
					Duration  float64
				}{
					ID: "media-2",
					Metadata: TestAudiobookshelfMetadata{
						Title:      "Error Book",
						AuthorName: "Error Author",
					},
				},
			},
			searchError:    errors.New("search API error"),
			expectedError:  true,
			errorSubstring: "failed to search for books",
		},
		{
			name: "Error - No search results",
			book: &TestAudiobookshelfBook{
				ID: "abs-book-3",
				Media: struct {
					ID        string
					Metadata  TestAudiobookshelfMetadata
					CoverPath string
					Duration  float64
				}{
					ID: "media-3",
					Metadata: TestAudiobookshelfMetadata{
						Title:      "No Results Book",
						AuthorName: "No Results Author",
					},
				},
			},
			searchResults:  []*TestHardcoverBook{},
			expectedError:  true,
			errorSubstring: "no books found matching search query",
		},
		{
			name: "Success - Book formerly with edition error",
			book: &TestAudiobookshelfBook{
				ID: "abs-book-4",
				Media: struct {
					ID        string
					Metadata  TestAudiobookshelfMetadata
					CoverPath string
					Duration  float64
				}{
					ID: "media-4",
					Metadata: TestAudiobookshelfMetadata{
						Title:      "Edition Error Book",
						AuthorName: "Edition Error Author",
					},
				},
			},
			searchResults: []*TestHardcoverBook{
				{
					ID:    "hc-book-4",
					Title: "Edition Error Book",
				},
			},
			// No longer used in new implementation
			editionError:   errors.New("edition API error"),
			expectedBook: &models.HardcoverBook{
				ID:    "hc-book-4",
				Title: "Edition Error Book",
			},
			expectedError:  false, // No longer fails in new implementation
			// errorSubstring: "edition not found for book", // No longer relevant
		},
		{
			name: "Success - Book formerly with empty edition ID",
			book: &TestAudiobookshelfBook{
				ID: "abs-book-5",
				Media: struct {
					ID        string
					Metadata  TestAudiobookshelfMetadata
					CoverPath string
					Duration  float64
				}{
					ID: "media-5",
					Metadata: TestAudiobookshelfMetadata{
						Title:      "Empty Edition Book",
						AuthorName: "Empty Edition Author",
					},
				},
			},
			searchResults: []*TestHardcoverBook{
				{
					ID:    "hc-book-5",
					Title: "Empty Edition Book",
				},
			},
			// No longer used in new implementation
			edition: &models.Edition{
				ID:     "", // Empty edition ID
				ASIN:   "B67890",
				ISBN13: "9786789012345",
				ISBN10: "6789012345",
			},
			expectedBook: &models.HardcoverBook{
				ID:    "hc-book-5",
				Title: "Empty Edition Book",
			},
			expectedError:  false, // No longer fails in new implementation
			// errorSubstring: "edition ID is empty", // No longer relevant
		},
		{
			name: "Success - No author",
			book: &TestAudiobookshelfBook{
				ID: "abs-book-6",
				Media: struct {
					ID        string
					Metadata  TestAudiobookshelfMetadata
					CoverPath string
					Duration  float64
				}{
					ID: "media-6",
					Metadata: TestAudiobookshelfMetadata{
						Title:      "No Author Book",
						AuthorName: "", // Empty author
					},
				},
			},
			searchResults: []*TestHardcoverBook{
				{
					ID:    "hc-book-6",
					Title: "No Author Book",
				},
			},
			edition: &models.Edition{
				ID:     "edition-6",
				ASIN:   "B67890",
				ISBN13: "9786789012345",
				ISBN10: "6789012345",
			},
			expectedBook: &models.HardcoverBook{
				ID:           "hc-book-6",
				Title:        "No Author Book",
				EditionID:    "edition-6",
				EditionASIN:  "B67890",
				EditionISBN13: "9786789012345",
				EditionISBN10: "6789012345",
			},
			expectedError: false,
		},
		{
			name: "Success - Book formerly with nil edition",
			book: &TestAudiobookshelfBook{
				ID: "abs-book-7",
				Media: struct {
					ID        string
					Metadata  TestAudiobookshelfMetadata
					CoverPath string
					Duration  float64
				}{
					ID: "media-7",
					Metadata: TestAudiobookshelfMetadata{
						Title:      "Nil Edition Book",
						AuthorName: "Nil Edition Author",
					},
				},
			},
			searchResults: []*TestHardcoverBook{
				{
					ID:    "hc-book-7",
					Title: "Nil Edition Book",
				},
			},
			edition: nil, // Nil edition - no longer used in new implementation
			expectedBook: &models.HardcoverBook{
				ID:    "hc-book-7",
				Title: "Nil Edition Book",
			},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock Hardcover client
			mockClient := new(MockHardcoverClient)

			// Set up expectations for SearchBooks
			if tt.searchError != nil {
				mockClient.On("SearchBooks", mock.Anything, mock.Anything, mock.Anything).Return(nil, tt.searchError)
			} else {
				mockClient.On("SearchBooks", mock.Anything, mock.Anything, mock.Anything).Return(tt.searchResults, nil)
			}

			// We no longer call GetEdition with book ID in the new implementation
			// No need to set up expectations for GetEdition

			// Create service with mock client
			service := &Service{
				hardcover: mockClient,
				log:       log,
				config:    config.DefaultConfig(),
			}

			// Convert test book to model
			absBook := convertTestBookToModel(tt.book)

			// Call the function under test
			result, err := service.findBookInHardcoverByTitleAuthor(context.Background(), absBook)

			// Check error
			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorSubstring != "" {
					assert.Contains(t, err.Error(), tt.errorSubstring)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, tt.expectedBook.ID, result.ID)
				assert.Equal(t, tt.expectedBook.Title, result.Title)
			}

			// Verify all expectations were met
			mockClient.AssertExpectations(t)
		})
	}
}
