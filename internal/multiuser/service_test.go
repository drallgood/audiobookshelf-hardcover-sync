package multiuser

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/crypto"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	syncsvc "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
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

func TestStatusAggregateOmitsErrorAndProfileStatusRetainsIt(t *testing.T) {
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
		Snapshot: &syncsvc.SyncSnapshot{
			RunID:          "profile-a-run-1",
			State:          "failed",
			BooksTotal:     12,
			ProcessedSoFar: 1,
			ProcessedCount: 1,
			OutcomeCounts:  syncsvc.OutcomeCounts{Failed: 1},
			BookOutcomes: []syncsvc.BookOutcomeRecord{{
				BookID:  "book-1",
				Outcome: syncsvc.OutcomeFailed,
			}},
			AttentionRecords: []syncsvc.BookOutcomeRecord{{
				BookID:  "book-1",
				Outcome: syncsvc.OutcomeFailed,
			}},
		},
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
	require.NotNil(t, aggregate[0].Snapshot)
	require.Equal(t, status.Snapshot.RunID, aggregate[0].Snapshot.RunID)
	require.Equal(t, status.Snapshot.OutcomeCounts, aggregate[0].Snapshot.OutcomeCounts)
	require.Nil(t, aggregate[0].Snapshot.BookOutcomes)
	require.Nil(t, aggregate[0].Snapshot.AttentionRecords)
	require.Nil(t, aggregate[0].Snapshot.BooksNotFound)
	require.Nil(t, aggregate[0].Snapshot.Mismatches)

	direct := service.GetProfileStatus(profileID)
	require.NotNil(t, direct)
	require.Equal(t, status.Error, direct.Error)
}

func TestAggregateStatusMapsLiveTerminalSnapshotState(t *testing.T) {
	for _, test := range []struct {
		name          string
		librariesCode int
		wantStatus    string
		wantState     string
		wantSyncError bool
	}{
		{name: "completed", librariesCode: http.StatusOK, wantStatus: "completed", wantState: "completed"},
		{name: "failed", librariesCode: http.StatusInternalServerError, wantStatus: "error", wantState: "failed", wantSyncError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newStatusLookupService(t)
			profileID := "profile-" + test.name
			require.NoError(t, service.repository.CreateProfile(
				profileID, "Profile", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
			))

			absServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/me":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{}`))
				case "/api/libraries":
					if test.librariesCode != http.StatusOK {
						w.WriteHeader(test.librariesCode)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"libraries":[]}`))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(absServer.Close)

			dataDir := t.TempDir()
			cfg := config.DefaultConfig()
			cfg.Audiobookshelf.URL = absServer.URL
			cfg.Audiobookshelf.Token = "abs-token"
			cfg.Sync.StateFile = filepath.Join(dataDir, "state.json")
			cfg.Paths.CacheDir = filepath.Join(dataDir, "cache")
			cfg.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
			hcConfig := hardcover.DefaultClientConfig()
			hcConfig.BaseURL = "http://hardcover.invalid"
			liveService, err := syncsvc.NewService(
				audiobookshelf.NewClient(absServer.URL, "abs-token"),
				hardcover.NewClientWithConfig(hcConfig, "hc-token", logger.Get()),
				cfg,
			)
			require.NoError(t, err)

			run := activeSyncRun{generation: 1, runID: profileID + "-run", startedAt: time.Now().UTC()}
			service.syncMutex.Lock()
			service.activeRuns[profileID] = run
			service.syncMutex.Unlock()
			require.True(t, service.registerSyncService(profileID, run.generation, liveService))

			syncErr := liveService.Sync(context.Background())
			if test.wantSyncError {
				require.Error(t, syncErr)
			} else {
				require.NoError(t, syncErr)
			}

			// Keep the completed service registered and the run current to model
			// the terminal handoff window before publication/removal.
			statuses, err := service.GetAllProfileStatuses()
			require.NoError(t, err)
			require.Len(t, statuses, 1)
			require.Equal(t, test.wantStatus, statuses[0].Status)
			require.NotNil(t, statuses[0].Snapshot)
			require.Equal(t, test.wantState, statuses[0].Snapshot.State)

			service.removeSyncService(profileID, run.generation, liveService)
			service.finishActiveRun(profileID, run.generation)
		})
	}
}

func TestCreateProfileValidatesComposedStateFilenameLength(t *testing.T) {
	for _, test := range []struct {
		name           string
		profileID      string
		configuredPath string
		wantBaseBytes  int
		wantError      bool
	}{
		{name: "default 255 bytes", profileID: strings.Repeat("d", 244), wantBaseBytes: 255},
		{name: "default 256 bytes", profileID: strings.Repeat("d", 245), wantBaseBytes: 256, wantError: true},
		{name: "custom 255 bytes", profileID: "id", configuredPath: strings.Repeat("c", 252) + ".json", wantBaseBytes: 255},
		{name: "custom 256 bytes", profileID: "id", configuredPath: strings.Repeat("c", 253) + ".json", wantBaseBytes: 256, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newStatusLookupService(t)
			service.globalConfig.Paths.DataDir = t.TempDir()
			derivedPath := service.profileSpecificStatePath(test.profileID, test.configuredPath)
			require.Len(t, []byte(filepath.Base(derivedPath)), test.wantBaseBytes)
			err := service.CreateProfile(
				test.profileID,
				"Profile",
				"http://audiobookshelf",
				"abs-token",
				"hc-token",
				database.SyncConfigData{StateFile: test.configuredPath},
			)
			if test.wantError {
				require.ErrorIs(t, err, ErrProfileStateFileNameTooLong)
				profile, getErr := service.GetProfile(test.profileID)
				require.NoError(t, getErr)
				require.Nil(t, profile)
				return
			}
			require.NoError(t, err)
			profile, getErr := service.GetProfile(test.profileID)
			require.NoError(t, getErr)
			require.NotNil(t, profile)
		})
	}
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
