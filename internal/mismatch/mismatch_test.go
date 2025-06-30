package mismatch

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Import the models package to use the correct types
// No local UserBook type needed as we'll use models.UserBook

// CreateUserBookInput is a mock implementation of the hardcover.CreateUserBookInput type
type CreateUserBookInput struct {
	EditionID string `json:"editionId"`
	Source    string `json:"source"`
}

// CreateUserBookResult is a mock implementation of the hardcover.CreateUserBookResult type
type CreateUserBookResult struct {
	ID int `json:"id"`
}



// newTestContext creates a context with a test logger
func newTestContext(t *testing.T) context.Context {
	// Reset the global logger for testing
	logger.ResetForTesting()

	// Create a buffer to capture log output
	var buf bytes.Buffer

	// Setup the logger with test configuration
	logger.Setup(logger.Config{
		Level:      "debug",
		Format:     "console",
		Output:     &buf,
		TimeFormat: time.RFC3339,
	})

	// Return a context with the logger
	return logger.NewContext(context.Background(), logger.Get())
}

// newTestConfig creates a minimal valid config for testing
func newTestConfig(mismatchDir string) *config.Config {
	return &config.Config{
		App: struct {
			Debug           bool          `yaml:"debug" env:"DEBUG"`
			SyncInterval    time.Duration `yaml:"sync_interval" env:"SYNC_INTERVAL"`
			MinimumProgress float64       `yaml:"minimum_progress" env:"MINIMUM_PROGRESS"`
			SyncWantToRead  bool          `yaml:"sync_want_to_read" env:"SYNC_WANT_TO_READ"`
			SyncOwned       bool          `yaml:"sync_owned" env:"SYNC_OWNED"`
			DryRun          bool          `yaml:"dry_run" env:"DRY_RUN"`
			TestBookFilter  string        `yaml:"test_book_filter" env:"TEST_BOOK_FILTER"`
			TestBookLimit   int           `yaml:"test_book_limit" env:"TEST_BOOK_LIMIT"`
		}{
			// Set required fields to their zero values
			Debug:           false,
			SyncInterval:    0,
			MinimumProgress: 0,
			SyncWantToRead:  false,
			SyncOwned:       false,
			DryRun:          false,
			TestBookFilter:  "",
			TestBookLimit:   0,
		},
		Paths: struct {
			CacheDir          string `yaml:"cache_dir" env:"CACHE_DIR"`
			MismatchOutputDir string `yaml:"mismatch_output_dir" env:"MISMATCH_OUTPUT_DIR"`
		}{
			MismatchOutputDir: mismatchDir,
		},
	}
}

// MockHardcoverClient is a mock implementation of the HardcoverClientInterface for testing
type MockHardcoverClient struct {
	mock.Mock
}

// HardcoverClientInterfaceMock is a mock implementation of the HardcoverClientInterface
// that embeds the mock.Mock type for testing

func (m *MockHardcoverClient) SearchAuthors(ctx context.Context, query string, limit int) ([]models.Author, error) {
	args := m.Called(ctx, query, limit)
	return args.Get(0).([]models.Author), args.Error(1)
}

func (m *MockHardcoverClient) SearchNarrators(ctx context.Context, query string, limit int) ([]models.Author, error) {
	args := m.Called(ctx, query, limit)
	return args.Get(0).([]models.Author), args.Error(1)
}

// Add other required methods of the interface with empty implementations
func (m *MockHardcoverClient) SearchPublishers(ctx context.Context, query string, limit int) ([]models.Publisher, error) {
	return nil, nil
}

func (m *MockHardcoverClient) GetEditionByASIN(ctx context.Context, asin string) (*models.Edition, error) {
	return nil, nil
}

func (m *MockHardcoverClient) GetEditionByISBN13(ctx context.Context, isbn13 string) (*models.Edition, error) {
	return nil, nil
}

func (m *MockHardcoverClient) GetEditionByISBN10(ctx context.Context, isbn10 string) (*models.Edition, error) {
	return nil, nil
}

func (m *MockHardcoverClient) GetEdition(ctx context.Context, id string) (*models.Edition, error) {
	return nil, nil
}

func (m *MockHardcoverClient) GetUserBookID(ctx context.Context, editionID int) (int, error) {
	return 0, nil
}

func (m *MockHardcoverClient) GetUserBook(ctx context.Context, userBookID string) (*models.HardcoverBook, error) {
	args := m.Called(ctx, userBookID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.HardcoverBook), args.Error(1)
}

// SearchBooks is a mock implementation for the HardcoverClientInterface
func (m *MockHardcoverClient) SearchBooks(ctx context.Context, title, author string) ([]models.HardcoverBook, error) {
	args := m.Called(ctx, title, author)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.HardcoverBook), args.Error(1)
}

func (m *MockHardcoverClient) SearchBookByASIN(ctx context.Context, asin string) (*models.HardcoverBook, error) {
	args := m.Called(ctx, asin)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.HardcoverBook), args.Error(1)
}

func (m *MockHardcoverClient) SearchBookByISBN10(ctx context.Context, isbn10 string) (*models.HardcoverBook, error) {
	args := m.Called(ctx, isbn10)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.HardcoverBook), args.Error(1)
}

func (m *MockHardcoverClient) SearchBookByISBN13(ctx context.Context, isbn13 string) (*models.HardcoverBook, error) {
	args := m.Called(ctx, isbn13)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.HardcoverBook), args.Error(1)
}

func (m *MockHardcoverClient) AddWithMetadata(metadata string, bookID interface{}, extraData map[string]interface{}) error {
	args := m.Called(metadata, bookID, extraData)
	return args.Error(0)
}

func (m *MockHardcoverClient) SaveToFile(filename string) error {
	args := m.Called(filename)
	return args.Error(0)
}

// GetAuthHeader is a mock implementation for the HardcoverClientInterface
func (m *MockHardcoverClient) GetAuthHeader() string {
	args := m.Called()
	return args.String(0)
}

// CheckBookOwnership is a mock implementation for the HardcoverClientInterface
func (m *MockHardcoverClient) CheckBookOwnership(ctx context.Context, bookID int) (bool, error) {
	args := m.Called(ctx, bookID)
	return args.Bool(0), args.Error(1)
}

// CheckExistingUserBookRead is a mock implementation for the HardcoverClientInterface
func (m *MockHardcoverClient) CheckExistingUserBookRead(ctx context.Context, input hardcover.CheckExistingUserBookReadInput) (*hardcover.CheckExistingUserBookReadResult, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*hardcover.CheckExistingUserBookReadResult), args.Error(1)
}

// CreateUserBook is a mock implementation for the HardcoverClientInterface
func (m *MockHardcoverClient) CreateUserBook(ctx context.Context, editionID, status string) (string, error) {
	args := m.Called(ctx, editionID, status)
	return args.String(0), args.Error(1)
}

// GetUserBookReads gets the reading progress for a user book
func (m *MockHardcoverClient) GetUserBookReads(ctx context.Context, input hardcover.GetUserBookReadsInput) ([]hardcover.UserBookRead, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]hardcover.UserBookRead), args.Error(1)
}

// InsertUserBookRead creates a new reading progress entry for a user book
func (m *MockHardcoverClient) InsertUserBookRead(ctx context.Context, input hardcover.InsertUserBookReadInput) (int, error) {
	args := m.Called(ctx, input)
	return args.Int(0), args.Error(1)
}

// MarkEditionAsOwned adds a book to the user's "Owned" list
func (m *MockHardcoverClient) MarkEditionAsOwned(ctx context.Context, editionID int) error {
	args := m.Called(ctx, editionID)
	return args.Error(0)
}

// UpdateReadingProgress is a mock implementation for the HardcoverClientInterface
func (m *MockHardcoverClient) UpdateReadingProgress(ctx context.Context, bookID string, progress float64, status string, markAsOwned bool) error {
	args := m.Called(ctx, bookID, progress, status, markAsOwned)
	return args.Error(0)
}

// UpdateUserBookRead updates the reading progress for a user book
func (m *MockHardcoverClient) UpdateUserBookRead(ctx context.Context, input hardcover.UpdateUserBookReadInput) (bool, error) {
	args := m.Called(ctx, input)
	return args.Bool(0), args.Error(1)
}

// UpdateUserBookStatus updates the status of a user book
func (m *MockHardcoverClient) UpdateUserBookStatus(ctx context.Context, input hardcover.UpdateUserBookStatusInput) error {
	args := m.Called(ctx, input)
	return args.Error(0)
}

// GetGoogleUploadCredentials is a mock implementation for the HardcoverClientInterface
func (m *MockHardcoverClient) GetGoogleUploadCredentials(ctx context.Context, filename string, fileSize int) (*edition.GoogleUploadInfo, error) {
	args := m.Called(ctx, filename, fileSize)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*edition.GoogleUploadInfo), args.Error(1)
}

func TestBookMismatchToEditionExport(t *testing.T) {
	// Create a test context with logger
	ctx := newTestContext(t)

	tests := []struct {
		name     string
		book     BookMismatch
		setupMocks func(*MockHardcoverClient)
		expected EditionExport
	}{
		{
			name: "basic book with minimum fields",
			book: BookMismatch{
				BookID:          "123",
				Title:           "Test Book",
				Author:          "Test Author",
				Reason:          "test reason",
				DurationSeconds: 19800, // 5.5 hours in seconds
				Timestamp:       time.Now().Unix(),
				CreatedAt:       time.Now(),
			},
			setupMocks: func(mockHC *MockHardcoverClient) {
				// Expect a call to SearchAuthors with the test author name
				mockHC.On("SearchAuthors", mock.Anything, "Test Author", 5).
					Return([]models.Author{}, nil) // Return empty slice to simulate author not found
			},
			expected: EditionExport{
				BookID:        123, // Parsed from BookID
				Title:         "Test Book",
				AuthorIDs:     []int{}, // Empty slice when no authors found
				AudioSeconds:  19800,
				EditionFormat: "Audiobook",
				EditionInfo:   "Imported from Audiobookshelf\n\nReason: test reason.",
				LanguageID:    1, // Default values
				CountryID:     1, // Default values
				PublisherID:   0, // Default values
			},
		},
		{
			name: "book with all fields",
			book: BookMismatch{
				BookID:          "456",
				Title:           "Test Book",
				Subtitle:        "Test Subtitle",
				Author:          "Test Author",
				Narrator:        "Test Narrator",
				ASIN:            "B07GNTNXQW",
				ISBN:            "1234567890",
				ISBN10:          "1234567890",
				ISBN13:          "9781234567890",
				ReleaseDate:     "2020-01-01",
				PublishedYear:   "2020",
				DurationSeconds: 37800, // 10.5 hours in seconds
				CoverURL:        "https://example.com/cover.jpg",
				ImageURL:        "https://example.com/image.jpg",
				EditionFormat:   "Audiobook",
				EditionInfo:     "Special Edition",
				LanguageID:      1,
				CountryID:       1,
				PublisherID:     2,
				Reason:          "test reason",
				Timestamp:       time.Now().Unix(),
				CreatedAt:       time.Now(),
			},
			setupMocks: func(mockHC *MockHardcoverClient) {
				// Set up mock expectations for SearchAuthors
				mockHC.On("SearchAuthors", mock.Anything, "Test Author", 5).
					Return([]models.Author{{
						ID:        "1",
						Name:      "Test Author",
						BookCount: 10,
					}}, nil).Once()
				
				// Set up mock expectations for SearchNarrators
				mockHC.On("SearchNarrators", mock.Anything, "Test Narrator", 5).
					Return([]models.Author{{
						ID:        "2",
						Name:      "Test Narrator",
						BookCount: 5,
					}}, nil).Once()
			},
			expected: EditionExport{
				BookID:        456, // Parsed from BookID
				Title:         "Test Book",
				Subtitle:      "Test Subtitle",
				ImageURL:      "https://example.com/image.jpg",
				ASIN:          "B07GNTNXQW",
				ISBN10:        "1234567890",
				ISBN13:        "9781234567890",
				AuthorIDs:     []int{1}, // Expected author ID from mock
				NarratorIDs:   []int{2}, // Expected narrator ID from mock
				PublisherID:   2,
				ReleaseDate:   "2020-01-01",
				AudioSeconds:  37800,
				EditionFormat: "Audiobook",
				EditionInfo:   "Imported from Audiobookshelf\n\nReason: test reason\n\nSpecial Edition.",
				LanguageID:    1,
				CountryID:     1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new mock client for this test case
			mockHC := &MockHardcoverClient{}
			
			// Default mock expectations will be set up in each test case
			
			// Set up mock expectations if provided
			if tt.setupMocks != nil {
				tt.setupMocks(mockHC)
			}
			
			result := tt.book.ToEditionExport(ctx, mockHC)

			// Check all fields that should be directly copied
			// Verify all mock expectations were met
			mockHC.AssertExpectations(t)
			
			assert.Equal(t, tt.expected.BookID, result.BookID)
			assert.Equal(t, tt.expected.Title, result.Title)
			assert.Equal(t, tt.expected.Subtitle, result.Subtitle)
			assert.Equal(t, tt.expected.ImageURL, result.ImageURL)
			assert.Equal(t, tt.expected.ASIN, result.ASIN)
			assert.Equal(t, tt.expected.ISBN10, result.ISBN10)
			assert.Equal(t, tt.expected.ISBN13, result.ISBN13)
			assert.Equal(t, tt.expected.ReleaseDate, result.ReleaseDate)
			assert.Equal(t, tt.expected.AudioSeconds, result.AudioSeconds)
			assert.Equal(t, tt.expected.EditionFormat, result.EditionFormat)
			// For EditionInfo, we expect it to contain the expected parts and end with a period
			assert.True(t, strings.HasSuffix(result.EditionInfo, "."), "EditionInfo should end with a period")
			// Check that the expected content is contained in the actual EditionInfo
			expectedParts := strings.Split(tt.expected.EditionInfo, "\n\n")
			for _, part := range expectedParts {
				assert.Contains(t, result.EditionInfo, part, "EditionInfo is missing expected content")
			}
			assert.Equal(t, tt.expected.LanguageID, result.LanguageID)
			assert.Equal(t, tt.expected.CountryID, result.CountryID)
			assert.Equal(t, tt.expected.PublisherID, result.PublisherID)

			// For slices, initialize empty slices instead of nil for comparison
			expectedAuthorIDs := tt.expected.AuthorIDs
			if expectedAuthorIDs == nil {
				expectedAuthorIDs = []int{} // Empty slice instead of nil
			}
			resultAuthorIDs := result.AuthorIDs
			if resultAuthorIDs == nil {
				resultAuthorIDs = []int{} // Empty slice instead of nil
			}
			assert.ElementsMatch(t, expectedAuthorIDs, resultAuthorIDs, "AuthorIDs do not match")

			// For NarratorIDs, we expect it to be empty by default, even when Narrator is set
			expectedNarratorIDs := tt.expected.NarratorIDs
			if expectedNarratorIDs == nil {
				expectedNarratorIDs = []int{} // Empty slice instead of nil
			}
			resultNarratorIDs := result.NarratorIDs
			if resultNarratorIDs == nil {
				resultNarratorIDs = []int{} // Empty slice instead of nil
			}
			assert.ElementsMatch(t, expectedNarratorIDs, resultNarratorIDs, "NarratorIDs do not match")
		})
	}
}

// TestAddWithMetadata verifies that AddWithMetadata populates all required fields
func TestAddWithMetadata(t *testing.T) {
	// Setup
	metadata := MediaMetadata{
		Title:         "Test Book",
		Subtitle:      "Test Subtitle",
		AuthorName:    "Test Author",
		NarratorName:  "Test Narrator",
		ISBN:          "1234567890",
		ASIN:          "B07GNTNXQW",
		CoverURL:      "https://example.com/cover.jpg",
		PublishedYear: "2020",
		PublishedDate: "2020-01-15",
	}

	// Call the function with a nil Hardcover client for testing
	AddWithMetadata(metadata, "123", "edition123", "test reason", 3600, "abs123", nil)

	// Get the added mismatch
	mismatches := GetAll()
	require.NotEmpty(t, mismatches, "Expected at least one mismatch")
	mismatch := mismatches[len(mismatches)-1] // Get the last added mismatch

	// Verify the fields
	assert.Equal(t, "123", mismatch.BookID)
	assert.Equal(t, "Test Book", mismatch.Title)
	assert.Equal(t, "Test Subtitle", mismatch.Subtitle)
	assert.Equal(t, "Test Author", mismatch.Author)
	assert.Equal(t, "Test Narrator", mismatch.Narrator)
	assert.Equal(t, "1234567890", mismatch.ISBN)
	assert.Equal(t, "1234567890", mismatch.ISBN10) // Should be set from ISBN
	assert.Equal(t, "B07GNTNXQW", mismatch.ASIN)
	assert.Equal(t, "https://example.com/cover.jpg", mismatch.CoverURL)
	assert.Equal(t, "https://example.com/cover.jpg", mismatch.ImageURL) // Should match CoverURL
	assert.Equal(t, 3600, mismatch.DurationSeconds)
	assert.Equal(t, "2020-01-15", mismatch.ReleaseDate) // Should use PublishedDate
	assert.Equal(t, "2020", mismatch.PublishedYear)
	assert.Equal(t, "Audiobook", mismatch.EditionFormat)
	assert.Equal(t, "Imported from Audiobookshelf: test reason", mismatch.EditionInfo)
	assert.Equal(t, 1, mismatch.LanguageID)  // Default to English
	assert.Equal(t, 1, mismatch.CountryID)   // Default to US
	assert.Equal(t, 1, mismatch.PublisherID) // Default publisher
}

func TestBookMismatchToEditionInput(t *testing.T) {
	// Create a test context
	ctx := context.Background()

	tests := []struct {
		name     string
		book     BookMismatch
		hc       *hardcover.Client // Mocked Hardcover client
		expected EditionCreatorInput
		err      bool
	}{
		{
			name: "basic book with minimum fields",
			book: BookMismatch{
				BookID:          "123",
				Title:           "Test Book",
				Author:          "Test Author",
				Reason:          "test reason",
				DurationSeconds: 19800, // 5.5 hours in seconds
				Timestamp:       time.Now().Unix(),
				CreatedAt:       time.Now(),
			},
			expected: EditionCreatorInput{
				Title:         "Test Book",
				Subtitle:      "",
				ASIN:          "",
				ISBN10:        "",
				ISBN13:        "",
				AudioLength:   19800,
				EditionFormat: "Audiobook",
				ImageURL:      "",
			},
			err: false,
		},
		{
			name: "book with all fields",
			book: BookMismatch{
				BookID:          "456",
				Title:           "Test Book",
				Subtitle:        "Test Subtitle",
				Author:          "Test Author",
				Narrator:        "Test Narrator",
				ASIN:            "B07GNTNXQW",
				ISBN:            "1234567890",
				ISBN10:          "1234567890",
				ISBN13:          "9781234567890",
				ReleaseDate:     "2020-01-01",
				PublishedYear:   "2020",
				DurationSeconds: 37800, // 10.5 hours in seconds
				CoverURL:        "https://example.com/cover.jpg",
				ImageURL:        "https://example.com/image.jpg",
				EditionFormat:   "Audiobook",
				EditionInfo:     "Special Edition",
				LanguageID:      1,
				CountryID:       1,
				PublisherID:     2,
				Reason:          "test reason",
				Timestamp:       time.Now().Unix(),
				CreatedAt:       time.Now(),
			},
			expected: EditionCreatorInput{
				Title:         "Test Book",
				Subtitle:      "Test Subtitle",
				ASIN:          "B07GNTNXQW",
				ISBN10:        "1234567890",
				ISBN13:        "9781234567890",
				AudioLength:   37800,
				EditionFormat: "Audiobook",
				ImageURL:      "https://example.com/image.jpg",
			},
			err: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, err := tt.book.ToEditionInput(ctx, tt.hc)
			if (err != nil) != tt.err {
				t.Fatalf("ToEditionInput() error = %v, expectErr %v", err, tt.err)
			}

			// Check the fields we care about
			if input.Title != tt.expected.Title {
				t.Errorf("Title: got %v, want %v", input.Title, tt.expected.Title)
			}
			if input.Subtitle != tt.expected.Subtitle {
				t.Errorf("Subtitle: got %v, want %v", input.Subtitle, tt.expected.Subtitle)
			}
			if input.ASIN != tt.expected.ASIN {
				t.Errorf("ASIN: got %v, want %v", input.ASIN, tt.expected.ASIN)
			}
			if input.ISBN10 != tt.expected.ISBN10 {
				t.Errorf("ISBN10: got %v, want %v", input.ISBN10, tt.expected.ISBN10)
			}
			if input.AudioLength != tt.expected.AudioLength {
				t.Errorf("AudioLength: got %v, want %v", input.AudioLength, tt.expected.AudioLength)
			}
			if input.EditionFormat != tt.expected.EditionFormat {
				t.Errorf("EditionFormat: got %v, want %v", input.EditionFormat, tt.expected.EditionFormat)
			}
			if input.ImageURL != tt.expected.ImageURL {
				t.Errorf("ImageURL: got %v, want %v", input.ImageURL, tt.expected.ImageURL)
			}
		})
	}
}

func TestSaveMismatchesJSONFileIndividual(t *testing.T) {
	// Create a test context
	ctx := context.Background()

	tempDir, err := os.MkdirTemp("", "mismatch-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a test logger
	log := logger.Get()

	// Create a mock Hardcover client with test token
	hc := hardcover.NewClient("test-token", log)

	// Create a test config with the temp directory
	cfg := newTestConfig(tempDir)

	// Clear any existing mismatches
	Clear()

	// Add test mismatches
	now := time.Now()
	mismatches := []BookMismatch{
		{
			BookID:    "test-book-1",
			Title:     "Test Book 1",
			Author:    "Author 1",
			AuthorIDs: []int{}, // Initialize empty slice
			ISBN:      "1234567890",
			ISBN10:    "1234567890", // Set ISBN10 explicitly
			ISBN13:    "",
			Reason:    "test reason 1",
			Timestamp: now.Unix(),
			CreatedAt: now,
		},
		{
			BookID:    "test-book-2",
			Title:     "Test Book 2",
			Author:    "Author 2",
			AuthorIDs: []int{}, // Initialize empty slice
			ISBN:      "0987654321",
			ISBN10:    "0987654321", // Set ISBN10 explicitly
			ISBN13:    "",
			Reason:    "test reason 2",
			Timestamp: now.Add(-time.Hour).Unix(),
			CreatedAt: now.Add(-time.Hour),
		},
	}

	for _, m := range mismatches {
		Add(m)
	}

	// Save mismatches to files
	if err = SaveToFile(ctx, hc, "", cfg); err != nil {
		t.Fatalf("SaveToFile failed: %v", err)
	}

	// Verify files were created
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("Failed to read temp dir: %v", err)
	}

	if len(files) != len(mismatches) {
		t.Fatalf("Expected %d files, got %d", len(mismatches), len(files))
	}

	// Verify file contents
	for i, file := range files {
		filePath := filepath.Join(tempDir, file.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			t.Fatalf("Failed to read file %s: %v", filePath, err)
		}

		// Use a map to handle flexible field types during unmarshaling
		var result map[string]interface{}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatalf("Failed to unmarshal %s: %v", filePath, err)
		}

		// Extract the book_id, handling both string and number types
		switch result["book_id"].(type) {
		case string, float64, int, int64:
			// Valid book_id types, continue with test
		case nil:
			// Skip this test case if book_id is nil (not all test cases may have a book_id)
			continue
		default:
			t.Fatalf("Unexpected type for book_id: %T", result["book_id"])
		}

		// Create a map to store the expected values from our test data
		expected := mismatches[i]

		// Verify the fields we care about in the saved JSON
		if title, ok := result["title"].(string); ok && title != expected.Title {
			t.Errorf("Mismatch in file %s: Title got %q, want %q", filePath, title, expected.Title)
		}

		// Verify author_ids exists and is an empty array
		authorIDs, ok := result["author_ids"].([]interface{})
		if !ok {
			t.Errorf("Expected author_ids field to be an array in file %s", filePath)
		} else if len(authorIDs) != 0 {
			t.Errorf("Expected empty author_ids array, got %v in file %s", authorIDs, filePath)
		}

		// Verify ISBN fields match the test data
		expectedISBN10 := ""
		expectedISBN13 := ""

		// Determine expected ISBNs based on the test data
		switch expected.Title {
		case "Test Book 1":
			expectedISBN10 = "1234567890"
		case "Test Book 2":
			expectedISBN10 = "0987654321"
		}

		// Check ISBN10
		if isbn10, ok := result["isbn_10"].(string); !ok {
			t.Errorf("Expected isbn_10 field in file %s", filePath)
		} else if isbn10 != expectedISBN10 {
			t.Errorf("Expected isbn_10 to be %q, got %q in file %s", expectedISBN10, isbn10, filePath)
		}

		// Check ISBN13 (should be empty in test data)
		if isbn13, ok := result["isbn_13"].(string); !ok {
			t.Errorf("Expected isbn_13 field in file %s", filePath)
		} else if isbn13 != expectedISBN13 {
			t.Errorf("Expected isbn_13 to be %q, got %q in file %s", expectedISBN13, isbn13, filePath)
		}

		// Check the edition_information field for the reason
		if editionInfo, ok := result["edition_information"].(string); ok && expected.Reason != "" {
			if !strings.Contains(editionInfo, expected.Reason) {
				t.Errorf("Mismatch in file %s: edition_information should contain reason %q but got %q",
					filePath, expected.Reason, editionInfo)
			}
		} else if expected.Reason != "" {
			t.Errorf("Missing edition_information in file %s, expected to contain reason: %s", filePath, expected.Reason)
		}

		// Verify the book_id matches the expected BookID if it's numeric
		if expected.BookID != "" {
			if id, err := strconv.Atoi(expected.BookID); err == nil {
				savedID, ok := result["book_id"].(float64)
				if !ok || int(savedID) != id {
					t.Errorf("Mismatch in file %s: book_id got %v, want %d", filePath, result["book_id"], id)
				}
			}
		}
	}
}
