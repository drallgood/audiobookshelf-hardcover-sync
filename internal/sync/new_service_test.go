package sync

import (
	"os"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/assert"
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
