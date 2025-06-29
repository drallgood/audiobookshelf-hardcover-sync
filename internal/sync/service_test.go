package sync

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockHardcoverClient is a mock implementation of the hardcover.HardcoverClientInterface
type MockHardcoverClient struct {
	mock.Mock
}

// GetAuthHeader mocks the GetAuthHeader method
func (m *MockHardcoverClient) GetAuthHeader() string {
	args := m.Called()
	return args.String(0)
}

// AddWithMetadata mocks the AddWithMetadata method
func (m *MockHardcoverClient) AddWithMetadata(key string, value interface{}, metadata map[string]interface{}) error {
	args := m.Called(key, value, metadata)
	return args.Error(0)
}

// SaveToFile mocks the SaveToFile method
func (m *MockHardcoverClient) SaveToFile(filepath string) error {
	args := m.Called(filepath)
	return args.Error(0)
}

// GetEditionByASIN mocks the GetEditionByASIN method
func (m *MockHardcoverClient) GetEditionByASIN(ctx context.Context, asin string) (*models.Edition, error) {
	args := m.Called(ctx, asin)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Edition), args.Error(1)
}

// GetEditionByISBN13 mocks the GetEditionByISBN13 method
func (m *MockHardcoverClient) GetEditionByISBN13(ctx context.Context, isbn13 string) (*models.Edition, error) {
	args := m.Called(ctx, isbn13)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Edition), args.Error(1)
}

// GetGoogleUploadCredentials mocks the GetGoogleUploadCredentials method
func (m *MockHardcoverClient) GetGoogleUploadCredentials(ctx context.Context, filename string, editionID int) (*edition.GoogleUploadInfo, error) {
	args := m.Called(ctx, filename, editionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*edition.GoogleUploadInfo), args.Error(1)
}

// UpdateReadingProgress mocks the UpdateReadingProgress method
func (m *MockHardcoverClient) UpdateReadingProgress(ctx context.Context, bookID string, progress float64, status string, markAsOwned bool) error {
	args := m.Called(ctx, bookID, progress, status, markAsOwned)
	return args.Error(0)
}

// GetUserBookReads mocks the GetUserBookReads method
func (m *MockHardcoverClient) GetUserBookReads(ctx context.Context, input hardcover.GetUserBookReadsInput) ([]hardcover.UserBookRead, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]hardcover.UserBookRead), args.Error(1)
}

// InsertUserBookRead mocks the InsertUserBookRead method
func (m *MockHardcoverClient) InsertUserBookRead(ctx context.Context, input hardcover.InsertUserBookReadInput) (int, error) {
	args := m.Called(ctx, input)
	return args.Int(0), args.Error(1)
}

// UpdateUserBookRead mocks the UpdateUserBookRead method
func (m *MockHardcoverClient) UpdateUserBookRead(ctx context.Context, input hardcover.UpdateUserBookReadInput) (bool, error) {
	args := m.Called(ctx, input)
	return args.Bool(0), args.Error(1)
}

// CheckExistingUserBookRead mocks the CheckExistingUserBookRead method
func (m *MockHardcoverClient) CheckExistingUserBookRead(ctx context.Context, input hardcover.CheckExistingUserBookReadInput) (*hardcover.CheckExistingUserBookReadResult, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*hardcover.CheckExistingUserBookReadResult), args.Error(1)
}

// UpdateUserBookStatus mocks the UpdateUserBookStatus method
func (m *MockHardcoverClient) UpdateUserBookStatus(ctx context.Context, input hardcover.UpdateUserBookStatusInput) error {
	args := m.Called(ctx, input)
	return args.Error(0)
}

// CreateUserBook mocks the CreateUserBook method
func (m *MockHardcoverClient) CreateUserBook(ctx context.Context, editionID, status string) (string, error) {
	args := m.Called(ctx, editionID, status)
	return args.String(0), args.Error(1)
}

// SearchPublishers mocks the SearchPublishers method
func (m *MockHardcoverClient) SearchPublishers(ctx context.Context, name string, limit int) ([]models.Publisher, error) {
	args := m.Called(ctx, name, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Publisher), args.Error(1)
}

// SearchAuthors mocks the SearchAuthors method
func (m *MockHardcoverClient) SearchAuthors(ctx context.Context, name string, limit int) ([]models.Author, error) {
	args := m.Called(ctx, name, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Author), args.Error(1)
}

// SearchNarrators mocks the SearchNarrators method
func (m *MockHardcoverClient) SearchNarrators(ctx context.Context, name string, limit int) ([]models.Author, error) {
	args := m.Called(ctx, name, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.Author), args.Error(1)
}

// GetEdition mocks the GetEdition method
func (m *MockHardcoverClient) GetEdition(ctx context.Context, editionID string) (*models.Edition, error) {
	args := m.Called(ctx, editionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Edition), args.Error(1)
}

// CheckBookOwnership mocks the CheckBookOwnership method
func (m *MockHardcoverClient) CheckBookOwnership(ctx context.Context, editionID int) (bool, error) {
	args := m.Called(ctx, editionID)
	return args.Bool(0), args.Error(1)
}

// MarkEditionAsOwned mocks the MarkEditionAsOwned method
func (m *MockHardcoverClient) MarkEditionAsOwned(ctx context.Context, editionID int) error {
	args := m.Called(ctx, editionID)
	return args.Error(0)
}

// GetUserBookID mocks the GetUserBookID method
func (m *MockHardcoverClient) GetUserBookID(ctx context.Context, editionID int) (int, error) {
	args := m.Called(ctx, editionID)
	return args.Int(0), args.Error(1)
}

// GetUserBook mocks the GetUserBook method
func (m *MockHardcoverClient) GetUserBook(ctx context.Context, userBookID string) (*models.HardcoverBook, error) {
	args := m.Called(ctx, userBookID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.HardcoverBook), args.Error(1)
}

// SearchBookByISBN13 mocks the SearchBookByISBN13 method
func (m *MockHardcoverClient) SearchBookByISBN13(ctx context.Context, isbn13 string) (*models.HardcoverBook, error) {
	args := m.Called(ctx, isbn13)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.HardcoverBook), args.Error(1)
}

// SearchBookByASIN mocks the SearchBookByASIN method
func (m *MockHardcoverClient) SearchBookByASIN(ctx context.Context, asin string) (*models.HardcoverBook, error) {
	args := m.Called(ctx, asin)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.HardcoverBook), args.Error(1)
}

// SearchBookByISBN10 mocks the SearchBookByISBN10 method
func (m *MockHardcoverClient) SearchBookByISBN10(ctx context.Context, isbn10 string) (*models.HardcoverBook, error) {
	args := m.Called(ctx, isbn10)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.HardcoverBook), args.Error(1)
}

// SearchBooks mocks the SearchBooks method
func (m *MockHardcoverClient) SearchBooks(ctx context.Context, title, author string) ([]models.HardcoverBook, error) {
	args := m.Called(ctx, title, author)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.HardcoverBook), args.Error(1)
}

// createTestBook creates a test AudiobookshelfBook with default values
func createTestBook(editionID string) models.AudiobookshelfBook {
	book := models.AudiobookshelfBook{
		ID:        "test-book-123",
		LibraryID: "test-library-id",
		Path:      "/path/to/book",
		MediaType: "book",
	}

	// Set media fields
	book.Media.ID = "test-media-id"
	book.Media.Metadata.Title = "Test Book"
	book.Media.Metadata.AuthorName = "Test Author"
	book.Media.Metadata.PublishedYear = "2023"
	book.Media.Metadata.Genres = []string{"Fiction"}
	book.Media.Metadata.ISBN = "1234567890"
	book.Media.Metadata.ASIN = "test-asin"
	book.Media.CoverPath = "/path/to/cover"
	book.Media.Duration = 3600 // 1 hour

	// Set progress
	book.Progress.CurrentTime = 1800 // 30 minutes
	book.Progress.IsFinished = false

	return book
}

// createTestConfig creates a test configuration with the given sync_owned value
func createTestConfig(syncOwned bool) *config.Config {
	// Create a minimal config with just the fields we need for testing
	cfg := &config.Config{}
	
	// Set the App configuration
	cfg.App.SyncOwned = syncOwned
	cfg.App.Debug = false
	cfg.App.SyncInterval = 1 * time.Hour
	cfg.App.MinimumProgress = 0.01
	cfg.App.SyncWantToRead = true
	cfg.App.DryRun = false
	cfg.App.TestBookFilter = ""
	cfg.App.TestBookLimit = 0

	// Set the Hardcover token
	cfg.Hardcover.Token = "test-token"

	// Set Logging configuration
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"

	// Set Server configuration
	cfg.Server.Port = "8080"
	cfg.Server.ShutdownTimeout = 30 * time.Second

	return cfg
}

func TestProcessFoundBook_OwnershipSync(t *testing.T) {
	// Set up test cases
	tests := []struct {
		name        string
		syncOwned   bool
		editionID   string
		isOwned    bool
		expectError bool
		expectOwned bool
		hcBook     *models.HardcoverBook
		setupMocks func(*MockHardcoverClient)
	}{
		{
			name:        "should mark as owned when sync_owned is true and not owned",
			syncOwned:   true,
			editionID:   "456",
			isOwned:    false,
			expectError: false,
			expectOwned: true,
			hcBook: &models.HardcoverBook{
				ID:        "123",
				EditionID: "456",
			},
			setupMocks: func(m *MockHardcoverClient) {
				editionID := 456
				m.On("CheckBookOwnership", mock.Anything, editionID).Return(false, nil)
				m.On("MarkEditionAsOwned", mock.Anything, editionID).Return(nil)
				m.On("GetUserBookID", mock.Anything, editionID).Return(789, nil)
			},
		},
		{
			name:        "should not mark as owned when sync_owned is false",
			syncOwned:   false,
			editionID:   "456",
			isOwned:    false,
			expectError: false,
			expectOwned: false,
			hcBook: &models.HardcoverBook{
				ID:        "123",
				EditionID: "456",
			},
			setupMocks: func(m *MockHardcoverClient) {
				editionID := 456
				m.On("GetUserBookID", mock.Anything, editionID).Return(789, nil)
			},
		},
		{
			name:        "should not mark as owned when already owned",
			syncOwned:   true,
			editionID:   "456",
			isOwned:    true,
			expectError: false,
			expectOwned: true,
			hcBook: &models.HardcoverBook{
				ID:        "123",
				EditionID: "456",
			},
			setupMocks: func(m *MockHardcoverClient) {
				editionID := 456
				m.On("CheckBookOwnership", mock.Anything, editionID).Return(true, nil)
				m.On("GetUserBookID", mock.Anything, editionID).Return(789, nil)
			},
		},
		{
			name:        "should handle invalid edition ID format",
			syncOwned:   true,
			editionID:   "invalid",
			isOwned:    false,
			expectError: false, // Error is logged but doesn't fail the function
			expectOwned: false,
			hcBook: &models.HardcoverBook{
				ID:        "123",
				EditionID: "invalid",
			},
			setupMocks: func(m *MockHardcoverClient) {
				// No mocks should be called for invalid edition ID
			},
		},
		{
			name:        "should handle zero edition ID",
			syncOwned:   true,
			editionID:   "0",
			isOwned:    false,
			expectError: false,
			expectOwned: false,
			hcBook: &models.HardcoverBook{
				ID:        "123",
				EditionID: "0",
			},
			setupMocks: func(m *MockHardcoverClient) {
				// When edition ID is "0", the code will try to get the first edition using the book ID
				edition := &models.Edition{
					ID:     "789",
					BookID: "123",
				}
				m.On("GetEdition", mock.Anything, "123").Return(edition, nil)
				m.On("GetUserBookID", mock.Anything, 789).Return(101112, nil)
			},
		},
		{
			name:        "should handle CheckBookOwnership error",
			syncOwned:   true,
			editionID:   "456",
			isOwned:    false,
			expectError: false, // Error is logged but doesn't fail the function
			expectOwned: false,
			hcBook: &models.HardcoverBook{
				ID:        "123",
				EditionID: "456",
			},
			setupMocks: func(m *MockHardcoverClient) {
				editionID := 456
				m.On("CheckBookOwnership", mock.Anything, editionID).Return(false, errors.New("API error"))
				m.On("GetUserBookID", mock.Anything, editionID).Return(789, nil)
			},
		},
		{
			name:        "should handle MarkEditionAsOwned error",
			syncOwned:   true,
			editionID:   "456",
			isOwned:    false,
			expectError: false, // Error is logged but doesn't fail the function
			expectOwned: false,
			hcBook: &models.HardcoverBook{
				ID:        "123",
				EditionID: "456",
			},
			setupMocks: func(m *MockHardcoverClient) {
				editionID := 456
				m.On("CheckBookOwnership", mock.Anything, editionID).Return(false, nil)
				m.On("MarkEditionAsOwned", mock.Anything, editionID).Return(errors.New("API error"))
				m.On("GetUserBookID", mock.Anything, editionID).Return(789, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock client
			mockClient := new(MockHardcoverClient)
			if tt.setupMocks != nil {
				tt.setupMocks(mockClient)
			}

			// Set up test data
			testBook := createTestBook(tt.editionID)
			testCfg := createTestConfig(tt.syncOwned)

			// Create a test logger
			logger := logger.Get()

			// Create a test service with the mock client
			svc := &Service{
				hardcover: mockClient,
				config:    testCfg,
				log:       logger,
			}

			// Call the function under test with the test book
			result, err := svc.processFoundBook(context.Background(), tt.hcBook, testBook)

			// Assertions
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, tt.hcBook.ID, result.ID)
				assert.Equal(t, tt.hcBook.EditionID, result.EditionID)

				// Verify mock expectations
				mockClient.AssertExpectations(t)
			}
		})
	}
}

func TestConcurrentOwnershipUpdates(t *testing.T) {
	// Set up test data
	editionID := "123"
	testBook := createTestBook(editionID)

	// Create a mock client
	mockClient := new(MockHardcoverClient)

	// Set up mock expectations
	hcBook := &models.HardcoverBook{
		ID:        "123",
		EditionID: editionID,
	}

	// Set up mock expectations for the concurrent test
	// Each of the 10 goroutines will call GetUserBookID once
	editionIDInt, _ := strconv.Atoi(editionID)
	for i := 0; i < 10; i++ {
		mockClient.On("GetUserBookID", mock.Anything, editionIDInt).Return(456, nil)
	}

	mockClient.On("GetEdition", mock.Anything, editionID).Return(&models.Edition{ID: editionID}, nil)
	mockClient.On("CheckBookOwnership", mock.Anything, mock.AnythingOfType("int")).Return(false, nil)
	mockClient.On("MarkEditionAsOwned", mock.Anything, mock.AnythingOfType("int")).Return(nil)

	// Create a test config
	cfg := createTestConfig(true)
	
	// Declare variables that will be used later
	var svc *Service

	// Create a test service with the mock client
	svc = &Service{
		hardcover: mockClient,
		config:    cfg,
		log:       logger.Get(),
	}

	// Number of concurrent goroutines
	numRoutines := 10

	// Channel to collect results
	errCh := make(chan error, numRoutines)

	// Run concurrent ownership updates
	for i := 0; i < numRoutines; i++ {
		go func() {
			_, err := svc.processFoundBook(context.Background(), hcBook, testBook)
			errCh <- err
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < numRoutines; i++ {
		err := <-errCh
		assert.NoError(t, err, "concurrent ownership update failed")
	}
}

func TestProcessFoundBook_WithBook(t *testing.T) {
	// Setup logger for testing
	logger.Setup(logger.Config{Level: "debug"})
	log := logger.Get()

	// Create a test config
	cfg := createTestConfig(true)

	// Create a mock client
	mockClient := &MockHardcoverClient{}

	// Create a test service with the mock client
	svc := &Service{
		hardcover: mockClient,
		config:    cfg,
		log:       log,
	}
	
	// Use the svc variable to avoid unused variable warning
	_ = svc

	// Create a test context
	ctx := context.Background()

	// Test case: Valid book with edition ID
	t.Run("valid book with edition ID", func(t *testing.T) {
		// Create a new mock client for this test case
		mockClient := new(MockHardcoverClient)

		// Create a test config
		cfg := createTestConfig(true)

		// Create a test service with the mock client
		svc := &Service{
			hardcover: mockClient,
			config:    cfg,
			log:       logger.Get(),
		}

		// Create a test book with an edition ID
		audiobook := createTestBook("123")
		audiobook.Media.Metadata.ISBN = "9781234567890"
		audiobook.Media.Metadata.ASIN = "B08N5KWB9H"

		// Create a minimal HardcoverBook with an edition ID
		hcBook := &models.HardcoverBook{
			ID:        "123",
			EditionID: "456",
		}

		// Set up mock expectations for this test case
		// The code may call GetEdition multiple times with different parameters
		mockClient.On("GetEdition", mock.Anything, hcBook.ID).Return(&models.Edition{
			ID:     hcBook.ID,
			BookID: hcBook.ID,
			ASIN:   "B08N5KWB9H",
			ISBN10: "1234567890",
			ISBN13: "9781234567890",
		}, nil).Maybe()
		
		mockClient.On("GetEdition", mock.Anything, hcBook.EditionID).Return(&models.Edition{
			ID:     hcBook.EditionID,
			BookID: hcBook.ID,
			ASIN:   "B08N5KWB9H",
			ISBN10: "1234567890",
			ISBN13: "9781234567890",
		}, nil).Maybe()

		mockClient.On("GetUserBookID", mock.Anything, mock.AnythingOfType("int")).Return(789, nil).Maybe()
		mockClient.On("CheckBookOwnership", mock.Anything, mock.AnythingOfType("int")).Return(false, nil).Maybe()
		mockClient.On("MarkEditionAsOwned", mock.Anything, mock.AnythingOfType("int")).Return(nil).Maybe()

		// Create a test context
		ctx := context.Background()

		// Call the function with the audiobook as a value (not pointer)
		result, err := svc.processFoundBook(ctx, hcBook, audiobook)

		// Assertions
		assert.NoError(t, err, "processFoundBook should not return an error for a valid book with edition ID")
		assert.NotNil(t, result, "result should not be nil")

		// Verify mock expectations
		mockClient.AssertExpectations(t)
	})

	// Test case 2: Book with ISBN but no edition ID
	t.Run("with ISBN", func(t *testing.T) {
		// Create a new mock client for this test case
		mockClient := new(MockHardcoverClient)

		// Create a test config
		cfg := createTestConfig(true)

		// Create a test service with the mock client
		svc := &Service{
			hardcover: mockClient,
			config:    cfg,
			log:       logger.Get(),
		}

		// Create a test book with ISBN but no edition ID
		audiobook := createTestBook("")
		audiobook.Media.Metadata.ISBN = "9781234567890"
		audiobook.Media.Metadata.ASIN = ""

		// Set up mock expectations for this test case
		// The code first searches by ISBN, then gets the edition
		editionID := "123"
		editionIDInt, _ := strconv.Atoi(editionID)
		
		mockClient.On("SearchBookByISBN13", mock.Anything, audiobook.Media.Metadata.ISBN).Return(&models.HardcoverBook{
			ID:        editionID,
			EditionID: editionID,
		}, nil)
		
		mockClient.On("GetUserBookID", mock.Anything, editionIDInt).Return(456, nil)
		
		// The code calls GetEdition with the book/edition ID
		mockClient.On("GetEdition", mock.Anything, editionID).Return(&models.Edition{
			ID:     editionID,
			BookID: editionID,
			ASIN:   "B08N5KWB9H",
			ISBN10: "1234567890",
			ISBN13: audiobook.Media.Metadata.ISBN,
		}, nil)
		
		mockClient.On("CheckBookOwnership", mock.Anything, mock.AnythingOfType("int")).Return(false, nil)
		mockClient.On("MarkEditionAsOwned", mock.Anything, mock.AnythingOfType("int")).Return(nil)

		// Create a minimal HardcoverBook
		hcBook := &models.HardcoverBook{
			ID: "123",
			// No EditionID
		}

		// Call the function with the audiobook as a value (not pointer)
		result, err := svc.processFoundBook(ctx, hcBook, audiobook)
		assert.NoError(t, err, "processFoundBook should not return an error when ISBN is available but no edition ID")
		assert.NotNil(t, result, "result should not be nil")
	})

	// Test case 3: Book with ASIN but no edition ID
	t.Run("with ASIN", func(t *testing.T) {
		// Create a new mock client for this test case
		mockClient := new(MockHardcoverClient)

		// Create a test config
		cfg := createTestConfig(true)

		// Create a test service with the mock client
		svc := &Service{
			hardcover: mockClient,
			config:    cfg,
			log:       logger.Get(),
		}

		// Create a test book with ASIN but no edition ID
		audiobook := createTestBook("")
		audiobook.Media.Metadata.ISBN = ""
		audiobook.Media.Metadata.ASIN = "B08N5KWB9H"

		// Set up mock expectations for this test case
		editionID := "789"
		editionIDInt, _ := strconv.Atoi(editionID)
		bookID := "123"
		
		mockClient.On("SearchBookByASIN", mock.Anything, audiobook.Media.Metadata.ASIN).Return(&models.HardcoverBook{
			ID:        bookID,
			EditionID: editionID,
		}, nil)
		
		// The code may call GetEdition with either the book ID or the edition ID
		hcEdition := &models.Edition{
			ID:     editionID,
			BookID: bookID,
			ASIN:   audiobook.Media.Metadata.ASIN,
			ISBN10: "1234567890",
			ISBN13: "9781234567890",
		}
		
		// Handle both possible GetEdition calls
		mockClient.On("GetEdition", mock.Anything, bookID).Return(hcEdition, nil).Maybe()
		mockClient.On("GetEdition", mock.Anything, editionID).Return(hcEdition, nil).Maybe()
		
		mockClient.On("GetUserBookID", mock.Anything, editionIDInt).Return(456, nil).Maybe()
		mockClient.On("CheckBookOwnership", mock.Anything, mock.AnythingOfType("int")).Return(false, nil).Maybe()
		mockClient.On("MarkEditionAsOwned", mock.Anything, mock.AnythingOfType("int")).Return(nil).Maybe()

		// Create a minimal HardcoverBook
		hcBook := &models.HardcoverBook{
			ID: "123",
			// No EditionID
		}

		// Call the function with the audiobook as a value (not pointer)
		result, err := svc.processFoundBook(context.Background(), hcBook, audiobook)
		assert.NoError(t, err, "processFoundBook should not return an error when ASIN is available but no edition ID")
		assert.NotNil(t, result, "result should not be nil")
	})
}

func TestProcessFoundBook_NilBook(t *testing.T) {
	// Setup logger for testing
	logger.Setup(logger.Config{Level: "debug"})
	log := logger.Get()

	// Create a test config
	cfg := createTestConfig(true)

	// Create a mock client
	mockClient := new(MockHardcoverClient)

	// Create a test service with the mock client
	svc := &Service{
		hardcover: mockClient,
		config:    cfg,
		log:       log,
	}

	// Create a minimal valid book struct for testing
	audiobook := createTestBook("test-book-123")
	audiobook.Media.Metadata.Title = "Test Book"
	audiobook.Media.Metadata.AuthorName = "Test Author"
	audiobook.Media.Metadata.Genres = []string{"Fiction"}

	// Test with nil book
	t.Run("nil book", func(t *testing.T) {
		// No mock expectations needed for this case as we expect an early return

		result, err := svc.processFoundBook(context.Background(), nil, audiobook)

		// Verify the error
		assert.Error(t, err, "processFoundBook should return an error when book is nil")
		assert.Contains(t, err.Error(), "book cannot be nil", "Error message should indicate that book cannot be nil")
		assert.Nil(t, result, "Result should be nil when book is nil")

		// Verify no unexpected calls were made
		mockClient.AssertExpectations(t)
	})

	// Test with nil edition ID
	t.Run("nil edition ID", func(t *testing.T) {
		// Reset mock expectations
		mockClient.ExpectedCalls = nil

		hcBook := &models.HardcoverBook{
			ID: "test-book-123",
			// No EditionID set
		}

		// When edition ID is empty, the function will try to get the edition by book ID
		expectedEdition := &models.Edition{
			ID:     "456",
			BookID: "test-book-123",
			ASIN:   "B08N5KWB9H",
			ISBN10: "1234567890",
			ISBN13: "9781234567890",
		}
		mockClient.On("GetEdition", mock.Anything, "test-book-123").Return(expectedEdition, nil)

		// The function will try to get the user book ID for the edition
		editionID, _ := strconv.Atoi(expectedEdition.ID)
		mockClient.On("GetUserBookID", mock.Anything, editionID).Return(0, errors.New("not found"))

		// Since we're not testing ownership sync here, we can return early
		// after the GetUserBookID call

		result, err := svc.processFoundBook(context.Background(), hcBook, audiobook)

		// Verify the result
		assert.NoError(t, err, "processFoundBook should not return an error when edition ID is empty")
		assert.NotNil(t, result, "processFoundBook should return a non-nil result")
		assert.Equal(t, "test-book-123", result.ID, "Returned book should have the expected ID")

		// Verify expected calls were made
		mockClient.AssertExpectations(t)
	})

	// Test with invalid edition ID
	t.Run("invalid edition ID", func(t *testing.T) {
		// Reset mock expectations
		mockClient.ExpectedCalls = nil

		hcBook := &models.HardcoverBook{
			ID:        "test-book-123",
			EditionID: "invalid",
		}

		// For invalid edition ID, we expect a warning log but no API calls

		result, err := svc.processFoundBook(context.Background(), hcBook, audiobook)

		// Verify the result
		assert.NoError(t, err, "processFoundBook should not return an error when edition ID is invalid")
		assert.NotNil(t, result, "processFoundBook should return a non-nil result")
		assert.Equal(t, "test-book-123", result.ID, "Returned book should have the expected ID")

		// Verify no unexpected calls were made
		mockClient.AssertExpectations(t)
	})
}
