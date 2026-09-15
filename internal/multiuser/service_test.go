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

func TestGetProfileStatusRechecksStatusAfterFallbackLookup(t *testing.T) {
	for _, tt := range []struct {
		name          string
		blockedSchema string
		initialStatus *SyncProfileStatus
	}{
		{name: "profile lookup", blockedSchema: "SyncProfile"},
		{
			name:          "state lookup",
			blockedSchema: "ProfileSyncState",
			initialStatus: &SyncProfileStatus{
				ProfileID: "profile-a", ProfileName: "Stored profile", Status: "completed",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service, db := newStatusLookupService(t)
			profileID := "profile-a"
			require.NoError(t, service.repository.CreateProfile(
				profileID, "Stored profile", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
			))
			if tt.initialStatus != nil {
				service.updateProfileStatus(profileID, tt.initialStatus)
			}

			lookupStarted := make(chan struct{})
			releaseLookup := make(chan struct{})
			var blockOnce sync.Once
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(releaseLookup) }) })
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
				"multiuser_test_block_fallback_lookup", func(tx *gorm.DB) {
					if tx.Statement.Schema == nil || tx.Statement.Schema.Name != tt.blockedSchema {
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
				t.Fatalf("timed out waiting for %s", tt.name)
			}

			// A new run must be able to publish its status while fallback I/O is
			// blocked, and that status must win when the lookup completes.
			run := activeSyncRun{generation: 1, runID: "profile-a-run-1", startedAt: time.Now().UTC()}
			_, cancel := context.WithCancel(context.Background())
			defer cancel()
			installDone := make(chan struct{})
			go func() {
				service.syncMutex.Lock()
				service.nextGeneration = run.generation
				service.activeRuns[profileID] = run
				service.activeSyncs[profileID] = cancel
				currentStatus := &SyncProfileStatus{ProfileID: profileID, ProfileName: "Current profile", Status: "syncing"}
				applySnapshotToStatus(currentStatus, newRunSnapshot(profileID, run, "syncing"))
				service.updateProfileStatus(profileID, currentStatus)
				service.syncMutex.Unlock()
				close(installDone)
			}()
			select {
			case <-installDone:
			case <-time.After(time.Second):
				releaseOnce.Do(func() { close(releaseLookup) })
				<-installDone
				t.Fatalf("sync lock remained held during %s", tt.name)
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
		})
	}
}

func TestStatusAggregateOmitsErrorButProfileStatusRetainsIt(t *testing.T) {
	service, _ := newStatusLookupService(t)
	profileID := "profile-a"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Profile A", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))

	lastSync := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	status := &SyncProfileStatus{
		ProfileID:   profileID,
		ProfileName: "Profile A",
		Status:      "error",
		LastSync:    &lastSync,
		Error:       "upstream response: sensitive details",
		Progress:    "Processing books",
		BooksTotal:  12,
		BooksSynced: 7,
	}
	service.updateProfileStatus(profileID, status)

	aggregate, err := service.GetAllProfileStatuses()
	require.NoError(t, err)
	require.Len(t, aggregate, 1)
	require.Equal(t, profileID, aggregate[0].ProfileID)
	require.Equal(t, status.ProfileName, aggregate[0].ProfileName)
	require.Equal(t, status.Status, aggregate[0].Status)
	require.Equal(t, status.LastSync, aggregate[0].LastSync)
	require.Equal(t, status.Progress, aggregate[0].Progress)
	require.Equal(t, status.BooksTotal, aggregate[0].BooksTotal)
	require.Equal(t, status.BooksSynced, aggregate[0].BooksSynced)
	require.Empty(t, aggregate[0].Error)

	direct := service.GetProfileStatus(profileID)
	require.NotNil(t, direct)
	require.Equal(t, status.Error, direct.Error)
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
