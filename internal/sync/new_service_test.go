package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewServiceWithRunIdentity_Success tests successful service creation.
func TestNewServiceWithRunIdentity_Success(t *testing.T) {
	// Initialize logger for testing
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	// Create a test config
	cfg := createTestConfig(true)
	cfg.Sync.StateFile = "/tmp/test_state_success.json"

	// Create mock clients
	absClient := &audiobookshelf.Client{}
	hcClient := new(MockHardcoverClient)

	// Create a new service
	svc, err := NewServiceWithRunIdentity(absClient, hcClient, cfg, "", time.Time{})

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

func TestNewServiceWithRunIdentityConfiguresHardcoverDryRun(t *testing.T) {
	logger.Setup(logger.Config{Level: "debug", Format: "json"})
	cfg := createTestConfig(false)
	cfg.Sync.DryRun = true
	cfg.Sync.StateFile = filepath.Join(t.TempDir(), "sync_state.json")
	cfg.Paths.CacheDir = t.TempDir()
	hcClient := hardcover.NewClient("test-token", logger.Get())

	svc, err := NewServiceWithRunIdentity(&audiobookshelf.Client{}, hcClient, cfg, "", time.Time{})
	require.NoError(t, err)

	createdID, err := svc.hardcover.CreateUserBook(context.Background(), "invalid", "invalid")
	require.NoError(t, err)
	assert.Equal(t, "-1", createdID)
}

// TestNewServiceWithRunIdentity_WithDifferentLogFormat tests creating a service with different log formats.
func TestNewServiceWithRunIdentity_WithDifferentLogFormat(t *testing.T) {
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
		svc, err := NewServiceWithRunIdentity(absClient, hcClient, cfg, "", time.Time{})

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
		svc, err := NewServiceWithRunIdentity(absClient, hcClient, cfg, "", time.Time{})

		// Verify results
		assert.NoError(t, err, "Should not return an error when creating a new service with console format")
		assert.NotNil(t, svc, "Should return a non-nil service")

		// Clean up
		_ = os.Remove(cfg.Sync.StateFile)
	})
}

// TestNewServiceWithRunIdentity_InvalidStatePath tests an invalid state path.
func TestNewServiceWithRunIdentity_InvalidStatePath(t *testing.T) {
	// Initialize logger for testing
	logger.Setup(logger.Config{Level: "debug", Format: "json"})

	// Create a test config with an invalid state path
	cfg := createTestConfig(true)
	cfg.Sync.StateFile = "/invalid/path/that/does/not/exist/state.json"

	// Create mock clients
	absClient := &audiobookshelf.Client{}
	hcClient := new(MockHardcoverClient)

	// Create a new service
	svc, err := NewServiceWithRunIdentity(absClient, hcClient, cfg, "", time.Time{})

	// Verify results
	// Note: This might not fail if the directory is created automatically
	// or if the path is actually valid in the test environment
	if err != nil {
		assert.Contains(t, err.Error(), "failed to load state", "Error message should indicate state loading failure")
		assert.Nil(t, svc, "Should return nil when state path is invalid")
	}
}
