package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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
	
	// Create test config with default values and update sync settings
	testConfig := config.DefaultConfig()
	
	// Configure sync settings - all sync-related settings are now consolidated under Sync
	testConfig.Sync.Incremental = false
	testConfig.Sync.StateFile = "/tmp/sync_state_test.json"
	testConfig.Sync.MinChangeThreshold = 60
	testConfig.Sync.SyncInterval = 1 * time.Hour
	testConfig.Sync.MinimumProgress = 0.05
	testConfig.Sync.SyncWantToRead = true
	testConfig.Sync.SyncOwned = true
	testConfig.Sync.DryRun = true
	
	// Initialize libraries include/exclude
	testConfig.Sync.Libraries.Include = []string{}
	testConfig.Sync.Libraries.Exclude = []string{}
	
	// Set test-specific app settings
	testConfig.App.TestBookFilter = ""
	testConfig.App.TestBookLimit = 0
	
	// Clear deprecated sync fields in App
	testConfig.App.SyncInterval = 0
	testConfig.App.MinimumProgress = 0
	testConfig.App.SyncWantToRead = false
	testConfig.App.SyncOwned = false
	testConfig.App.DryRun = false
	
	// Set test service configurations
	testConfig.Audiobookshelf.URL = "https://abs.example.com"
	testConfig.Audiobookshelf.Token = "test-token"
	testConfig.Hardcover.Token = "test-token"

	// Create the service with mocked clients
	svc := &Service{
		audiobookshelf: nil, // Will be replaced with mock
		hardcover:      mockHC,
		config:         testConfig,
		log:            log,
		state:          testState,
		statePath:      "",
		lastProgressUpdates: make(map[string]progressUpdateInfo),
		asinCache:           make(map[string]*models.HardcoverBook),
		persistentCache:     NewPersistentASINCache("/tmp"),
		userBookCache:       NewPersistentUserBookCache("/tmp"),
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
	
	// Create test config with default values and update sync settings
	testConfig := config.DefaultConfig()
	
	// Configure sync settings - all sync-related settings are now consolidated under Sync
	testConfig.Sync.Incremental = false
	testConfig.Sync.StateFile = "/tmp/sync_state_test.json"
	testConfig.Sync.MinChangeThreshold = 60
	testConfig.Sync.SyncInterval = 1 * time.Hour
	testConfig.Sync.MinimumProgress = 0.05
	testConfig.Sync.SyncWantToRead = true
	testConfig.Sync.SyncOwned = true
	testConfig.Sync.DryRun = true
	
	// Initialize libraries include/exclude
	testConfig.Sync.Libraries.Include = []string{}
	testConfig.Sync.Libraries.Exclude = []string{}
	
	// Set test-specific app settings
	testConfig.App.TestBookFilter = ""
	testConfig.App.TestBookLimit = 0
	
	// Clear deprecated sync fields in App
	testConfig.App.SyncInterval = 0
	testConfig.App.MinimumProgress = 0
	testConfig.App.SyncWantToRead = false
	testConfig.App.SyncOwned = false
	testConfig.App.DryRun = false
	
	// Set test service configurations
	testConfig.Audiobookshelf.URL = "https://abs.example.com"
	testConfig.Audiobookshelf.Token = "test-token"
	testConfig.Hardcover.Token = "test-token"

	// Create the service with mocked clients
	svc := &Service{
		audiobookshelf: nil, // Will be replaced with mock
		hardcover:      mockHC,
		config:         testConfig,
		log:            log,
		state:          testState,
		statePath:      "",
		lastProgressUpdates: make(map[string]progressUpdateInfo),
		asinCache:           make(map[string]*models.HardcoverBook),
		persistentCache:     NewPersistentASINCache("/tmp"),
		userBookCache:       NewPersistentUserBookCache("/tmp"),
		summary:             &SyncSummary{},
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
					ID        string                               `json:"id"`
					Metadata  models.AudiobookshelfMetadataStruct `json:"metadata"`
					CoverPath string                              `json:"coverPath"`
					Duration  float64                             `json:"duration"`
				}{
					ID: "media1",
					Metadata: models.AudiobookshelfMetadataStruct{
						Title:      "Test Book 1",
						AuthorName: "Test Author 1",
						ASIN:       "B123456789",
						ISBN:       "9781234567890",
					},
					CoverPath: "/covers/test.jpg",
					Duration:  3600,
				},
				Progress: struct {
					CurrentTime float64 `json:"currentTime"`
					IsFinished  bool    `json:"isFinished"`
					StartedAt   int64   `json:"startedAt"`
					FinishedAt  int64   `json:"finishedAt"`
				}{
					CurrentTime: 0,
					IsFinished:  false,
					StartedAt:   0,
					FinishedAt:  0,
				},
			},
		}
		
		// Setup mock expectations
		mockABS.On("GetLibraryItems", mock.Anything, "lib1").Return(testBooks, nil).Once()
		
		// Setup mock for SearchBookByASIN
		testBook := &models.HardcoverBook{
			ID: "test-book-id",
			Title: "Test Book 1",
			Authors: []models.Author{
				{Name: "Test Author 1"},
			},
			ASIN: "B123456789",
		}
		mockHC.On("SearchBookByASIN", mock.Anything, "B123456789").Return(testBook, nil).Once()
		// Some code paths attempt an ISBN13 lookup during enrichment; stub it to return no result
		mockHC.On("SearchBookByISBN13", mock.Anything, "9781234567890").Return((*models.HardcoverBook)(nil), nil).Maybe()
		// Enrichment may fall back to a title/author search; stub it as returning no results
		mockHC.On("SearchBooks", mock.Anything, "Test Book 1", "Test Author 1").Return([]*TestHardcoverBook{}, nil).Maybe()
		
		// Replace the clients in the service with our mocks
		svc.audiobookshelf = mockABS
		svc.hardcover = mockHC
		
		// Set a limit of 1 book
		testConfig.Sync.TestBookLimit = 1
		
		// Call processLibrary
		processed, err := svc.processLibrary(context.Background(), testLibrary, 0, testUserProgress)
		
		// Verify results
		assert.NoError(t, err)
		assert.Equal(t, 1, processed)
		
		// Verify mock expectations
		mockABS.AssertExpectations(t)
	})
}

func TestCheckpointStatePersistsCompletedBook(t *testing.T) {
	svc, _ := createTestService()
	svc.statePath = filepath.Join(t.TempDir(), "sync_state.json")
	svc.state.UpdateBook("book1", 50, "IN_PROGRESS")
	svc.state.SetHasProgressSeconds("book1")

	require.NoError(t, svc.checkpointState("book1"))

	loadedState, err := state.LoadState(svc.statePath)
	require.NoError(t, err)
	bookState, exists := loadedState.GetBookState("book1")
	require.True(t, exists)
	assert.Equal(t, 0.5, bookState.LastProgress)
	assert.Equal(t, "IN_PROGRESS", bookState.Status)
	assert.True(t, bookState.HasProgressSeconds)
}

func TestCheckpointStateSkipsDryRun(t *testing.T) {
	svc, _ := createTestService()
	svc.config.Sync.DryRun = true
	svc.statePath = filepath.Join(t.TempDir(), "sync_state.json")
	svc.state.UpdateBook("book1", 50, "IN_PROGRESS")

	require.NoError(t, svc.checkpointState("book1"))
	_, err := os.Stat(svc.statePath)
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, exists := svc.state.GetBookState("book1")
	assert.True(t, exists, "dry-run state should remain available in memory")
}

func TestProcessLibraryReturnsCheckpointFailure(t *testing.T) {
	svc, mockHC := createTestService()
	svc.config.Sync.ProcessUnreadBooks = false
	svc.statePath = t.TempDir() // Renaming a state file over a directory must fail.

	mockABS := new(MockAudiobookshelfClient)
	book := toAudiobookshelfBook(createTestBook("book1", "Unread Book", "Test Author", "", ""))
	book.Progress.CurrentTime = 0
	book.Progress.IsFinished = false
	mockABS.On("GetLibraryItems", mock.Anything, "lib1").Return([]models.AudiobookshelfBook{*book}, nil).Once()
	svc.audiobookshelf = mockABS

	processed, err := svc.processLibrary(
		context.Background(),
		&audiobookshelf.AudiobookshelfLibrary{ID: "lib1", Name: "Test Library"},
		0,
		&models.AudiobookshelfUserProgress{},
	)

	assert.ErrorIs(t, err, errStateCheckpoint)
	assert.Zero(t, processed)
	mockABS.AssertExpectations(t)
	mockHC.AssertExpectations(t)
}

func TestProcessLibraryCheckpointsOnceThenReturnsCancellation(t *testing.T) {
	svc, mockHC := createTestService()
	svc.config.Sync.ProcessUnreadBooks = false
	svc.statePath = filepath.Join(t.TempDir(), "sync_state.json")
	svc.state.UpdateBook("completed-book", 50, "IN_PROGRESS")

	mockABS := new(MockAudiobookshelfClient)
	books := make([]models.AudiobookshelfBook, 2)
	for i := range books {
		book := toAudiobookshelfBook(createTestBook(fmt.Sprintf("book%d", i+1), "Unread Book", "Test Author", "", ""))
		book.Progress.CurrentTime = 0
		book.Progress.IsFinished = false
		books[i] = *book
	}
	mockABS.On("GetLibraryItems", mock.Anything, "lib1").Return(books, nil).Once()
	svc.audiobookshelf = mockABS

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	processed, err := svc.processLibrary(
		ctx,
		&audiobookshelf.AudiobookshelfLibrary{ID: "lib1", Name: "Test Library"},
		0,
		&models.AudiobookshelfUserProgress{},
	)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, processed)
	assert.Equal(t, int32(1), svc.summary.TotalBooksProcessed, "cancellation must stop before the second book")
	loadedState, loadErr := state.LoadState(svc.statePath)
	require.NoError(t, loadErr)
	_, exists := loadedState.GetBookState("completed-book")
	assert.True(t, exists, "the final checkpoint must be durable before cancellation returns")
	mockABS.AssertExpectations(t)
	mockHC.AssertExpectations(t)
}

func TestSyncReturnsFinalStateSaveFailure(t *testing.T) {
	svc, mockHC := createTestService()
	svc.statePath = t.TempDir() // Renaming a state file over a directory must fail.
	svc.config.Paths.MismatchOutputDir = t.TempDir()

	mockABS := new(MockAudiobookshelfClient)
	mockABS.On("GetUserProgress", mock.Anything).Return(&models.AudiobookshelfUserProgress{}, nil).Once()
	mockABS.On("GetLibraries", mock.Anything).Return([]audiobookshelf.AudiobookshelfLibrary{}, nil).Once()
	mockHC.On("ClearUserBookCache").Return().Once()
	svc.audiobookshelf = mockABS

	err := svc.Sync(context.Background())

	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to save final sync state")
	mockABS.AssertExpectations(t)
	mockHC.AssertExpectations(t)
}

// TestProcessBook and TestFindBookInHardcover functions would follow the same pattern
// with their test configurations updated similarly to the above examples.
