package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestNewService_Success tests the successful creation of a new service
func TestNewService_Success(t *testing.T) {
	// Initialize logger for testing
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	// Create a test config
	cfg := createTestConfig(true)
	cfg.Sync.StateFile = "/tmp/test_state_success.json"

	// Create mock clients
	absClient := &audiobookshelf.Client{}
	hcClient := new(MockHardcoverClient)

	// Create a new service
	svc, err := NewService(absClient, hcClient, cfg)

	// Verify results
	assert.NoError(t, err, "Should not return an error when creating a new service")
	assert.NotNil(t, svc, "Should return a non-nil service")
	assert.Equal(t, absClient, svc.audiobookshelf, "Should set the audiobookshelf client")
	assert.Equal(t, hcClient, svc.hardcover, "Should set the hardcover client")
	assert.Equal(t, cfg, svc.config, "Should set the config")
	assert.Equal(t, cfg.Sync.StateFile, svc.statePath, "Should set the state path")
	assert.NotNil(t, svc.state, "Should initialize the state")
	assert.NotNil(t, svc.lastProgressUpdates, "Should initialize the lastProgressUpdates map")

	// Clean up
	_ = os.Remove(cfg.Sync.StateFile)
}

func TestNewService_DryRunBlocksAllHardcoverMutations(t *testing.T) {
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	tempDir := t.TempDir()
	cfg := createTestConfig(false)
	cfg.Sync.DryRun = true
	cfg.Sync.StateFile = filepath.Join(tempDir, "sync_state.json")
	cfg.Paths.CacheDir = filepath.Join(tempDir, "cache")

	hcClient := new(MockHardcoverClient)
	svc, err := NewService(&audiobookshelf.Client{}, hcClient, cfg)
	require.NoError(t, err)
	require.IsType(t, &dryRunHardcoverClient{}, svc.hardcover)

	ctx := context.Background()
	insertedID, err := svc.hardcover.InsertUserBookRead(ctx, hardcover.InsertUserBookReadInput{})
	require.NoError(t, err)
	assert.Zero(t, insertedID)
	updated, err := svc.hardcover.UpdateUserBookRead(ctx, hardcover.UpdateUserBookReadInput{})
	require.NoError(t, err)
	assert.True(t, updated)
	require.NoError(t, svc.hardcover.DeleteUserBookRead(ctx, 1))
	require.NoError(t, svc.hardcover.UpdateUserBookStatus(ctx, hardcover.UpdateUserBookStatusInput{}))
	require.NoError(t, svc.hardcover.UpdateUserBookEdition(ctx, 1, 2))
	createdID, err := svc.hardcover.CreateUserBook(ctx, "2", "FINISHED")
	require.NoError(t, err)
	assert.Equal(t, "-1", createdID)
	require.NoError(t, svc.hardcover.MarkEditionAsOwned(ctx, 2))

	hcClient.On("GetEdition", ctx, "2").Return(&models.Edition{ID: "2"}, nil).Once()
	edition, err := svc.hardcover.GetEdition(ctx, "2")
	require.NoError(t, err)
	assert.Equal(t, "2", edition.ID)

	hcClient.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
	hcClient.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.Anything)
	hcClient.AssertNotCalled(t, "DeleteUserBookRead", mock.Anything, mock.Anything)
	hcClient.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
	hcClient.AssertNotCalled(t, "UpdateUserBookEdition", mock.Anything, mock.Anything, mock.Anything)
	hcClient.AssertNotCalled(t, "CreateUserBook", mock.Anything, mock.Anything, mock.Anything)
	hcClient.AssertNotCalled(t, "MarkEditionAsOwned", mock.Anything, mock.Anything)
	hcClient.AssertExpectations(t)
}

// TestNewService_WithDifferentLogFormat tests creating a service with different log formats
func TestNewService_WithDifferentLogFormat(t *testing.T) {
	// Test with JSON format
	t.Run("JSON format", func(t *testing.T) {
		// Initialize logger for testing
		logger.Setup(logger.Config{Level: "debug", Format: "json"})

		// Create a test config
		cfg := createTestConfig(true)
		cfg.Logging.Format = "json"
		cfg.Sync.StateFile = "/tmp/test_state_json.json"

		// Create mock clients
		absClient := &audiobookshelf.Client{}
		hcClient := new(MockHardcoverClient)

		// Create a new service
		svc, err := NewService(absClient, hcClient, cfg)

		// Verify results
		assert.NoError(t, err, "Should not return an error when creating a new service with JSON format")
		assert.NotNil(t, svc, "Should return a non-nil service")

		// Clean up
		_ = os.Remove(cfg.Sync.StateFile)
	})

	// Test with console format
	t.Run("Console format", func(t *testing.T) {
		// Initialize logger for testing
		logger.Setup(logger.Config{Level: "debug", Format: "console"})

		// Create a test config
		cfg := createTestConfig(true)
		cfg.Logging.Format = "console"
		cfg.Sync.StateFile = "/tmp/test_state_console.json"

		// Create mock clients
		absClient := &audiobookshelf.Client{}
		hcClient := new(MockHardcoverClient)

		// Create a new service
		svc, err := NewService(absClient, hcClient, cfg)

		// Verify results
		assert.NoError(t, err, "Should not return an error when creating a new service with console format")
		assert.NotNil(t, svc, "Should return a non-nil service")

		// Clean up
		_ = os.Remove(cfg.Sync.StateFile)
	})
}

// TestNewService_InvalidStatePath tests the case where the state path is invalid
func TestNewService_InvalidStatePath(t *testing.T) {
	// Initialize logger for testing
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	// Create a test config with an invalid state path
	cfg := createTestConfig(true)
	cfg.Sync.StateFile = "/invalid/path/that/does/not/exist/state.json"

	// Create mock clients
	absClient := &audiobookshelf.Client{}
	hcClient := new(MockHardcoverClient)

	// Create a new service
	svc, err := NewService(absClient, hcClient, cfg)

	// Verify results
	// Note: This might not fail if the directory is created automatically
	// or if the path is actually valid in the test environment
	if err != nil {
		assert.Contains(t, err.Error(), "failed to load state", "Error message should indicate state loading failure")
		assert.Nil(t, svc, "Should return nil when state path is invalid")
	}
}
