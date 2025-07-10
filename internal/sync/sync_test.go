package sync

import (
	"context"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// AudiobookshelfClientInterface defines the interface for audiobookshelf.Client
// to allow for mocking in tests
type AudiobookshelfClientInterface interface {
	GetLibraries(ctx context.Context) ([]audiobookshelf.AudiobookshelfLibrary, error)
	GetLibraryItems(ctx context.Context, libraryID string) ([]models.AudiobookshelfBook, error)
	GetUserProgress(ctx context.Context) (*models.AudiobookshelfUserProgress, error)
	GetListeningSessions(ctx context.Context, since time.Time) ([]models.AudiobookshelfBook, error)
}

// MockAudiobookshelfClient is a mock implementation of the AudiobookshelfClientInterface
type MockAudiobookshelfClient struct {
	mock.Mock
}

// GetLibraries mocks the GetLibraries method
func (m *MockAudiobookshelfClient) GetLibraries(ctx context.Context) ([]audiobookshelf.AudiobookshelfLibrary, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]audiobookshelf.AudiobookshelfLibrary), args.Error(1)
}

// GetLibraryItems mocks the GetLibraryItems method
func (m *MockAudiobookshelfClient) GetLibraryItems(ctx context.Context, libraryID string) ([]models.AudiobookshelfBook, error) {
	args := m.Called(ctx, libraryID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.AudiobookshelfBook), args.Error(1)
}

// GetUserProgress mocks the GetUserProgress method
func (m *MockAudiobookshelfClient) GetUserProgress(ctx context.Context) (*models.AudiobookshelfUserProgress, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.AudiobookshelfUserProgress), args.Error(1)
}

// GetListeningSessions mocks the GetListeningSessions method
func (m *MockAudiobookshelfClient) GetListeningSessions(ctx context.Context, since time.Time) ([]models.AudiobookshelfBook, error) {
	args := m.Called(ctx, since)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.AudiobookshelfBook), args.Error(1)
}

// TestSync tests the Sync function
func TestSync(t *testing.T) {
	// Skip this test for now until all struct issues are fixed
	// Previously skipped until struct issues were fixed

	// Setup logger with test config
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})
	log := logger.Get()

	// Setup mocks
	mockABS := new(MockAudiobookshelfClient)
	mockHC := new(MockHardcoverClient)
	
	// Create a temporary state file
	testState := state.NewState()
	
	// Create test config
	testConfig := &config.Config{
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
			DryRun:           true,
			TestBookFilter:   "",
			TestBookLimit:    0,
			MinimumProgress:  0.05,
			SyncWantToRead:   true,
			SyncOwned:        true,
		},
		Sync: struct {
		Incremental       bool   `yaml:"incremental" env:"SYNC_INCREMENTAL"`
		StateFile         string `yaml:"state_file" env:"SYNC_STATE_FILE"`
		MinChangeThreshold int    `yaml:"min_change_threshold" env:"SYNC_MIN_CHANGE_THRESHOLD"`
	}{
			Incremental: false,
		},
		Audiobookshelf: struct {
		URL   string `yaml:"url" env:"AUDIOBOOKSHELF_URL"`
		Token string `yaml:"token" env:"AUDIOBOOKSHELF_TOKEN"`
	}{
			URL:   "https://abs.example.com",
			Token: "test-token",
		},
		Hardcover: struct {
		Token string `yaml:"token" env:"HARDCOVER_TOKEN"`
	}{
			Token: "test-token",
		},
	}

	// Create the service with mocked clients
	svc := &Service{
		audiobookshelf: nil, // Will be replaced with mock
		hardcover:      mockHC,
		config:         testConfig,
		log:            log,
		state:          testState,
		statePath:      "",
		lastProgressUpdates: make(map[string]progressUpdateInfo),
	}

	// We need to use a reflection trick to inject our mock into the service
	// since audiobookshelf.Client is a concrete type in the Service struct
	// For testing purposes, we'll create a function to inject the mock
	
	// Create test library for reference
	testLibrary := &audiobookshelf.AudiobookshelfLibrary{
		ID:   "lib1",
		Name: "Test Library",
	}
	
	// Create test user progress
	testUserProgress := &models.AudiobookshelfUserProgress{
		ID: "user1",
		Username: "testuser",
		MediaProgress: []struct {
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
		}{},
		ListeningSessions: []struct {
			ID            string `json:"id"`
			UserID        string `json:"userId"`
			LibraryItemID string `json:"libraryItemId"`
			MediaType     string `json:"mediaType"`
			MediaMetadata struct {
				Title  string `json:"title"`
				Author string `json:"author"`
			} `json:"mediaMetadata"`
			Duration    float64 `json:"duration"`
			CurrentTime float64 `json:"currentTime"`
			Progress    float64 `json:"progress"`
			IsFinished  bool    `json:"isFinished"`
			StartedAt   int64   `json:"startedAt"`
			UpdatedAt   int64   `json:"updatedAt"`
		}{},
	}
	
	// Create empty library items list
	emptyLibraryItems := []models.AudiobookshelfBook{}
	
	// Setup mock expectations - only include what's used in the processLibrary test
	mockABS.On("GetLibraryItems", mock.Anything, "lib1").Return(emptyLibraryItems, nil)
	
	// Replace the audiobookshelf client in the service with our mock
	svc.audiobookshelf = mockABS
	
	// For test purposes, we'll test processLibrary directly
	t.Run("Empty library - should complete without errors", func(t *testing.T) {
		// Create a context with a timeout
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		
		// Call processLibrary directly
		processed, err := svc.processLibrary(ctx, testLibrary, 0, testUserProgress)
		
		// Verify results
		assert.NoError(t, err)
		assert.Equal(t, 0, processed)
		
		// Verify mock expectations
		mockABS.AssertExpectations(t)
	})
}

// TestProcessLibrary tests the processLibrary function
func TestProcessLibrary(t *testing.T) {
	// Skip this test for now until all struct issues are fixed
	// Previously skipped until struct issues were fixed

	// Setup logger with test config
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})
	log := logger.Get()

	// Setup mocks
	mockABS := new(MockAudiobookshelfClient)
	mockHC := new(MockHardcoverClient)
	
	// Create a temporary state file
	testState := state.NewState()
	
	// Create test config
	testConfig := &config.Config{
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
			DryRun:           true,
			TestBookFilter:   "",
			TestBookLimit:    0,
			MinimumProgress:  0.05,
			SyncWantToRead:   true,
			SyncOwned:        true,
		},
		Sync: struct {
		Incremental       bool   `yaml:"incremental" env:"SYNC_INCREMENTAL"`
		StateFile         string `yaml:"state_file" env:"SYNC_STATE_FILE"`
		MinChangeThreshold int    `yaml:"min_change_threshold" env:"SYNC_MIN_CHANGE_THRESHOLD"`
	}{
			Incremental: false,
		},
		Audiobookshelf: struct {
		URL   string `yaml:"url" env:"AUDIOBOOKSHELF_URL"`
		Token string `yaml:"token" env:"AUDIOBOOKSHELF_TOKEN"`
	}{
			URL:   "https://abs.example.com",
			Token: "test-token",
		},
		Hardcover: struct {
		Token string `yaml:"token" env:"HARDCOVER_TOKEN"`
	}{
			Token: "test-token",
		},
	}

	// Create the service with mocked clients
	svc := &Service{
		audiobookshelf: nil, // Will be replaced with mock
		hardcover:      mockHC,
		config:         testConfig,
		log:            log,
		state:          testState,
		statePath:      "",
		lastProgressUpdates: make(map[string]progressUpdateInfo),
	}
	
	// Create test library
	testLibrary := &audiobookshelf.AudiobookshelfLibrary{
		ID:   "lib1",
		Name: "Test Library",
	}
	
	// Create test user progress
	testUserProgress := &models.AudiobookshelfUserProgress{
		ID: "user1",
		Username: "testuser",
		MediaProgress: []struct {
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
		}{},
		ListeningSessions: []struct {
			ID            string `json:"id"`
			UserID        string `json:"userId"`
			LibraryItemID string `json:"libraryItemId"`
			MediaType     string `json:"mediaType"`
			MediaMetadata struct {
				Title  string `json:"title"`
				Author string `json:"author"`
			} `json:"mediaMetadata"`
			Duration    float64 `json:"duration"`
			CurrentTime float64 `json:"currentTime"`
			Progress    float64 `json:"progress"`
			IsFinished  bool    `json:"isFinished"`
			StartedAt   int64   `json:"startedAt"`
			UpdatedAt   int64   `json:"updatedAt"`
		}{},
	}
	
	// Test with an empty library
	t.Run("Empty library", func(t *testing.T) {
		// Create empty library items list
		emptyLibraryItems := []models.AudiobookshelfBook{}
		
		// Setup mock expectations
		mockABS.On("GetLibraryItems", mock.Anything, "lib1").Return(emptyLibraryItems, nil).Once()
		
		// Replace the audiobookshelf client in the service with our mock
		svc.audiobookshelf = mockABS
		
		// Call processLibrary
		processed, err := svc.processLibrary(context.Background(), testLibrary, 0, testUserProgress)
		
		// Verify results
		assert.NoError(t, err)
		assert.Equal(t, 0, processed)
		
		// Verify mock expectations
		mockABS.AssertExpectations(t)
	})
	
	// Test with a non-empty library but with a limit
	t.Run("Library with items and limit", func(t *testing.T) {
		// Create test books
		testBooks := []models.AudiobookshelfBook{
			{
				ID: "book1",
				Media: struct {
					ID       string                      `json:"id"`
					Metadata models.AudiobookshelfMetadataStruct `json:"metadata"`
					CoverPath string                     `json:"coverPath"`
					Duration  float64                    `json:"duration"`
				}{
					ID: "media1",
					Metadata: models.AudiobookshelfMetadataStruct{
						Title:      "Test Book 1",
						AuthorName: "Test Author 1",
						ASIN:       "B123456789",
						ISBN:       "9781234567890",
					},
					CoverPath: "/covers/test.jpg",
					Duration: 3600,
				},
				Progress: struct {
					CurrentTime float64 `json:"currentTime"`
					IsFinished  bool    `json:"isFinished"`
					StartedAt   int64   `json:"startedAt"`
					FinishedAt  int64   `json:"finishedAt"`
				}{
					IsFinished: false,
					CurrentTime: 1800,
					StartedAt:  time.Now().Add(-24 * time.Hour).Unix(),
					FinishedAt:  0,
				},
			},
			{
				ID: "book2",
				Media: struct {
					ID       string                      `json:"id"`
					Metadata models.AudiobookshelfMetadataStruct `json:"metadata"`
					CoverPath string                     `json:"coverPath"`
					Duration  float64                    `json:"duration"`
				}{
					ID: "media2",
					Metadata: models.AudiobookshelfMetadataStruct{
						Title:      "Test Book 2",
						AuthorName: "Test Author 2",
						ASIN:       "B987654321",
						ISBN:       "9780987654321",
					},
					CoverPath: "/covers/test2.jpg",
					Duration: 7200,
				},
				Progress: struct {
					CurrentTime float64 `json:"currentTime"`
					IsFinished  bool    `json:"isFinished"`
					StartedAt   int64   `json:"startedAt"`
					FinishedAt  int64   `json:"finishedAt"`
				}{
					IsFinished:  true,
					CurrentTime: 7200,
					StartedAt:   time.Now().Add(-48 * time.Hour).Unix(),
					FinishedAt:  time.Now().Unix(),
				},
			},
		}
		
		// Setup mock expectations
		mockABS.On("GetLibraryItems", mock.Anything, "lib1").Return(testBooks, nil).Once()
		
		// Mock processBook to always succeed for this test
		// Instead of actually processing books, we'll just count calls
		
		// Add mock expectation for SearchBookByASIN
		mockHC.On("SearchBookByASIN", mock.Anything, "B123456789").Return(&models.HardcoverBook{}, nil)
		
		// Set audiobookshelf client to our mock
		svc.audiobookshelf = mockABS
		
		// Call processLibrary with limit of 1
		processed, err := svc.processLibrary(context.Background(), testLibrary, 1, testUserProgress)
		
		// Verify results
		assert.NoError(t, err)
		assert.Equal(t, 1, processed, "Should process one book with the limit of 1")
		
		// Verify mock expectations
		mockABS.AssertExpectations(t)
	})
}

// TestProcessBook tests the processBook function
func TestProcessBook(t *testing.T) {
	// Skip this test for now until all struct issues are fixed
	t.Skip("Skipping test until struct issues are fixed")

	// Setup logger with test config
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})
	log := logger.Get()

	// Setup mocks
	mockABS := new(MockAudiobookshelfClient)
	mockHC := new(MockHardcoverClient)
	
	// Create a temporary state file
	testState := state.NewState()
	
	// Create test config
	testConfig := &config.Config{
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
			DryRun:           true,
			TestBookFilter:   "",
			TestBookLimit:    0,
			MinimumProgress:  0.05,
			SyncWantToRead:   true,
			SyncOwned:        true,
		},
		Sync: struct {
		Incremental       bool   `yaml:"incremental" env:"SYNC_INCREMENTAL"`
		StateFile         string `yaml:"state_file" env:"SYNC_STATE_FILE"`
		MinChangeThreshold int    `yaml:"min_change_threshold" env:"SYNC_MIN_CHANGE_THRESHOLD"`
	}{
			Incremental: false,
		},
		Audiobookshelf: struct {
		URL   string `yaml:"url" env:"AUDIOBOOKSHELF_URL"`
		Token string `yaml:"token" env:"AUDIOBOOKSHELF_TOKEN"`
	}{
			URL:   "https://abs.example.com",
			Token: "test-token",
		},
		Hardcover: struct {
		Token string `yaml:"token" env:"HARDCOVER_TOKEN"`
	}{
			Token: "test-token",
		},
	}

	// Create the service with mocked clients
	svc := &Service{
		audiobookshelf: mockABS,
		hardcover:      mockHC,
		config:         testConfig,
		log:            log,
		state:          testState,
		statePath:      "",
		lastProgressUpdates: make(map[string]progressUpdateInfo),
	}
	
	// Create test user progress
	testUserProgress := &models.AudiobookshelfUserProgress{
		ID: "user1",
		Username: "testuser",
		MediaProgress: []struct {
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
		}{},
		ListeningSessions: []struct {
			ID            string `json:"id"`
			UserID        string `json:"userId"`
			LibraryItemID string `json:"libraryItemId"`
			MediaType     string `json:"mediaType"`
			MediaMetadata struct {
				Title  string `json:"title"`
				Author string `json:"author"`
			} `json:"mediaMetadata"`
			Duration    float64 `json:"duration"`
			CurrentTime float64 `json:"currentTime"`
			Progress    float64 `json:"progress"`
			IsFinished  bool    `json:"isFinished"`
			StartedAt   int64   `json:"startedAt"`
			UpdatedAt   int64   `json:"updatedAt"`
		}{},
	}
	
	// Create test book
	testBook := models.AudiobookshelfBook{
		ID: "book1",
		Media: struct {
			ID       string                      `json:"id"`
			Metadata models.AudiobookshelfMetadataStruct `json:"metadata"`
			CoverPath string                     `json:"coverPath"`
			Duration  float64                    `json:"duration"`
		}{
			ID: "media1",
			Metadata: models.AudiobookshelfMetadataStruct{
				Title:      "Test Book 1",
				AuthorName: "Test Author 1",
				ASIN:       "B123456789",
				ISBN:       "9781234567890",
			},
			CoverPath: "/covers/test.jpg",
			Duration: 3600,
		},
		Progress: struct {
			CurrentTime float64 `json:"currentTime"`
			IsFinished  bool    `json:"isFinished"`
			StartedAt   int64   `json:"startedAt"`
			FinishedAt  int64   `json:"finishedAt"`
		}{
			IsFinished:  false,
			CurrentTime: 1800,
			StartedAt:   time.Now().Add(-24 * time.Hour).Unix(),
			FinishedAt:  0,
		},
	}
	
	// Test book with incremental sync (book unchanged)
	t.Run("Incremental sync - unchanged book", func(t *testing.T) {
		// Update config to use incremental sync
		svc.config.Sync.Incremental = true
		
		// Set up book state with a recent last update
		// Make last update time in the future to ensure it's more recent than the book's activity
		futureTimestamp := time.Now().Add(1 * time.Hour).Unix()
		testState.Books = map[string]state.Book{
			"book1": {
				LastUpdated:  futureTimestamp,
				LastProgress: 0.5,
				Status:       "IN_PROGRESS",
			},
		}
		
		// Call processBook - it should skip the book
		err := svc.processBook(context.Background(), testBook, testUserProgress)
		
		// Verify results - should be skipped due to no changes
		assert.NoError(t, err)
		
		// Reset for next test
		svc.config.Sync.Incremental = false
	})
	
	// Test book with test filter that doesn't match
	t.Run("Test filter - no match", func(t *testing.T) {
		// Set test filter
		svc.config.App.TestBookFilter = "NonMatchingFilter"
		
		// Call processBook - it should skip the book
		err := svc.processBook(context.Background(), testBook, testUserProgress)
		
		// Verify results - should be skipped due to filter not matching
		assert.NoError(t, err)
		
		// Reset for next test
		svc.config.App.TestBookFilter = ""
	})
}

// TestFindBookInHardcover tests the findBookInHardcover function
func TestFindBookInHardcover(t *testing.T) {
	// Setup logger with test config
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})
	log := logger.Get()

	// Setup mocks
	mockHC := new(MockHardcoverClient)
	
	// Create a temporary state file
	testState := state.NewState()
	
	// Create test config
	testConfig := &config.Config{
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
			DryRun:           true,
			TestBookFilter:   "",
			TestBookLimit:    0,
			MinimumProgress:  0.05,
			SyncWantToRead:   true,
			SyncOwned:        true,
		},
		Sync: struct {
		Incremental       bool   `yaml:"incremental" env:"SYNC_INCREMENTAL"`
		StateFile         string `yaml:"state_file" env:"SYNC_STATE_FILE"`
		MinChangeThreshold int    `yaml:"min_change_threshold" env:"SYNC_MIN_CHANGE_THRESHOLD"`
	}{
			Incremental: false,
		},
		Audiobookshelf: struct {
		URL   string `yaml:"url" env:"AUDIOBOOKSHELF_URL"`
		Token string `yaml:"token" env:"AUDIOBOOKSHELF_TOKEN"`
	}{
			URL:   "https://abs.example.com",
			Token: "test-token",
		},
		Hardcover: struct {
		Token string `yaml:"token" env:"HARDCOVER_TOKEN"`
	}{
			Token: "test-token",
		},
	}

	// Create the service with mocked clients
	svc := &Service{
		audiobookshelf: nil, // Not needed for this test
		hardcover:      mockHC,
		config:         testConfig,
		log:            log,
		state:          testState,
		statePath:      "",
		lastProgressUpdates: make(map[string]progressUpdateInfo),
	}
	
	// Create test book with ASIN
	testBookWithASIN := models.AudiobookshelfBook{
		ID: "book1",
		Media: struct {
			ID       string                        `json:"id"`
			Metadata models.AudiobookshelfMetadataStruct `json:"metadata"`
			CoverPath string                       `json:"coverPath"`
			Duration  float64                      `json:"duration"`
		}{
			ID: "media1",
			Metadata: models.AudiobookshelfMetadataStruct{
				Title:      "Test Book 1",
				AuthorName: "Test Author 1",
				ASIN:       "B123456789",
				ISBN:       "",
			},
			Duration: 3600,
		},
	}
	
	// Create test Hardcover book to return from search
	testHardcoverBook := &models.HardcoverBook{
		ID:        "hc123",
		Title:     "Test Book 1",
		EditionID: "456",
	}
	
	// Test search by ASIN
	t.Run("Find by ASIN", func(t *testing.T) {
		// Setup mock expectations for ASIN search
		mockHC.On("SearchBookByASIN", mock.Anything, "B123456789").Return(testHardcoverBook, nil).Once()
		mockHC.On("GetUserBookID", mock.Anything, 456).Return(789, nil).Once()
		
		// Call findBookInHardcover
		result, err := svc.findBookInHardcover(context.Background(), testBookWithASIN)
		
		// Verify results
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "hc123", result.ID)
		
		// Verify mock expectations
		mockHC.AssertExpectations(t)
	})
	
	// Create test book with ISBN
	testBookWithISBN := models.AudiobookshelfBook{
		ID: "book2",
		Media: struct {
			ID       string                        `json:"id"`
			Metadata models.AudiobookshelfMetadataStruct `json:"metadata"`
			CoverPath string                       `json:"coverPath"`
			Duration  float64                      `json:"duration"`
		}{
			ID: "media2",
			Metadata: models.AudiobookshelfMetadataStruct{
				Title:      "Test Book 2",
				AuthorName: "Test Author 2",
				ASIN:       "",
				ISBN:       "9781234567890",
			},
			Duration: 7200,
		},
	}
	
	// Test search by ISBN
	t.Run("Find by ISBN", func(t *testing.T) {
		// Setup mock expectations for ISBN search
		mockHC.On("SearchBookByISBN13", mock.Anything, "9781234567890").Return(testHardcoverBook, nil).Once()
		mockHC.On("GetUserBookID", mock.Anything, 456).Return(789, nil).Once()
		// Add mock expectation for CheckBookOwnership
		mockHC.On("CheckBookOwnership", mock.Anything, 456).Return(true, nil).Once()
		
		// Call findBookInHardcover
		result, err := svc.findBookInHardcover(context.Background(), testBookWithISBN)
		
		// Verify results
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "hc123", result.ID)
		
		// Verify mock expectations
		mockHC.AssertExpectations(t)
	})
	
	// Create test book with title and author
	testBookWithTitleAuthor := models.AudiobookshelfBook{
		ID: "book3",
		Media: struct {
			ID       string                        `json:"id"`
			Metadata models.AudiobookshelfMetadataStruct `json:"metadata"`
			CoverPath string                       `json:"coverPath"`
			Duration  float64                      `json:"duration"`
		}{
			ID: "media3",
			Metadata: models.AudiobookshelfMetadataStruct{
				Title:      "Test Book 3",
				AuthorName: "Test Author 3",
				ASIN:       "",
				ISBN:       "",
			},
			Duration: 5400,
		},
	}
	
	// Test search by title and author
	t.Run("Find by title and author", func(t *testing.T) {
		// Setup mock expectations for title/author search
		mockHC.On("SearchBooks", mock.Anything, "Test Book 3 Test Author 3", "").Return([]models.HardcoverBook{*testHardcoverBook}, nil).Once()
		
		// Create test edition to return from GetEdition
		testEdition := &models.Edition{
			ID:     "456",
			ASIN:   "B9876",
			ISBN10: "0123456789",
			ISBN13: "9780123456789",
		}
		
		// Add mock expectation for GetEdition
		mockHC.On("GetEdition", mock.Anything, "hc123").Return(testEdition, nil).Once()
		
		// Allow any number of calls to CheckBookOwnership (omit .Once() for unlimited calls)
		mockHC.On("CheckBookOwnership", mock.Anything, 456).Return(true, nil)
		
		// Allow any number of calls to GetUserBookID (omit .Once() for unlimited calls)
		mockHC.On("GetUserBookID", mock.Anything, 456).Return(789, nil)
		
		// Call findBookInHardcover
		result, err := svc.findBookInHardcover(context.Background(), testBookWithTitleAuthor)
		
		// Verify results
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "hc123", result.ID)
		
		// Verify mock expectations
		mockHC.AssertExpectations(t)
	})
}
