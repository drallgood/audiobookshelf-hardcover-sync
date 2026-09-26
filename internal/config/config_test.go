package config

import (
	"io"
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

func TestAudiobookshelfNetworkTrustDefaultYAMLAndEnvironment(t *testing.T) {
	t.Setenv("AUDIOBOOKSHELF_URL", "")
	t.Setenv("AUDIOBOOKSHELF_TOKEN", "abs-token")
	t.Setenv("AUDIOBOOKSHELF_NETWORK_TRUST", "")
	t.Setenv("HARDCOVER_TOKEN", "hc-token")
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("audiobookshelf:\n  url: https://abs.example\n  network_trust: public_only\n"), 0644))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "public_only", cfg.Audiobookshelf.NetworkTrust)

	t.Setenv("AUDIOBOOKSHELF_NETWORK_TRUST", "allow_private")
	cfg, err = Load(path)
	require.NoError(t, err)
	assert.Equal(t, "allow_private", cfg.Audiobookshelf.NetworkTrust)

	t.Setenv("AUDIOBOOKSHELF_NETWORK_TRUST", "")
	defaultCfg := DefaultConfig()
	assert.Equal(t, "allow_private", defaultCfg.Audiobookshelf.NetworkTrust)
}

func TestAudiobookshelfNetworkTrustRejectsUnsupportedValues(t *testing.T) {
	t.Setenv("AUDIOBOOKSHELF_URL", "https://abs.example")
	t.Setenv("AUDIOBOOKSHELF_TOKEN", "abs-token")
	t.Setenv("AUDIOBOOKSHELF_NETWORK_TRUST", "trust_everything")
	t.Setenv("HARDCOVER_TOKEN", "hc-token")
	_, err := Load("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audiobookshelf.network_trust")
	assert.Contains(t, err.Error(), "trust_everything")
}

func TestAudiobookshelfConfigValidatesAndNormalizesURL(t *testing.T) {
	t.Setenv("AUDIOBOOKSHELF_URL", "")
	t.Setenv("AUDIOBOOKSHELF_TOKEN", "abs-token")
	t.Setenv("AUDIOBOOKSHELF_NETWORK_TRUST", "")
	t.Setenv("HARDCOVER_TOKEN", "hc-token")
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("audiobookshelf:\n  url: http://abs.example/\n"), 0644))
	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "http://abs.example", cfg.Audiobookshelf.URL)

	t.Setenv("AUDIOBOOKSHELF_NETWORK_TRUST", "public_only")
	_, err = Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must use https")
}

func TestLoadForToolLayersQuietlyWithoutServiceRequirements(t *testing.T) {
	for _, key := range []string{
		"AUDIOBOOKSHELF_URL", "AUDIOBOOKSHELF_TOKEN", "AUDIOBOOKSHELF_NETWORK_TRUST",
		"AUDIOBOOKSHELF_AUDNEXUS_REGION", "HARDCOVER_TOKEN", "ENABLE_WEB_UI",
	} {
		t.Setenv(key, "")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(
		"audiobookshelf:\n  url: http://abs.lan:13378/\n  network_trust: allow_private\n  audnexus_region: xx\nhardcover:\n  token: file-token\n",
	), 0o600))

	var cfg *Config
	stdout := captureStdout(t, func() {
		var err error
		cfg, err = LoadForTool(path)
		require.NoError(t, err)
	})
	assert.Empty(t, stdout, "standalone commands keep stdout for their own output")
	assert.Equal(t, "http://abs.lan:13378", cfg.Audiobookshelf.URL)
	assert.Equal(t, "us", cfg.Audiobookshelf.AudnexusRegion, "an unsupported region warns and uses US")
	assert.Equal(t, "file-token", cfg.Hardcover.Token)

	t.Setenv("HARDCOVER_TOKEN", "env-token")
	t.Setenv("AUDIOBOOKSHELF_URL", "https://abs.example/")
	t.Setenv("AUDIOBOOKSHELF_NETWORK_TRUST", "public_only")
	cfg, err := LoadForTool(path)
	require.NoError(t, err)
	assert.Equal(t, "env-token", cfg.Hardcover.Token)
	assert.Equal(t, "public_only", cfg.Audiobookshelf.NetworkTrust)
	assert.Equal(t, "https://abs.example", cfg.Audiobookshelf.URL)

	t.Setenv("AUDIOBOOKSHELF_URL", "http://abs.example")
	_, err = LoadForTool(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must use https")

	t.Setenv("AUDIOBOOKSHELF_NETWORK_TRUST", "trust_everything")
	_, err = LoadForTool(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audiobookshelf.network_trust")

	t.Setenv("AUDIOBOOKSHELF_URL", "")
	t.Setenv("AUDIOBOOKSHELF_NETWORK_TRUST", "")
	cfg, err = LoadForTool("")
	require.NoError(t, err, "Hardcover-only environment configuration needs no Audiobookshelf settings")
	assert.Equal(t, "env-token", cfg.Hardcover.Token)
	assert.Empty(t, cfg.Audiobookshelf.URL)
	assert.Equal(t, "allow_private", cfg.Audiobookshelf.NetworkTrust)

	_, err = LoadForTool(filepath.Join(t.TempDir(), "missing.yaml"))
	require.Error(t, err, "a named configuration file must exist")
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = original }()
	fn()
	require.NoError(t, writer.Close())
	output, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(output)
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
