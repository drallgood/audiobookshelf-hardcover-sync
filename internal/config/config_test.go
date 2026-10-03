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
	t.Setenv("AUDIOBOOKSHELF_URL", "")
	t.Setenv("AUDIOBOOKSHELF_TOKEN", "")
	t.Setenv("HARDCOVER_TOKEN", "")

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`audiobookshelf:
  url: https://example.com/audiobookshelf
  token: file-audiobookshelf-token
hardcover:
  token: file-hardcover-token
`), 0600))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/audiobookshelf", cfg.Audiobookshelf.URL)
	assert.Equal(t, "file-audiobookshelf-token", cfg.Audiobookshelf.Token)
	assert.Equal(t, "file-hardcover-token", cfg.Hardcover.Token)
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

func TestLoadConfigNormalizesAudnexusRegionAfterLayering(t *testing.T) {
	for _, test := range []struct {
		name string
		env  string
		yaml string
		want string
	}{
		{"environment overrides YAML", " JP ", "audiobookshelf:\n  audnexus_region: de\n", "jp"},
		{"YAML is normalized", "", "audiobookshelf:\n  audnexus_region: UK\n", "uk"},
		{"unsupported environment falls back", "mx", "", "us"},
		{"omitted region defaults to empty", "", "audiobookshelf: {}\n", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AUDIOBOOKSHELF_URL", "https://example.com/audiobookshelf")
			t.Setenv("AUDIOBOOKSHELF_TOKEN", "test-audiobookshelf-token")
			t.Setenv("HARDCOVER_TOKEN", "test-hardcover-token")
			t.Setenv("AUDIOBOOKSHELF_AUDNEXUS_REGION", test.env)

			path := ""
			if test.yaml != "" {
				path = filepath.Join(t.TempDir(), "config.yaml")
				require.NoError(t, os.WriteFile(path, []byte(test.yaml), 0600))
			}
			cfg, err := Load(path)
			require.NoError(t, err)
			assert.Equal(t, test.want, cfg.Audiobookshelf.AudnexusRegion)
		})
	}
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

func TestOwnershipRecheckDaysDefaultsAndLoadsOverrides(t *testing.T) {
	t.Setenv("SYNC_OWNERSHIP_RECHECK_DAYS", "")
	cfg, err := LoadForTool("")
	require.NoError(t, err)
	assert.Equal(t, DefaultOwnershipRecheckDays, cfg.Sync.OwnershipRecheckDays)

	for _, test := range []struct {
		name string
		yaml string
		env  string
		want int
	}{
		{name: "yaml value", yaml: "sync:\n  ownership_recheck_days: 12\n", want: 12},
		{name: "yaml explicit zero", yaml: "sync:\n  ownership_recheck_days: 0\n", want: 0},
		{name: "environment overrides yaml", yaml: "sync:\n  ownership_recheck_days: 12\n", env: "0", want: 0},
		{name: "negative falls back", yaml: "sync:\n  ownership_recheck_days: -1\n", want: DefaultOwnershipRecheckDays},
		{name: "duration overflow falls back", yaml: "sync:\n  ownership_recheck_days: 106752\n", want: DefaultOwnershipRecheckDays},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SYNC_OWNERSHIP_RECHECK_DAYS", test.env)
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(test.yaml), 0o600))
			cfg, err := LoadForTool(path)
			require.NoError(t, err)
			assert.Equal(t, test.want, cfg.Sync.OwnershipRecheckDays)
		})
	}
}
