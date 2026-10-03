package database

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/crypto"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/require"
)

func TestEditionActionJournalSurvivesDatabaseReopenEncryptedAndScoped(t *testing.T) {
	config := &DatabaseConfig{Type: DatabaseTypeSQLite, Path: filepath.Join(t.TempDir(), "journal.db")}
	key := bytes.Repeat([]byte{42}, 32)
	encryptor, err := crypto.NewEncryptionManagerWithKey(key, logger.Get())
	require.NoError(t, err)
	db, err := NewDatabase(config, logger.Get())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewRepository(db, encryptor, logger.Get())
	pending := []byte(`{"outcome":"unconfirmed","data":{"recovery_token":"signed-test-recovery"}}`)
	for _, scope := range [][3]string{
		{"profile-a", "run-1", "book-1"},
		{"profile-a", "run-2", "book-1"},
		{"profile-b", "run-1", "book-1"},
	} {
		require.NoError(t, repo.SaveEditionAction(scope[0], scope[1], scope[2], pending))
	}
	var stored EditionActionJournal
	require.NoError(t, db.GetDB().Where("profile_id = ? AND run_id = ? AND abs_item_id = ?", "profile-a", "run-1", "book-1").First(&stored).Error)
	require.NotContains(t, stored.PayloadEncrypted, "signed-test-recovery")
	require.NotContains(t, stored.PayloadEncrypted, "unconfirmed")
	require.NoError(t, db.Close())

	reopened, err := NewDatabase(config, logger.Get())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	newEncryptor, err := crypto.NewEncryptionManagerWithKey(key, logger.Get())
	require.NoError(t, err)
	reloaded := NewRepository(reopened, newEncryptor, logger.Get())
	payload, found, err := reloaded.GetEditionAction("profile-a", "run-1", "book-1")
	require.NoError(t, err)
	require.True(t, found)
	require.JSONEq(t, string(pending), string(payload))

	final := []byte(`{"outcome":"failed","error":"Final import failure"}`)
	require.NoError(t, reloaded.SaveEditionAction("profile-a", "run-1", "book-1", final))
	actions, err := reloaded.ListEditionActionsForRun("profile-a", "run-1")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.JSONEq(t, string(final), string(actions["book-1"]))
	for _, scope := range [][3]string{{"profile-a", "run-2", "book-1"}, {"profile-b", "run-1", "book-1"}} {
		payload, found, err = reloaded.GetEditionAction(scope[0], scope[1], scope[2])
		require.NoError(t, err)
		require.True(t, found)
		require.JSONEq(t, string(pending), string(payload))
	}
	for _, scope := range [][3]string{{"profile-c", "run-1", "book-1"}, {"profile-a", "missing-run", "book-1"}, {"profile-a", "run-1", "other-book"}} {
		_, found, err = reloaded.GetEditionAction(scope[0], scope[1], scope[2])
		require.NoError(t, err)
		require.False(t, found)
	}
}

func TestEditionActionJournalCleanupPreservesOtherProfilesAndItems(t *testing.T) {
	db, repo := newRepositoryForTest(t)
	encryptor, err := crypto.NewEncryptionManagerWithKey(bytes.Repeat([]byte{42}, 32), logger.Get())
	require.NoError(t, err)
	repo.encryptor = encryptor
	createTestProfile(t, db, "profile-a")
	createTestProfile(t, db, "profile-b")
	for _, scope := range [][3]string{
		{"profile-a", "run-1", "book-1"},
		{"profile-a", "run-2", "book-1"},
		{"profile-a", "run-1", "book-2"},
		{"profile-b", "run-1", "book-1"},
	} {
		require.NoError(t, repo.SaveEditionAction(scope[0], scope[1], scope[2], []byte(`{"outcome":"transport_unknown"}`)))
	}
	require.NoError(t, repo.DeleteEditionActionsForProfileItem("profile-a", "book-1"))
	for _, runID := range []string{"run-1", "run-2"} {
		_, found, err := repo.GetEditionAction("profile-a", runID, "book-1")
		require.NoError(t, err)
		require.False(t, found)
	}
	actions, err := repo.ListEditionActionsForRun("profile-a", "run-1")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Contains(t, actions, "book-2")
	require.NoError(t, repo.DeleteProfile("profile-a"))
	actions, err = repo.ListEditionActionsForRun("profile-a", "run-1")
	require.NoError(t, err)
	require.Empty(t, actions)
	_, found, err := repo.GetEditionAction("profile-b", "run-1", "book-1")
	require.NoError(t, err)
	require.True(t, found)
}

func TestEditionActionJournalFollowsTerminalRunRetention(t *testing.T) {
	db, repo := newRepositoryForTest(t)
	encryptor, err := crypto.NewEncryptionManagerWithKey(bytes.Repeat([]byte{42}, 32), logger.Get())
	require.NoError(t, err)
	repo.encryptor = encryptor
	createTestProfile(t, db, "profile-a")
	createTestProfile(t, db, "profile-b")
	repo.SetSyncRunReportRetention(1)
	payload := []byte(`{"outcome":"unconfirmed"}`)
	require.NoError(t, repo.SaveEditionAction("profile-a", "old", "book-1", payload))
	require.NoError(t, repo.SaveEditionAction("profile-b", "old", "book-1", payload))
	require.NoError(t, repo.UpsertSyncRunReportContext(context.Background(), &SyncRunReport{
		ProfileID: "profile-a", RunID: "old", Generation: 1, Phase: SyncRunPhaseFailed, SnapshotJSON: "{}",
	}))
	require.NoError(t, repo.SaveEditionAction("profile-a", "new", "book-1", payload))
	require.NoError(t, repo.UpsertSyncRunReportContext(context.Background(), &SyncRunReport{
		ProfileID: "profile-a", RunID: "new", Generation: 2, Phase: SyncRunPhaseFailed, SnapshotJSON: "{}",
	}))
	_, found, err := repo.GetEditionAction("profile-a", "old", "book-1")
	require.NoError(t, err)
	require.False(t, found)
	for _, scope := range [][3]string{{"profile-a", "new", "book-1"}, {"profile-b", "old", "book-1"}} {
		_, found, err = repo.GetEditionAction(scope[0], scope[1], scope[2])
		require.NoError(t, err)
		require.True(t, found)
	}
}
