package multiuser

import (
	"context"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/crypto"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

func TestGetProfileStatusRechecksStatusAfterProfileLookup(t *testing.T) {
	service, db := newStatusLookupService(t)
	profileID := "profile-a"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Stored profile", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))

	lookupStarted := make(chan struct{})
	releaseLookup := make(chan struct{})
	var blockOnce sync.Once
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseLookup) }) })
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		"multiuser_test_block_profile_lookup", func(tx *gorm.DB) {
			if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "SyncProfile" {
				return
			}
			blockOnce.Do(func() { close(lookupStarted) })
			<-releaseLookup
		},
	))

	statusResult := make(chan *SyncProfileStatus, 1)
	go func() { statusResult <- service.GetProfileStatus(profileID) }()
	select {
	case <-lookupStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for profile lookup")
	}

	// A new run must be able to publish its status while the fallback lookup
	// is blocked, and that status must win when the lookup completes.
	run := activeSyncRun{generation: 1, runID: "profile-a-run-1", startedAt: time.Now().UTC()}
	_, cancel := context.WithCancel(context.Background())
	installDone := make(chan struct{})
	go func() {
		service.syncMutex.Lock()
		service.nextGeneration = run.generation
		service.activeRuns[profileID] = run
		service.activeSyncs[profileID] = cancel
		initialStatus := &SyncProfileStatus{ProfileID: profileID, ProfileName: "Current profile", Status: "syncing"}
		applySnapshotToStatus(initialStatus, newRunSnapshot(profileID, run, "syncing"))
		service.updateProfileStatus(profileID, initialStatus)
		service.syncMutex.Unlock()
		close(installDone)
	}()
	select {
	case <-installDone:
	case <-time.After(time.Second):
		releaseOnce.Do(func() { close(releaseLookup) })
		<-installDone
		t.Fatal("sync lock remained held during profile lookup")
	}

	releaseOnce.Do(func() { close(releaseLookup) })
	select {
	case status := <-statusResult:
		require.NotNil(t, status)
		require.Equal(t, "syncing", status.Status)
		require.NotNil(t, status.Snapshot)
		require.Equal(t, run.runID, status.Snapshot.RunID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for profile status")
	}
	cancel()
}

func TestGetProfileStatusRechecksStatusAfterStateLookup(t *testing.T) {
	service, db := newStatusLookupService(t)
	profileID := "profile-a"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Stored profile", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))
	service.updateProfileStatus(profileID, &SyncProfileStatus{
		ProfileID: profileID, ProfileName: "Stored profile", Status: "completed",
	})

	lookupStarted := make(chan struct{})
	releaseLookup := make(chan struct{})
	var blockOnce sync.Once
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseLookup) }) })
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		"multiuser_test_block_state_lookup", func(tx *gorm.DB) {
			if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "ProfileSyncState" {
				return
			}
			blockOnce.Do(func() { close(lookupStarted) })
			<-releaseLookup
		},
	))

	statusResult := make(chan *SyncProfileStatus, 1)
	go func() { statusResult <- service.GetProfileStatus(profileID) }()
	select {
	case <-lookupStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for state lookup")
	}

	run := activeSyncRun{generation: 1, runID: "profile-a-run-1", startedAt: time.Now().UTC()}
	_, cancel := context.WithCancel(context.Background())
	installDone := make(chan struct{})
	go func() {
		service.syncMutex.Lock()
		service.nextGeneration = run.generation
		service.activeRuns[profileID] = run
		service.activeSyncs[profileID] = cancel
		initialStatus := &SyncProfileStatus{ProfileID: profileID, ProfileName: "Current profile", Status: "syncing"}
		applySnapshotToStatus(initialStatus, newRunSnapshot(profileID, run, "syncing"))
		service.updateProfileStatus(profileID, initialStatus)
		service.syncMutex.Unlock()
		close(installDone)
	}()
	select {
	case <-installDone:
	case <-time.After(time.Second):
		releaseOnce.Do(func() { close(releaseLookup) })
		<-installDone
		t.Fatal("sync lock remained held during state lookup")
	}

	releaseOnce.Do(func() { close(releaseLookup) })
	select {
	case status := <-statusResult:
		require.NotNil(t, status)
		require.Equal(t, "syncing", status.Status)
		require.NotNil(t, status.Snapshot)
		require.Equal(t, run.runID, status.Snapshot.RunID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for profile status")
	}
	cancel()
}

func newStatusLookupService(t *testing.T) (*MultiUserService, *gorm.DB) {
	t.Helper()
	logger.ForceSetup(logger.Config{Level: "error", Format: logger.FormatJSON, Output: io.Discard})
	dataDir := t.TempDir()
	db, err := database.NewDatabase(&database.DatabaseConfig{
		Type: database.DatabaseTypeSQLite,
		Path: filepath.Join(dataDir, "multiuser-status-test.db"),
	}, logger.Get())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	encryptor, err := crypto.NewEncryptionManagerWithDataDir(dataDir, logger.Get())
	require.NoError(t, err)
	repo := database.NewRepository(db, encryptor, logger.Get())
	return NewMultiUserService(repo, config.DefaultConfig(), logger.Get()), db.GetDB()
}
