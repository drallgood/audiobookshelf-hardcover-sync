package sync

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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

// ClearUserBookCache mocks the ClearUserBookCache method
func (m *MockHardcoverClient) ClearUserBookCache() {
	m.Called()
}

// GetUserBook mocks the GetUserBook method
func (m *MockHardcoverClient) GetUserBook(ctx context.Context, userBookID string) (*models.HardcoverBook, error) {
	args := m.Called(ctx, userBookID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	
	// Check if the returned value is already a *models.HardcoverBook
	if book, ok := args.Get(0).(*models.HardcoverBook); ok {
		return book, args.Error(1)
	}
	
	// Otherwise, try to convert from *TestHardcoverBook
	testBook, ok := args.Get(0).(*TestHardcoverBook)
	if !ok {
		panic("GetUserBook mock return value must be either *models.HardcoverBook or *TestHardcoverBook")
	}
	
	// Convert TestHardcoverBook to models.HardcoverBook
	return &models.HardcoverBook{
		ID:            testBook.ID,
		UserBookID:    testBook.UserBookID,
		EditionID:     testBook.EditionID,
		Title:         testBook.Title,
		Subtitle:      testBook.Subtitle,
		Authors:       testBook.Authors,
		Narrators:     testBook.Narrators,
		CoverImageURL: testBook.CoverImageURL,
		Description:   testBook.Description,
		PageCount:     testBook.PageCount,
		ReleaseDate:   testBook.ReleaseDate,
		Publisher:     testBook.Publisher,
		ISBN:          testBook.ISBN,
		ASIN:          testBook.ASIN,
		BookStatusID:  testBook.BookStatusID,
		CanonicalID:   testBook.CanonicalID,
		EditionASIN:   testBook.EditionASIN,
		EditionISBN10: testBook.EditionISBN10,
		EditionISBN13: testBook.EditionISBN13,
	}, args.Error(1)
}

func (m *MockHardcoverClient) GetBookByID(ctx context.Context, bookID string) (*models.HardcoverBook, error) {
	args := m.Called(ctx, bookID)
	// Support both *models.HardcoverBook and *TestHardcoverBook, including typed-nil
	if b, ok := args.Get(0).(*models.HardcoverBook); ok {
		if b == nil {
			return nil, args.Error(1)
		}
		return b, args.Error(1)
	}
	if tb, ok := args.Get(0).(*TestHardcoverBook); ok {
		if tb == nil {
			return nil, args.Error(1)
		}
		return toHardcoverBook(tb), args.Error(1)
	}
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	panic("GetBookByID mock return value must be either *models.HardcoverBook or *TestHardcoverBook or nil")
}

// SearchBookByISBN13 mocks the SearchBookByISBN13 method
func (m *MockHardcoverClient) SearchBookByISBN13(ctx context.Context, isbn13 string) (*models.HardcoverBook, error) {
    args := m.Called(ctx, isbn13)
    // Accept typed-nil values safely
    if b, ok := args.Get(0).(*models.HardcoverBook); ok {
        if b == nil {
            return nil, args.Error(1)
        }
        return b, args.Error(1)
    }
    if tb, ok := args.Get(0).(*TestHardcoverBook); ok {
        if tb == nil {
            return nil, args.Error(1)
        }
        // Convert TestHardcoverBook to models.HardcoverBook
        return &models.HardcoverBook{
            ID:            tb.ID,
            UserBookID:    tb.UserBookID,
            EditionID:     tb.EditionID,
            Title:         tb.Title,
            Subtitle:      tb.Subtitle,
            Authors:       tb.Authors,
            Narrators:     tb.Narrators,
            CoverImageURL: tb.CoverImageURL,
            Description:   tb.Description,
            PageCount:     tb.PageCount,
            ReleaseDate:   tb.ReleaseDate,
            Publisher:     tb.Publisher,
            ISBN:          tb.ISBN,
            ASIN:          tb.ASIN,
            BookStatusID:  tb.BookStatusID,
            CanonicalID:   tb.CanonicalID,
            EditionASIN:   tb.EditionASIN,
            EditionISBN10: tb.EditionISBN10,
            EditionISBN13: tb.EditionISBN13,
        }, args.Error(1)
    }
    if args.Get(0) == nil {
        return nil, args.Error(1)
    }
    panic("SearchBookByASIN mock return must be *models.HardcoverBook, *TestHardcoverBook or nil")
}

// SearchBookByASIN mocks the SearchBookByASIN method
func (m *MockHardcoverClient) SearchBookByASIN(ctx context.Context, asin string) (*models.HardcoverBook, error) {
    args := m.Called(ctx, asin)
    // Accept typed-nil values safely
    if b, ok := args.Get(0).(*models.HardcoverBook); ok {
        if b == nil {
            return nil, args.Error(1)
        }
        return b, args.Error(1)
    }
    if tb, ok := args.Get(0).(*TestHardcoverBook); ok {
        if tb == nil {
            return nil, args.Error(1)
        }
        return &models.HardcoverBook{
            ID:            tb.ID,
            UserBookID:    tb.UserBookID,
            EditionID:     tb.EditionID,
            Title:         tb.Title,
            Subtitle:      tb.Subtitle,
            Authors:       tb.Authors,
            Narrators:     tb.Narrators,
            CoverImageURL: tb.CoverImageURL,
            Description:   tb.Description,
            PageCount:     tb.PageCount,
            ReleaseDate:   tb.ReleaseDate,
            Publisher:     tb.Publisher,
            ISBN:          tb.ISBN,
            ASIN:          tb.ASIN,
            BookStatusID:  tb.BookStatusID,
            CanonicalID:   tb.CanonicalID,
            EditionASIN:   tb.EditionASIN,
            EditionISBN10: tb.EditionISBN10,
            EditionISBN13: tb.EditionISBN13,
        }, args.Error(1)
    }
    if args.Get(0) == nil {
        return nil, args.Error(1)
    }
    panic("SearchBookByASIN mock return must be *models.HardcoverBook, *TestHardcoverBook or nil")
}

// SearchBookByISBN10 mocks the SearchBookByISBN10 method
func (m *MockHardcoverClient) SearchBookByISBN10(ctx context.Context, isbn10 string) (*models.HardcoverBook, error) {
	args := m.Called(ctx, isbn10)
	// Accept typed-nil values safely
	if b, ok := args.Get(0).(*models.HardcoverBook); ok {
		if b == nil {
			return nil, args.Error(1)
		}
		return b, args.Error(1)
	}
	if tb, ok := args.Get(0).(*TestHardcoverBook); ok {
		if tb == nil {
			return nil, args.Error(1)
		}
		return &models.HardcoverBook{
			ID:            tb.ID,
			UserBookID:    tb.UserBookID,
			EditionID:     tb.EditionID,
			Title:         tb.Title,
			Subtitle:      tb.Subtitle,
			Authors:       tb.Authors,
			Narrators:     tb.Narrators,
			CoverImageURL: tb.CoverImageURL,
			Description:   tb.Description,
			PageCount:     tb.PageCount,
			ReleaseDate:   tb.ReleaseDate,
			Publisher:     tb.Publisher,
			ISBN:          tb.ISBN,
			ASIN:          tb.ASIN,
			BookStatusID:  tb.BookStatusID,
			CanonicalID:   tb.CanonicalID,
			EditionASIN:   tb.EditionASIN,
			EditionISBN10: tb.EditionISBN10,
			EditionISBN13: tb.EditionISBN13,
		}, args.Error(1)
	}
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	panic("SearchBookByISBN10 mock return must be *models.HardcoverBook, *TestHardcoverBook or nil")
}

// SearchBooks mocks the SearchBooks method
func (m *MockHardcoverClient) SearchBooks(ctx context.Context, title, author string) ([]models.HardcoverBook, error) {
	args := m.Called(ctx, title, author)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	
	// Handle both []models.HardcoverBook and []*TestHardcoverBook types
	switch books := args.Get(0).(type) {
	case []models.HardcoverBook:
		// Already the correct type, return as is
		return books, args.Error(1)
		
	case []*TestHardcoverBook:
		// Convert []*TestHardcoverBook to []models.HardcoverBook
		result := make([]models.HardcoverBook, len(books))
		for i, book := range books {
			// Convert TestHardcoverBook to models.HardcoverBook using the helper function
			hcBook := toHardcoverBook(book)
			result[i] = *hcBook
		}
		return result, args.Error(1)
		
	case *models.HardcoverBook:
		// Single book provided, return as a slice with one element
		return []models.HardcoverBook{*books}, args.Error(1)
	}
	
	return nil, fmt.Errorf("unsupported type for SearchBooks mock")

}

// createTestConfig creates a test configuration with the specified sync ownership setting
// and other default test values.
func createTestConfig(syncOwned bool) *config.Config {
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
	
	// Clear deprecated fields in App
	cfg.App = struct {
		TestBookFilter string `yaml:"test_book_filter" env:"TEST_BOOK_FILTER"`
		TestBookLimit  int    `yaml:"test_book_limit" env:"TEST_BOOK_LIMIT"`
		// Deprecated fields for backward compatibility
		SyncInterval    time.Duration `yaml:"sync_interval,omitempty" env:"-"`
		MinimumProgress float64       `yaml:"minimum_progress,omitempty" env:"-"`
		SyncWantToRead  bool          `yaml:"sync_want_to_read,omitempty" env:"-"`
		SyncOwned       bool          `yaml:"sync_owned,omitempty" env:"-"`
		DryRun          bool          `yaml:"dry_run,omitempty" env:"-"`
	}{
		TestBookFilter: "",
		TestBookLimit:  0,
	}
	
	return cfg
}

func TestProcessFoundBook_OwnershipSync(t *testing.T) {
	// Set up test cases
	tests := []struct {
		name         string
		syncOwned    bool
		hcBook       *TestHardcoverBook
		expectError  bool
		expectOwned  bool
		setupMocks   func(*MockHardcoverClient)
		verifyMocks  func(*testing.T, *MockHardcoverClient)
		verifyResult func(*testing.T, *models.HardcoverBook, error)
	}{
		{
			name:        "should mark as owned when sync_owned is true and not owned",
			syncOwned:   true,
			hcBook: &TestHardcoverBook{
				ID:        "123",
				EditionID: "456",
			},
			expectError: false,
			expectOwned: true,
			setupMocks: func(m *MockHardcoverClient) {
				editionID := 456
				// Expect GetUserBookID and CheckBookOwnership since sync_owned is true
				m.On("GetUserBookID", mock.Anything, editionID).Return(789, nil)
				// CheckBookOwnership uses BOOK ID (123)
				m.On("CheckBookOwnership", mock.Anything, 123).Return(false, nil)
				m.On("MarkEditionAsOwned", mock.Anything, editionID).Return(nil)
			},
			verifyResult: func(t *testing.T, result *models.HardcoverBook, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, "123", result.ID)
				assert.Equal(t, "456", result.EditionID)
			},
		},
		{
			name:        "should not mark as owned when sync_owned is false",
			syncOwned:   false,
			hcBook: &TestHardcoverBook{
				ID:        "123",
				EditionID: "456",
			},
			expectError: false,
			expectOwned: false,
			setupMocks: func(m *MockHardcoverClient) {
				editionID := 456
				// Only expect GetUserBookID when sync_owned is false
				m.On("GetUserBookID", mock.Anything, editionID).Return(789, nil)
			},
			verifyResult: func(t *testing.T, result *models.HardcoverBook, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, "123", result.ID)
				assert.Equal(t, "456", result.EditionID)
			},
		},
		{
			name:        "should not mark as owned when already owned",
			syncOwned:   true,
			hcBook: &TestHardcoverBook{
				ID:        "123",
				EditionID: "456",
			},
			expectError: false,
			expectOwned: true,
			setupMocks: func(m *MockHardcoverClient) {
				editionID := 456
				// Expect GetUserBookID and CheckBookOwnership since sync_owned is true
				m.On("GetUserBookID", mock.Anything, editionID).Return(789, nil)
				// Return true to indicate the book is already owned (BOOK ID)
				m.On("CheckBookOwnership", mock.Anything, 123).Return(true, nil)
			},
			verifyResult: func(t *testing.T, result *models.HardcoverBook, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, "123", result.ID)
				assert.Equal(t, "456", result.EditionID)
			},
		},
		{
			name:        "should handle invalid edition ID format",
			syncOwned:   true,
			hcBook: &TestHardcoverBook{
				ID:        "123",
				EditionID: "invalid",
			},
			expectError: false, // Error is logged but doesn't fail the function
			expectOwned: false,
			setupMocks: func(m *MockHardcoverClient) {
				// No mocks should be called for invalid edition ID
			},
			verifyResult: func(t *testing.T, result *models.HardcoverBook, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, "123", result.ID)
				assert.Equal(t, "invalid", result.EditionID)
			},
		},
		{
			name:        "should handle zero edition ID",
			syncOwned:   true,
			hcBook: &TestHardcoverBook{
				ID:        "123",
				EditionID: "0",
			},
			expectError: false,
			expectOwned: false,
			setupMocks: func(m *MockHardcoverClient) {
				// When edition ID is "0", the code will try to get the first edition using the book ID
				edition := &models.Edition{
					ID:     "789",
					BookID: "123",
				}
				m.On("GetEdition", mock.Anything, "123").Return(edition, nil)
				m.On("GetUserBookID", mock.Anything, 789).Return(101112, nil)
			},
			verifyResult: func(t *testing.T, result *models.HardcoverBook, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, "123", result.ID)
				// The edition ID should be updated to "789" from the mock
				assert.Equal(t, "789", result.EditionID)
				// The user_book_id should be "101112" because that's what we return in the mock
				// for GetUserBookID when edition ID is 789
				assert.Equal(t, "101112", result.UserBookID)
			},
		},
		{
			name:        "should handle CheckBookOwnership error",
			syncOwned:   true,
			hcBook: &TestHardcoverBook{
				ID:        "123",
				EditionID: "456",
			},
			expectError: false, // Error is logged but doesn't fail the function
			expectOwned: false,
			setupMocks: func(m *MockHardcoverClient) {
				editionID := 456
				// Expect GetUserBookID and CheckBookOwnership with error
				m.On("GetUserBookID", mock.Anything, editionID).Return(789, nil)
				// BOOK ID is used for ownership check
				m.On("CheckBookOwnership", mock.Anything, 123).Return(false, errors.New("ownership check failed"))
			},
			verifyResult: func(t *testing.T, result *models.HardcoverBook, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, "123", result.ID)
				assert.Equal(t, "456", result.EditionID)
			},
		},
		{
			name:        "should handle MarkEditionAsOwned error",
			syncOwned:   true,
			hcBook: &TestHardcoverBook{
				ID:        "123",
				EditionID: "456",
			},
			expectError: false, // Error is logged but doesn't fail the function
			expectOwned: false,
			setupMocks: func(m *MockHardcoverClient) {
				editionID := 456
				// Expect GetUserBookID, CheckBookOwnership, and MarkEditionAsOwned with error
				m.On("GetUserBookID", mock.Anything, editionID).Return(789, nil)
				// Use BOOK ID for ownership check
				m.On("CheckBookOwnership", mock.Anything, 123).Return(false, nil)
				m.On("MarkEditionAsOwned", mock.Anything, editionID).Return(errors.New("failed to mark as owned"))
			},
			verifyResult: func(t *testing.T, result *models.HardcoverBook, err error) {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.Equal(t, "123", result.ID)
				assert.Equal(t, "456", result.EditionID)
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
			authorName := "Test Author"
			if len(tt.hcBook.Authors) > 0 {
				authorName = tt.hcBook.Authors[0].Name
			}
			testBook := createTestBook(tt.hcBook.EditionID, tt.hcBook.Title, authorName, tt.hcBook.ASIN, tt.hcBook.ISBN)
			testCfg := createTestConfig(tt.syncOwned)

			// Create a test logger
			logger := logger.Get()

			// Create a test service with the mock client
			svc := &Service{
				hardcover: mockClient,
				config:    testCfg,
				log:       logger,
			}

			// Convert test books to production models
			hcBook := toHardcoverBook(tt.hcBook)
			absBook := toAudiobookshelfBook(testBook)

			// Call the function under test with the converted books
			result, err := svc.processFoundBook(context.Background(), hcBook, *absBook)

			// Assertions
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				if tt.verifyResult != nil {
					tt.verifyResult(t, result, err)
				}
			}

			// Verify mock expectations
			mockClient.AssertExpectations(t)
		})
	}
}

func createTestService() (*Service, *MockHardcoverClient) {
	// Setup logger for testing
	logger.Setup(logger.Config{Level: "debug"})

	// Create a test config
	cfg := createTestConfig(true)

	// Create a mock client
	mockClient := &MockHardcoverClient{}

	// Create a test state
	state := state.NewState()

	// Create and initialize caches
	persistentCache := NewPersistentASINCache("/tmp/test-cache")
	_ = persistentCache.Load() // Load cache (will create empty if doesn't exist)
	
	userBookCache := NewPersistentUserBookCache("/tmp/test-cache")
	_ = userBookCache.Load() // Load cache (will create empty if doesn't exist)

	// Create and return a test service with the mock client
	svc := &Service{
		hardcover: mockClient,
		config:    cfg,
		log:       logger.Get(),
		state:     state,
		lastProgressUpdates: make(map[string]progressUpdateInfo),
		asinCache:           make(map[string]*models.HardcoverBook),
		persistentCache:     persistentCache,
		userBookCache:       userBookCache,
		createdReadsThisRun: make(map[int64]struct{}),
	}

	return svc, mockClient
}

func TestProcessFoundBook_WithBook(t *testing.T) {
	svc, mockClient := createTestService()
	ctx := context.Background()

	t.Run("should handle book with edition ID", func(t *testing.T) {
		// Create a test HardcoverBook with edition ID and convert to production model
		testHcBook := &TestHardcoverBook{
			ID:        "123",
			EditionID: "456",
		}
		hcBook := toHardcoverBook(testHcBook)
		audiobook := &models.AudiobookshelfBook{} // Initialize with required fields

		// Set up mock expectations for this test case
		// The code may call GetEdition multiple times with different parameters
		edition := &models.Edition{
			ID:     hcBook.EditionID,
			BookID: hcBook.ID,
			ASIN:   "B08N5KWB9H",
			ISBN10: "1234567890",
			ISBN13: "9781234567890",
		}

		// Handle both possible GetEdition calls (by book ID or edition ID)
		mockClient.On("GetEdition", mock.Anything, hcBook.ID).Return(edition, nil).Maybe()
		mockClient.On("GetEdition", mock.Anything, hcBook.EditionID).Return(edition, nil).Maybe()

		editionIDInt, _ := strconv.Atoi(hcBook.EditionID)
		mockClient.On("GetUserBookID", mock.Anything, editionIDInt).Return(789, nil).Maybe()

		// The code may call these methods with different parameters
		mockClient.On("UpdateReadingProgress", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockClient.On("CheckBookOwnership", mock.Anything, mock.AnythingOfType("int")).Return(false, nil).Maybe()
		mockClient.On("MarkEditionAsOwned", mock.Anything, mock.AnythingOfType("int")).Return(nil).Maybe()

		// Call the function with the audiobook as a value (dereference the pointer)
		result, err := svc.processFoundBook(ctx, hcBook, *audiobook)

		// Verify results
		assert.NoError(t, err, "processFoundBook should not return an error when book has an edition ID")
		assert.NotNil(t, result, "result should not be nil")

		// Verify mock expectations
		mockClient.AssertExpectations(t)
	})
}

func TestProcessFoundBook_WithBook_NoEditionID(t *testing.T) {
	svc, mockClient := createTestService()
	ctx := context.Background()

	// Create a test audiobook with ISBN
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Create a test HardcoverBook with no edition ID
	testHcBook := &TestHardcoverBook{
		ID: "123",
		// No EditionID
	}
	hcBook := toHardcoverBook(testHcBook)

	// Set up mock expectations for searching by ISBN
	t.Run("should handle book with no edition ID but with ISBN", func(t *testing.T) {
		// First, the function will try to get the edition using the book ID (123)
		mockClient.On("GetEdition", mock.Anything, "123").Return((*models.Edition)(nil), nil).Once()

		// The function will then search by ISBN, but since we're testing processFoundBook directly,
		// we don't need to set up the search mock here. The test is verifying the behavior
		// when the edition is not found by the initial GetEdition call.

		// Call the function with the audiobook as a value (dereference the pointer)
		result, err := svc.processFoundBook(ctx, hcBook, *audiobook)

		// Verify results - the function should return the original book with no edition ID
		// since we didn't set up the search mock to be called
		assert.NoError(t, err, "processFoundBook should not return an error when book has no edition ID")
		assert.NotNil(t, result, "result should not be nil")
		
		// The function should return the original book with no edition ID
		// since we didn't set up the search mock to be called
		assert.Equal(t, "123", result.ID, "book ID should be the same as the input")
		assert.Equal(t, "", result.EditionID, "edition ID should be empty since no edition was found")
		assert.Equal(t, "", result.UserBookID, "user book ID should be empty since no edition was found")

		// Verify mock expectations
		mockClient.AssertExpectations(t)
	})
}

func TestProcessFoundBook_NoEditionFound(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Create a test audiobook with ASIN and ISBN
	testAudiobook := createTestBook("test-book-1", "Test Book", "Test Author", "B08N5KWB9H", "9781234567890")
	testAudiobook.Media.Metadata.ASIN = "B08N5KWB9H"
	testAudiobook.Media.Metadata.ISBN = "9781234567890"
	audiobook := toAudiobookshelfBook(testAudiobook)

	// Create a test Hardcover book with an edition ID
	testHcBook := &TestHardcoverBook{
		ID:            "123",
		Title:         "Test Book",
		EditionID:     "456",
		ASIN:          "B08N5KWB9H",
		ISBN:          "9781234567890",
		EditionASIN:   "B08N5KWB9H",
		EditionISBN10: "1234567890",
		EditionISBN13: "9781234567890",
	}
	hcBook := toHardcoverBook(testHcBook)

	// Setup mock expectations
	editionErr := errors.New("edition not found")

	// Mock the GetEdition call to return not found - but this might not be called if the edition ID is already set
	editionID := "456"
	editionIDInt, _ := strconv.Atoi(editionID)
	nilEdition := (*models.Edition)(nil)
	// Make this expectation optional since processFoundBook might not call GetEdition if edition ID is already set
	mockClient.On("GetEdition", mock.Anything, editionID).Return(nilEdition, editionErr).Maybe()

	// Mock the CheckBookOwnership call to return false (not owned) using BOOK ID
	mockClient.On("CheckBookOwnership", mock.Anything, 123).Return(false, nil).Maybe()

	// Mock the MarkEditionAsOwned call since the book is not owned and sync_owned is true
	mockClient.On("MarkEditionAsOwned", mock.Anything, editionIDInt).Return(nil).Maybe()

	// Mock the GetUserBookID call to return a user book ID
	userBookID := 789
	mockClient.On("GetUserBookID", mock.Anything, editionIDInt).Return(userBookID, nil).Once()

	// Call the function
	result, err := svc.processFoundBook(context.Background(), hcBook, *audiobook)

	// Verify results
	assert.NoError(t, err, "Should not return an error when edition is not found but book is processed")
	require.NotNil(t, result, "Should return a result even when edition is not found")
	assert.Equal(t, "123", result.ID, "Result should have the correct book ID")
	assert.Equal(t, "Test Book", result.Title, "Result should have the correct title")
	
	// Use mock.Anything for the context parameter to make the test more flexible
	mockClient.AssertExpectations(t)
}

func TestProcessFoundBook_OwnershipSync_DryRun(t *testing.T) {
    // Config: SyncOwned enabled, DryRun enabled
    cfg := createTestConfig(true)
    cfg.Sync.DryRun = true

    // Mock client and service
    mockClient := new(MockHardcoverClient)
    svc := &Service{
        hardcover: mockClient,
        config:    cfg,
        log:       logger.Get(),
    }

    // Test data
    testHc := &TestHardcoverBook{ID: "123", EditionID: "456"}
    hcBook := toHardcoverBook(testHc)
    testAbs := createTestBook("test-book-1", "Test Book", "Test Author", "", "")
    absBook := toAudiobookshelfBook(testAbs)

    // Expectations: ownership checked by BOOK ID, but MarkEditionAsOwned must NOT be called due to DryRun
    mockClient.On("GetUserBookID", mock.Anything, 456).Return(789, nil).Once()
    mockClient.On("CheckBookOwnership", mock.Anything, 123).Return(false, nil).Once()

    // Execute
    result, err := svc.processFoundBook(context.Background(), hcBook, *absBook)

    // Verify
    assert.NoError(t, err)
    assert.NotNil(t, result)
    mockClient.AssertNotCalled(t, "MarkEditionAsOwned", mock.Anything, mock.AnythingOfType("int"))
    mockClient.AssertExpectations(t)
}
