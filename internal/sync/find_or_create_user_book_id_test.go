package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

	// Mock the GetUserBookID call to return an error
	editionID := "456"
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
	cfg.App.DryRun = true
	svc, mockClient := createTestServiceWithConfig(cfg)

	// Mock the GetUserBookID call to return no existing user book ID
	editionID := "456"
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, nil).Once()

	// Call the function
	userBookID, err := svc.findOrCreateUserBookID(context.Background(), editionID, "WANT_TO_READ")

	// Verify results
	assert.NoError(t, err, "Should not return an error in dry-run mode")
	assert.Equal(t, int64(-1), userBookID, "Should return -1 in dry-run mode")
	mockClient.AssertExpectations(t)
}

// TestFindOrCreateUserBookID_SecondCheckFindsUserBook tests the case where the second check finds a user book
func TestFindOrCreateUserBookID_SecondCheckFindsUserBook(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Mock the first GetUserBookID call to return no existing user book ID
	editionID := "456"
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, nil).Once()

	// Mock the second GetUserBookID call to return an existing user book ID
	expectedUserBookID := 789
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(expectedUserBookID, nil).Once()

	// Call the function
	userBookID, err := svc.findOrCreateUserBookID(context.Background(), editionID, "WANT_TO_READ")

	// Verify results
	assert.NoError(t, err, "Should not return an error when second check finds user book")
	assert.Equal(t, int64(expectedUserBookID), userBookID, "Should return the user book ID found in second check")
	mockClient.AssertExpectations(t)
}

// TestFindOrCreateUserBookID_SecondCheckError tests the case where the second check returns an error
func TestFindOrCreateUserBookID_SecondCheckError(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Mock the first GetUserBookID call to return no existing user book ID
	editionID := "456"
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, nil).Once()

	// Mock the second GetUserBookID call to return an error
	expectedErr := errors.New("API error")
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, expectedErr).Once()

	// Call the function
	userBookID, err := svc.findOrCreateUserBookID(context.Background(), editionID, "WANT_TO_READ")

	// Verify results
	assert.Error(t, err, "Should return an error when second check fails")
	assert.Contains(t, err.Error(), "error in second check for existing user book ID")
	assert.Equal(t, int64(0), userBookID, "Should return 0 when second check fails")
	mockClient.AssertExpectations(t)
}

// TestFindOrCreateUserBookID_CreateUserBookError tests the case where CreateUserBook returns an error
func TestFindOrCreateUserBookID_CreateUserBookError(t *testing.T) {
	// Create test service and mock client
	svc, mockClient := createTestService()

	// Mock the first GetUserBookID call to return no existing user book ID
	editionID := "456"
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, nil).Once()

	// Mock the second GetUserBookID call to also return no existing user book ID
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

	// Mock the first GetUserBookID call to return no existing user book ID
	editionID := "456"
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, nil).Once()

	// Mock the second GetUserBookID call to also return no existing user book ID
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

	// Mock the first GetUserBookID call to return no existing user book ID
	editionID := "456"
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(0, nil).Once()

	// Mock the second GetUserBookID call to also return no existing user book ID
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

// Helper function to create a test service with a custom config
func createTestServiceWithConfig(cfg *config.Config) (*Service, *MockHardcoverClient) {
	// Create a mock client
	mockClient := new(MockHardcoverClient)
	
	// Create and return a test service with the mock client
	svc := &Service{
		hardcover:          mockClient,
		config:             cfg,
		log:                logger.Get(),
		lastProgressUpdates: make(map[string]progressUpdateInfo),
	}
	
	return svc, mockClient
}
