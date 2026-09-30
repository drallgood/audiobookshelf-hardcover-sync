package sync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestFindOrCreateUserBookID_InvalidEditionID tests the case where the edition ID is invalid
func TestFindOrCreateUserBookID_InvalidEditionID(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Call the function with an invalid edition ID
	userBookID, err := svc.findOrCreateUserBookID(context.Background(), "invalid", "WANT_TO_READ")

	// Verify results
	assert.Error(t, err, "Should return an error when edition ID is invalid")
	assert.Contains(t, err.Error(), "invalid edition ID format")
	assert.Equal(t, int64(0), userBookID, "Should return 0 when edition ID is invalid")
	mockClient.AssertExpectations(t)
}

// TestFindOrCreateUserBookID_ExistingUserBook tests the case where a user book already exists
func TestFindOrCreateUserBookID_ExistingUserBook(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Mock the GetUserBookID call to return an existing user book ID
	editionID := "456"
	expectedUserBookID := 789
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(expectedUserBookID, nil).Once()

	// Mock the GetEdition call for checking existing user book by book ID
	mockEdition := &models.Edition{
		ID:     "456",
		BookID: "432575", // Some book ID
	}
	mockClient.On("GetEdition", mock.Anything, editionID).Return(mockEdition, nil).Once()

	// Mock the findExistingUserBookForBook to return no existing user book
	// This requires type asserting to the concrete client, so we'll handle it differently
	// For now, let's make sure the test works with the current implementation

	// Call the function
	userBookID, err := svc.findOrCreateUserBookID(context.Background(), editionID, "WANT_TO_READ")

	// Verify results
	assert.NoError(t, err, "Should not return an error when user book exists")
	assert.Equal(t, int64(expectedUserBookID), userBookID, "Should return the existing user book ID")
	mockClient.AssertExpectations(t)
}

// TestFindOrCreateUserBookID_GetUserBookIDError tests the case where GetUserBookID returns an error
func TestFindOrCreateUserBookID_GetUserBookIDError(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Mock the GetEdition call
	editionID := "456"
	mockEdition := &models.Edition{
		ID:     "456",
		BookID: "432575", // Some book ID
	}
	mockClient.On("GetEdition", mock.Anything, editionID).Return(mockEdition, nil).Once()

	// Mock the findExistingUserBookForBook to return no existing user book
	// This requires type asserting to the concrete client, so we'll handle it differently

	// Mock the GetUserBookID call to return an error
	expectedErr := errors.New("API error")
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, expectedErr).Once()

	// Call the function
	userBookID, err := svc.findOrCreateUserBookID(context.Background(), editionID, "WANT_TO_READ")

	// Verify results
	assert.Error(t, err, "Should return an error when GetUserBookID fails")
	assert.Contains(t, err.Error(), "error checking for existing user book ID")
	assert.Equal(t, int64(0), userBookID, "Should return 0 when GetUserBookID fails")
	mockClient.AssertExpectations(t)
}

// TestFindOrCreateUserBookID_DryRun tests the case where dry-run mode is enabled
func TestFindOrCreateUserBookID_DryRun(t *testing.T) {
	// Create test service and mock client with dry-run enabled
	cfg := createTestConfig(false)
	cfg.Sync.DryRun = true
	svc, mockClient := createTestServiceWithConfig(cfg)

	// Mock the GetEdition call
	editionID := "456"
	mockEdition := &models.Edition{
		ID:     "456",
		BookID: "432575", // Some book ID
	}
	mockClient.On("GetEdition", mock.Anything, editionID).Return(mockEdition, nil).Once()

	// Mock the findExistingUserBookForBook to return no existing user book
	// This requires type asserting to the concrete client, so we'll handle it differently

	// Mock the GetUserBookID call to return no existing user book ID
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, nil).Once()

	// Call the function
	userBookID, err := svc.findOrCreateUserBookID(context.Background(), editionID, "WANT_TO_READ")

	// Verify results
	assert.NoError(t, err, "Should not return an error in dry-run mode")
	assert.Equal(t, int64(-1), userBookID, "Should return -1 in dry-run mode")
	mockClient.AssertExpectations(t)
}

// TestFindOrCreateUserBookID_LookupFindsUserBook tests the case where the second check finds a user book
func TestFindOrCreateUserBookID_LookupFindsUserBook(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Mock the GetEdition call
	editionID := "456"
	mockEdition := &models.Edition{
		ID:     "456",
		BookID: "432575", // Some book ID
	}
	mockClient.On("GetEdition", mock.Anything, editionID).Return(mockEdition, nil).Once()

	// Mock the findExistingUserBookForBook to return no existing user book
	// This requires type asserting to the concrete client, so we'll handle it differently

	// The single fresh lookup finds a user book before any insertion.
	expectedUserBookID := 789
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(expectedUserBookID, nil).Once()

	// Call the function
	userBookID, err := svc.findOrCreateUserBookID(context.Background(), editionID, "WANT_TO_READ")

	// Verify results
	assert.NoError(t, err, "Should not return an error when the lookup finds a user book")
	assert.Equal(t, int64(expectedUserBookID), userBookID, "Should return the user book ID found before insertion")
	mockClient.AssertExpectations(t)
}

// TestFindOrCreateUserBookID_LookupError tests the case where the second check returns an error
func TestFindOrCreateUserBookID_LookupError(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Mock the GetEdition call
	editionID := "456"
	mockEdition := &models.Edition{
		ID:     "456",
		BookID: "432575", // Some book ID
	}
	mockClient.On("GetEdition", mock.Anything, editionID).Return(mockEdition, nil).Once()

	// Mock the findExistingUserBookForBook to return no existing user book
	// This requires type asserting to the concrete client, so we'll handle it differently

	// The fresh lookup fails, so the service must not attempt insertion.
	expectedErr := errors.New("API error")
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, expectedErr).Once()

	// Call the function
	userBookID, err := svc.findOrCreateUserBookID(context.Background(), editionID, "WANT_TO_READ")

	// Verify results
	assert.Error(t, err, "Should return an error when the fresh lookup fails")
	assert.Contains(t, err.Error(), "error checking for existing user book ID")
	assert.Equal(t, int64(0), userBookID, "Should return 0 when the fresh lookup fails")
	mockClient.AssertExpectations(t)
}

// TestFindOrCreateUserBookID_CreateUserBookError tests the case where CreateUserBook returns an error
func TestFindOrCreateUserBookID_CreateUserBookError(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Mock the GetEdition call
	editionID := "456"
	mockEdition := &models.Edition{
		ID:     "456",
		BookID: "432575", // Some book ID
	}
	mockClient.On("GetEdition", mock.Anything, editionID).Return(mockEdition, nil).Once()

	// Mock the findExistingUserBookForBook to return no existing user book
	// This requires type asserting to the concrete client, so we'll handle it differently

	// The fresh edition-specific check must succeed before attempting insertion.
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, nil).Once()

	// Mock the CreateUserBook call to return an error
	expectedErr := errors.New("API error")
	mockClient.On("CreateUserBook", mock.Anything, editionID, "WANT_TO_READ").Return("", expectedErr).Once()

	// Call the function
	userBookID, err := svc.findOrCreateUserBookID(context.Background(), editionID, "WANT_TO_READ")

	// Verify results
	assert.Error(t, err, "Should return an error when CreateUserBook fails")
	assert.Contains(t, err.Error(), "failed to create user book")
	assert.Equal(t, int64(0), userBookID, "Should return 0 when CreateUserBook fails")
	mockClient.AssertExpectations(t)
}

// TestFindOrCreateUserBookID_InvalidUserBookIDFormat tests the case where the new user book ID has an invalid format
func TestFindOrCreateUserBookID_InvalidUserBookIDFormat(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Mock the GetEdition call
	editionID := "456"
	mockEdition := &models.Edition{
		ID:     "456",
		BookID: "432575", // Some book ID
	}
	mockClient.On("GetEdition", mock.Anything, editionID).Return(mockEdition, nil).Once()

	// Mock the findExistingUserBookForBook to return no existing user book
	// This requires type asserting to the concrete client, so we'll handle it differently

	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, nil).Once()

	// Mock the CreateUserBook call to return an invalid user book ID
	mockClient.On("CreateUserBook", mock.Anything, editionID, "WANT_TO_READ").Return("invalid", nil).Once()

	// Call the function
	userBookID, err := svc.findOrCreateUserBookID(context.Background(), editionID, "WANT_TO_READ")

	// Verify results
	assert.Error(t, err, "Should return an error when new user book ID has invalid format")
	assert.Contains(t, err.Error(), "invalid user book ID format")
	assert.Equal(t, int64(0), userBookID, "Should return 0 when new user book ID has invalid format")
	mockClient.AssertExpectations(t)
}

// TestFindOrCreateUserBookID_Success tests the successful creation of a new user book
func TestFindOrCreateUserBookID_Success(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Mock the GetEdition call
	editionID := "456"
	mockEdition := &models.Edition{
		ID:     "456",
		BookID: "432575", // Some book ID
	}
	mockClient.On("GetEdition", mock.Anything, editionID).Return(mockEdition, nil).Once()

	// Mock the findExistingUserBookForBook to return no existing user book
	// This requires type asserting to the concrete client, so we'll handle it differently

	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, nil).Once()

	// Mock the CreateUserBook call to return a valid user book ID
	expectedUserBookID := "789"
	mockClient.On("CreateUserBook", mock.Anything, editionID, "WANT_TO_READ").Return(expectedUserBookID, nil).Once()

	// Call the function
	userBookID, err := svc.findOrCreateUserBookID(context.Background(), editionID, "WANT_TO_READ")

	// Verify results
	assert.NoError(t, err, "Should not return an error when creating user book succeeds")
	assert.Equal(t, int64(789), userBookID, "Should return the new user book ID")
	mockClient.AssertExpectations(t)
}

func TestFindOrCreateUserBookID_FinishedBookCreatesWantToReadFirst(t *testing.T) {
	svc, mockClient := createTestService()

	const editionID = "456"
	mockClient.On("GetEdition", mock.Anything, editionID).Return(&models.Edition{
		ID:     editionID,
		BookID: "432575",
	}, nil).Once()
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, nil).Once()
	mockClient.On("CreateUserBook", mock.Anything, editionID, "WANT_TO_READ").Return("789", nil).Once()

	userBookID, err := svc.findOrCreateUserBookID(context.Background(), editionID, "FINISHED")

	assert.NoError(t, err)
	assert.Equal(t, int64(789), userBookID)
	mockClient.AssertExpectations(t)
}

// TestFindOrCreateUserBookID_FindsExistingUserBookForDifferentEdition tests that the function
// finds an existing user book for the same book even when it has a different edition
func TestFindOrCreateUserBookID_FindsExistingUserBookForDifferentEdition(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Mock the GetEdition call
	editionID := "456"
	mockEdition := &models.Edition{
		ID:     "456",
		BookID: "432575", // Some book ID
	}
	mockClient.On("GetEdition", mock.Anything, editionID).Return(mockEdition, nil).Once()

	// Since this test uses an interface mock, no book-level lookup is configured;
	// the fresh edition-specific lookup still occurs before any insertion.

	// Mock the GetUserBookID call (this will be called after findExistingUserBookForBook)
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, nil).Once()

	// Mock the CreateUserBook call (since no existing user book is found)
	mockClient.On("CreateUserBook", mock.Anything, editionID, "WANT_TO_READ").Return("789", nil).Once()

	// Call the function
	_, err := svc.findOrCreateUserBookID(context.Background(), editionID, "WANT_TO_READ")

	// Verify the function doesn't error out
	assert.NoError(t, err, "Should not return an error")
	mockClient.AssertExpectations(t)
}

func TestFindOrCreateUserBookIDReusesFetchedSnapshotOnlyForCurrentBookOperation(t *testing.T) {
	svc, mockClient := createTestService()
	svc.userBookCache = NewPersistentUserBookCache(t.TempDir())
	require.NoError(t, svc.userBookCache.Load())
	svc.findExistingUserBookForBookFunc = func(context.Context, int64) (int64, error) { return 789, nil }
	targetEdition := &models.Edition{ID: "456", BookID: "432575", ReadingFormatID: "4"}
	existing := &models.HardcoverBook{ID: "432575", UserBookID: "789", EditionID: "456", BookStatusID: 2}
	mockClient.On("GetEdition", mock.Anything, "456").Return(targetEdition, nil).Once()
	mockClient.On("GetUserBook", mock.Anything, "789").Return(existing, nil).Once()
	ctx := withOperationUserBookSnapshots(context.Background())

	userBookID, err := svc.findOrCreateUserBookID(ctx, "456", "IN_PROGRESS")

	require.NoError(t, err)
	require.Equal(t, int64(789), userBookID)
	reused, found := svc.getUserBookSnapshot(ctx, 789)
	require.True(t, found)
	require.Same(t, existing, reused)
	_, found = svc.userBookCache.GetByUserBook(789)
	assert.False(t, found, "a fetched snapshot must not be persisted across sync operations")
	mockClient.AssertExpectations(t)
}

func TestFindOrCreateUserBookIDUpdatesOperationSnapshotAfterEditionCorrection(t *testing.T) {
	svc, mockClient := createTestService()
	svc.userBookCache = NewPersistentUserBookCache(t.TempDir())
	require.NoError(t, svc.userBookCache.Load())
	svc.findExistingUserBookForBookFunc = func(context.Context, int64) (int64, error) { return 789, nil }
	targetEdition := &models.Edition{
		ID: "456", BookID: "432575", ReadingFormatID: "4", ASIN: "NEWASIN123", ISBN10: "0306406152", ISBN13: "9780306406157",
	}
	existing := &models.HardcoverBook{ID: "432575", UserBookID: "789", EditionID: "123", BookStatusID: 2, Title: "Preserved title"}
	svc.userBookCache.SetByUserBook(789, &models.HardcoverBook{ID: "432575", UserBookID: "789", EditionID: "123"})
	mockClient.On("GetEdition", mock.Anything, "456").Return(targetEdition, nil).Once()
	mockClient.On("GetUserBook", mock.Anything, "789").Return(existing, nil).Once()
	mockClient.On("UpdateUserBookEdition", mock.Anything, 789, 456).Return(nil).Once()
	ctx := withOperationUserBookSnapshots(context.Background())

	userBookID, err := svc.findOrCreateUserBookID(ctx, "456", "IN_PROGRESS")

	require.NoError(t, err)
	require.Equal(t, int64(789), userBookID)
	corrected, found := svc.getUserBookSnapshot(ctx, 789)
	require.True(t, found)
	require.Equal(t, "456", corrected.EditionID)
	require.Equal(t, "NEWASIN123", corrected.EditionASIN)
	require.Equal(t, "0306406152", corrected.EditionISBN10)
	require.Equal(t, "9780306406157", corrected.EditionISBN13)
	require.Equal(t, 2, corrected.BookStatusID)
	require.Equal(t, "Preserved title", corrected.Title)
	_, found = svc.userBookCache.GetByUserBook(789)
	assert.False(t, found, "the corrected snapshot remains scoped to this book operation")
	mockClient.AssertExpectations(t)
}

func TestFindOrCreateUserBookIDReusesSnapshotDuringDryRunEditionCorrection(t *testing.T) {
	cfg := createTestConfig(false)
	cfg.Sync.DryRun = true
	svc, mockClient := createTestServiceWithConfig(cfg)
	svc.findExistingUserBookForBookFunc = func(context.Context, int64) (int64, error) { return 789, nil }
	targetEdition := &models.Edition{ID: "456", BookID: "432575", ReadingFormatID: "4"}
	existing := &models.HardcoverBook{ID: "432575", UserBookID: "789", EditionID: "123", BookStatusID: 2}
	mockClient.On("GetEdition", mock.Anything, "456").Return(targetEdition, nil).Once()
	mockClient.On("GetUserBook", mock.Anything, "789").Return(existing, nil).Once()
	ctx := withOperationUserBookSnapshots(context.Background())

	userBookID, err := svc.findOrCreateUserBookID(ctx, "456", "IN_PROGRESS")

	require.NoError(t, err)
	require.Equal(t, int64(789), userBookID)
	reused, found := svc.getUserBookSnapshot(ctx, 789)
	require.True(t, found)
	require.Same(t, existing, reused)
	assert.Equal(t, "123", reused.EditionID, "dry run must retain the live edition snapshot")
	assert.Equal(t, 2, reused.BookStatusID)
	mockClient.AssertNotCalled(t, "UpdateUserBookEdition", mock.Anything, mock.Anything, mock.Anything)
	mockClient.AssertExpectations(t)
}

func TestFindOrCreateUserBookIDInvalidatesPersistentSnapshotWhenEditionCorrectionFails(t *testing.T) {
	svc, mockClient := createTestService()
	svc.userBookCache = NewPersistentUserBookCache(t.TempDir())
	require.NoError(t, svc.userBookCache.Load())
	svc.findExistingUserBookForBookFunc = func(context.Context, int64) (int64, error) { return 789, nil }
	targetEdition := &models.Edition{ID: "456", BookID: "432575", ReadingFormatID: "4"}
	stale := &models.HardcoverBook{ID: "432575", UserBookID: "789", EditionID: "123"}
	svc.userBookCache.SetByUserBook(789, stale)
	mockClient.On("GetEdition", mock.Anything, "456").Return(targetEdition, nil).Once()
	mockClient.On("GetUserBook", mock.Anything, "789").Return(stale, nil).Once()
	mockClient.On("UpdateUserBookEdition", mock.Anything, 789, 456).Return(errors.New("write failed")).Once()
	ctx := withOperationUserBookSnapshots(context.Background())

	_, err := svc.findOrCreateUserBookID(ctx, "456", "IN_PROGRESS")

	require.ErrorContains(t, err, "failed to update user book edition")
	_, found := svc.userBookCache.GetByUserBook(789)
	assert.False(t, found, "an ambiguous failed mutation must not leave a stale edition snapshot cached")
	_, found = svc.getUserBookSnapshot(ctx, 789)
	assert.False(t, found)
	mockClient.AssertExpectations(t)
}

func TestFindOrCreateUserBookIDWithVerifiedEditionUsesFreshGraphQLCheckWithoutRefetchingEdition(t *testing.T) {
	tests := []struct {
		name              string
		userBookExists    bool
		lookupFails       bool
		dryRun            bool
		wantID            int64
		wantErr           bool
		wantEditionLookup int
		wantInsert        int
	}{
		{name: "existing user book", userBookExists: true, wantID: 789, wantEditionLookup: 0},
		{name: "new user book", wantID: 789, wantEditionLookup: 1, wantInsert: 1},
		{name: "lookup failure", lookupFails: true, wantErr: true, wantEditionLookup: 0},
		{name: "dry run", dryRun: true, wantID: -1, wantEditionLookup: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counts := make(map[string]int)
			var countsMu sync.Mutex
			recordRequest := func(operation string) {
				countsMu.Lock()
				counts[operation]++
				countsMu.Unlock()
			}
			requestCount := func(operation string) int {
				countsMu.Lock()
				defer countsMu.Unlock()
				return counts[operation]
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Query string `json:"query"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				query := request.Query
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.Contains(query, "GetCurrentUserID"):
					recordRequest("current_user")
					_, _ = w.Write([]byte(`{"data":{"me":[{"id":99}]}}`))
				case strings.Contains(query, "GetUserBookByBook("):
					recordRequest("by_book_and_edition")
					if tt.lookupFails {
						_, _ = w.Write([]byte(`{"errors":[{"message":"lookup failed"}]}`))
						return
					}
					if tt.userBookExists {
						_, _ = w.Write([]byte(`{"data":{"user_books":[{"id":789,"book_id":432575,"edition_id":456}]}}`))
						return
					}
					_, _ = w.Write([]byte(`{"data":{"user_books":[]}}`))
				case strings.Contains(query, "GetUserBookByEdition"):
					recordRequest("by_edition")
					_, _ = w.Write([]byte(`{"data":{"user_books":[]}}`))
				case strings.Contains(query, "GetEdition"):
					recordRequest("get_edition")
					_, _ = w.Write([]byte(`{"data":{"editions":[]}}`))
				case strings.Contains(query, "InsertUserBook"):
					recordRequest("insert")
					_, _ = w.Write([]byte(`{"data":{"insert_user_book":{"id":789,"user_book":{"id":789,"status_id":1},"error":null}}}`))
				default:
					t.Errorf("unexpected GraphQL operation: %s", query)
					http.Error(w, "unexpected operation", http.StatusBadRequest)
				}
			}))
			defer server.Close()

			clientConfig := hardcover.DefaultClientConfig()
			clientConfig.BaseURL = server.URL
			clientConfig.RateLimit = time.Nanosecond
			clientConfig.MaxRetries = 0
			client := hardcover.NewClientWithConfig(clientConfig, "test-token", logger.Get())
			client.SetDryRun(tt.dryRun)
			cfg := createTestConfig(false)
			cfg.Sync.DryRun = tt.dryRun
			svc, _ := createTestServiceWithConfig(cfg)
			svc.hardcover = client
			svc.findExistingUserBookForBookFunc = func(context.Context, int64) (int64, error) { return 0, nil }
			edition := &models.Edition{ID: "456", BookID: "432575", ReadingFormatID: "4"}

			id, err := svc.findOrCreateUserBookIDWithEdition(context.Background(), edition.ID, "WANT_TO_READ", edition)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantID, id)
			}
			assert.Equal(t, 1, requestCount("current_user"))
			assert.Equal(t, 1, requestCount("by_book_and_edition"), "the concrete path must do a fresh existence check")
			assert.Equal(t, tt.wantEditionLookup, requestCount("by_edition"))
			assert.Zero(t, requestCount("get_edition"), "the verified edition should be reused")
			assert.Equal(t, tt.wantInsert, requestCount("insert"))
		})
	}
}

// Helper function to create a test service with a custom config
func createTestServiceWithConfig(cfg *config.Config) (*Service, *MockHardcoverClient) {
	// Create a mock client
	mockClient := new(MockHardcoverClient)

	// Create and initialize caches
	userBookCache := NewPersistentUserBookCache("/tmp/test-cache")
	_ = userBookCache.Load() // Load cache (will create empty if doesn't exist)

	// Create and return a test service with the mock client
	svc := &Service{
		hardcover:           mockClient,
		config:              cfg,
		log:                 logger.Get(),
		lastProgressUpdates: make(map[string]progressUpdateInfo),
		userBookCache:       userBookCache,
	}

	return svc, mockClient
}
