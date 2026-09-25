package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestDatabaseSyncRunReportRetentionUsesYAMLAndEnvironment(t *testing.T) {
	t.Setenv("AUDIOBOOKSHELF_URL", "https://example.com/audiobookshelf")
	t.Setenv("AUDIOBOOKSHELF_TOKEN", "test-audiobookshelf-token")
	t.Setenv("HARDCOVER_TOKEN", "test-hardcover-token")
	t.Setenv("DATABASE_SYNC_RUN_REPORT_RETENTION", "7")

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("database:\n  sync_run_report_retention: 3\n"), 0644))
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, 7, cfg.Database.SyncRunReportRetention)

	t.Setenv("DATABASE_SYNC_RUN_REPORT_RETENTION", "")
	cfg, err = Load(path)
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.Database.SyncRunReportRetention)
}

func TestLoadConfig_WithAudnexusRegion(t *testing.T) {
	// Set required environment variables including Audnexus region
	t.Setenv("AUDIOBOOKSHELF_URL", "https://example.com/audiobookshelf")
	t.Setenv("AUDIOBOOKSHELF_TOKEN", "test-audiobookshelf-token")
	t.Setenv("HARDCOVER_TOKEN", "test-hardcover-token")
	t.Setenv("AUDIOBOOKSHELF_AUDNEXUS_REGION", " JP ")

	// The environment value overrides the file value and is normalized after
	// both sources have been loaded.
	yamlContent := `server:
  port: "8080"
audiobookshelf:
  url: "https://example.com/audiobookshelf"
  token: "test-audiobookshelf-token"
  audnexus_region: "de"
hardcover:
  token: "test-hardcover-token"
`

	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.WriteString(yamlContent)
	require.NoError(t, err)
	err = tmpfile.Close()
	require.NoError(t, err)

	cfg, err := Load(tmpfile.Name())
	require.NoError(t, err)

	assert.Equal(t, "jp", cfg.Audiobookshelf.AudnexusRegion,
		"AudnexusRegion should be normalized from the overriding environment value")
}

func TestLoadConfig_WithAudnexusRegionFromYAML(t *testing.T) {
	// Set required environment variables only (no audnexus env var)
	t.Setenv("AUDIOBOOKSHELF_URL", "https://example.com/audiobookshelf")
	t.Setenv("AUDIOBOOKSHELF_TOKEN", "test-audiobookshelf-token")
	t.Setenv("HARDCOVER_TOKEN", "test-hardcover-token")
	t.Setenv("AUDIOBOOKSHELF_AUDNEXUS_REGION", "")

	// YAML with audnexus_region set
	yamlContent := `server:
  port: "8080"
audiobookshelf:
  url: "https://example.com/audiobookshelf"
  token: "test-audiobookshelf-token"
  audnexus_region: "UK"
hardcover:
  token: "test-hardcover-token"
`

	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.WriteString(yamlContent)
	require.NoError(t, err)
	err = tmpfile.Close()
	require.NoError(t, err)

	cfg, err := Load(tmpfile.Name())
	require.NoError(t, err)

	assert.Equal(t, "uk", cfg.Audiobookshelf.AudnexusRegion,
		"AudnexusRegion should be lowercase from YAML config")
}

func TestLoadConfig_UnsupportedAudnexusRegionFallsBackToUS(t *testing.T) {
	t.Setenv("AUDIOBOOKSHELF_URL", "https://example.com/audiobookshelf")
	t.Setenv("AUDIOBOOKSHELF_TOKEN", "test-audiobookshelf-token")
	t.Setenv("HARDCOVER_TOKEN", "test-hardcover-token")
	t.Setenv("AUDIOBOOKSHELF_AUDNEXUS_REGION", "mx")

	cfg, err := Load("")
	require.NoError(t, err)
	assert.Equal(t, "us", cfg.Audiobookshelf.AudnexusRegion)
}

func TestNormalizeAudnexusRegionSupportsConfiguredRegions(t *testing.T) {
	for _, region := range []string{"us", "ca", "uk", "au", "de", "fr", "es", "in", "it", "jp"} {
		t.Run(region, func(t *testing.T) {
			normalized, valid := NormalizeAudnexusRegion(strings.ToUpper(region))
			assert.True(t, valid)
			assert.Equal(t, region, normalized)
		})
	}
}

func TestLoadConfig_DefaultAudnexusRegion(t *testing.T) {
	// No AUDIOBOOKSHELF_AUDNEXUS_REGION env var set
	t.Setenv("AUDIOBOOKSHELF_URL", "https://example.com/audiobookshelf")
	t.Setenv("AUDIOBOOKSHELF_TOKEN", "test-audiobookshelf-token")
	t.Setenv("HARDCOVER_TOKEN", "test-hardcover-token")
	t.Setenv("AUDIOBOOKSHELF_AUDNEXUS_REGION", "")

	yamlContent := `server:
  port: "8080"
audiobookshelf:
  url: "https://example.com/audiobookshelf"
  token: "test-audiobookshelf-token"
hardcover:
  token: "test-hardcover-token"
`

	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.WriteString(yamlContent)
	require.NoError(t, err)
	err = tmpfile.Close()
	require.NoError(t, err)

	cfg, err := Load(tmpfile.Name())
	require.NoError(t, err)

	assert.Equal(t, "", cfg.Audiobookshelf.AudnexusRegion,
		"AudnexusRegion should default to empty string")
}
