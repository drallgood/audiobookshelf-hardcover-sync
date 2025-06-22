package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigFromFile(t *testing.T) {
	// Set required environment variables for testing
	t.Setenv("AUDIOBOOKSHELF_URL", "https://example.com/audiobookshelf")
	t.Setenv("AUDIOBOOKSHELF_TOKEN", "test-audiobookshelf-token")
	t.Setenv("HARDCOVER_TOKEN", "test-hardcover-token")

	// Test with a sample YAML configuration
	yamlContent := `# Server configuration
server:
  address: ":8080"
  debug: true

# Logging configuration
logging:
  level: "debug"
  pretty: true

# Audiobookshelf configuration
audiobookshelf:
  url: "https://example.com/audiobookshelf"
  token: "test-audiobookshelf-token"
  timeout: "30s"

# Hardcover configuration
hardcover:
  token: "test-hardcover-token"
  timeout: "30s"
  sync_delay: "100ms"

# Application settings
app:
  debug: true
  log_level: "debug"
  sync_interval: "1h"
  minimum_progress: 0.99
  audiobook_match_mode: "strict"
  sync_want_to_read: true
  sync_owned: true
  dry_run: true
  test_book_filter: ""
  test_book_limit: 5

# File paths
paths:
  mismatch_json_file: "./mismatched_books.json"
  cache_dir: "./cache"

# Cache configuration
cache:
  enabled: true
  ttl: "24h"
  path: "./cache"

# HTTP client configuration
http:
  timeout: "30s"
  max_idle_conns: 10
  idle_conn_timeout: "90s"
  tls_handshake_timeout: "10s"
  expect_continue_timeout: "1s"
`

	// Create a temporary file with the YAML content
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err, "Failed to create temporary file")
	defer os.Remove(tmpfile.Name()) // Clean up

	_, err = tmpfile.WriteString(yamlContent)
	require.NoError(t, err, "Failed to write to temporary file")
	err = tmpfile.Close()
	require.NoError(t, err, "Failed to close temporary file")

	// Test loading the configuration
	cfg, err := Load(tmpfile.Name())
	require.NoError(t, err, "Failed to load configuration from file")

	// Verify the loaded configuration
	assert.Equal(t, "https://example.com/audiobookshelf", cfg.Audiobookshelf.URL)
	assert.Equal(t, "test-audiobookshelf-token", cfg.Audiobookshelf.Token)
	assert.Equal(t, "test-hardcover-token", cfg.Hardcover.Token)
	// Note: DryRun and TestBookLimit are not directly on the Config struct
	// They are part of the sync configuration which may be handled differently
}

func TestLoadConfig(t *testing.T) {
	// Set required environment variables for testing
	t.Setenv("AUDIOBOOKSHELF_URL", "https://example.com/audiobookshelf")
	t.Setenv("AUDIOBOOKSHELF_TOKEN", "test-audiobookshelf-token")
	t.Setenv("HARDCOVER_TOKEN", "test-hardcover-token")

	// Test with a sample YAML configuration
	yamlContent := `# Server configuration
server:
  address: ":8080"
  debug: true

# Logging configuration
logging:
  level: "debug"
  pretty: true

# Audiobookshelf configuration
audiobookshelf:
  url: "https://example.com/audiobookshelf"
  token: "test-audiobookshelf-token"
  timeout: "30s"

# Hardcover configuration
hardcover:
  token: "test-hardcover-token"
  timeout: "30s"
  sync_delay: "100ms"

# Application settings
app:
  debug: true
  log_level: "debug"
  sync_interval: "1h"
  minimum_progress: 0.99
  audiobook_match_mode: "strict"
  sync_want_to_read: true
  sync_owned: true
  dry_run: true
  test_book_filter: ""
  test_book_limit: 5

# File paths
paths:
  mismatch_json_file: "./mismatched_books.json"
  cache_dir: "./cache"

# Cache configuration
cache:
  enabled: true
  ttl: "24h"
  path: "./cache"

# HTTP client configuration
http:
  timeout: "30s"
  max_idle_conns: 10
  idle_conn_timeout: "90s"
  tls_handshake_timeout: "10s"
  expect_continue_timeout: "1s"
`

	// Create a temporary file with the YAML content
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err, "Failed to create temporary file")
	defer os.Remove(tmpfile.Name()) // Clean up

	_, err = tmpfile.WriteString(yamlContent)
	require.NoError(t, err, "Failed to write to temporary file")
	err = tmpfile.Close()
	require.NoError(t, err, "Failed to close temporary file")

	// Test loading the configuration
	cfg, err := Load(tmpfile.Name())
	require.NoError(t, err, "Failed to load configuration from file")

	// Verify the loaded configuration
	assert.Equal(t, "https://example.com/audiobookshelf", cfg.Audiobookshelf.URL)
	assert.Equal(t, "test-audiobookshelf-token", cfg.Audiobookshelf.Token)
	assert.Equal(t, "test-hardcover-token", cfg.Hardcover.Token)
}
