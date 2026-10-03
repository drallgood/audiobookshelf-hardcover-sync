package database

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/crypto"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/require"
)

func newRepositoryWithEncryptionForTest(t *testing.T) *Repository {
	t.Helper()
	dataDir := t.TempDir()
	db, err := NewDatabase(&DatabaseConfig{
		Type: DatabaseTypeSQLite,
		Path: filepath.Join(dataDir, "sync.db"),
	}, logger.Get())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	encryptor, err := crypto.NewEncryptionManagerWithDataDir(dataDir, logger.Get())
	require.NoError(t, err)
	return NewRepository(db, encryptor, logger.Get())
}

func TestMigrateFromSingleUserConfigCarriesAudnexusRegion(t *testing.T) {
	t.Setenv("SYNC_OWNERSHIP_RECHECK_DAYS", "")
	repo := newRepositoryWithEncryptionForTest(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
server:
  enable_web_ui: true
audiobookshelf:
  url: "http://audiobookshelf.invalid"
  token: "abs-token"
  audnexus_region: "fr"
hardcover:
  token: "hc-token"
sync:
  ownership_recheck_days: 0
`), 0o600))

	require.NoError(t, NewMigrationManager(repo, logger.Get()).MigrateFromSingleUserConfig(configPath))

	profile, err := repo.GetProfile("default")
	require.NoError(t, err)
	require.NotNil(t, profile)
	require.Equal(t, "fr", profile.SyncConfig.AudnexusRegion,
		"the legacy audnexus_region must be carried into the default profile")
	require.NotNil(t, profile.SyncConfig.OwnershipRecheckDays)
	require.Equal(t, 0, *profile.SyncConfig.OwnershipRecheckDays,
		"an explicit zero must survive single-user profile migration")
}
