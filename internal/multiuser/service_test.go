package multiuser

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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
	statepkg "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
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

func TestGetProfileSnapshotUsesCurrentRunWithoutProfileHydration(t *testing.T) {
	service, db := newStatusLookupService(t)
	profileID := "profile-a"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Profile A", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))

	const queryCallbackName = "multiuser_test_forbid_snapshot_profile_hydration"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(queryCallbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "SyncProfile" {
			return
		}
		t.Errorf("GetProfileSnapshot hydrated profile metadata from the database")
		require.ErrorIs(t, tx.AddError(gorm.ErrInvalidValue), gorm.ErrInvalidValue)
	}))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove(queryCallbackName))
	})

	cfg := config.DefaultConfig()
	cfg.Sync.StateFile = filepath.Join(t.TempDir(), "state.json")
	cfg.Paths.CacheDir = filepath.Join(t.TempDir(), "cache")
	cfg.Paths.MismatchOutputDir = filepath.Join(t.TempDir(), "mismatches")
	hcConfig := hardcover.DefaultClientConfig()
	hcConfig.BaseURL = "http://hardcover.invalid"
	liveService, err := syncsvc.NewService(
		audiobookshelf.NewClient("http://audiobookshelf.invalid", "abs-token"),
		hardcover.NewClientWithConfig(hcConfig, "hc-token", logger.Get()),
		cfg,
	)
	require.NoError(t, err)

	run := activeSyncRun{generation: 1, runID: "profile-a-run-1", startedAt: time.Now().UTC()}
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	service.syncMutex.Lock()
	service.nextGeneration = run.generation
	service.activeRuns[profileID] = run
	service.activeSyncs[profileID] = cancel
	service.syncMutex.Unlock()
	require.True(t, service.registerSyncService(profileID, run.generation, liveService))
	t.Cleanup(func() {
		service.removeSyncService(profileID, run.generation, liveService)
		service.finishActiveRun(profileID, run.generation)
	})

	snapshot := service.GetProfileSnapshot(profileID)
	require.NotNil(t, snapshot)
	require.Equal(t, run.runID, snapshot.RunID)
	require.Equal(t, profileID, snapshot.UserID)
	require.Equal(t, "syncing", snapshot.State)
}

func TestGetProfileSnapshotRestoresRetainedRunWithoutProfileHydration(t *testing.T) {
	service, db := newStatusLookupService(t)
	profileID := "profile-retained"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Profile", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))
	queuedAt := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	report, err := service.repository.ReserveSyncRun(profileID, "run-retained", false, queuedAt)
	require.NoError(t, err)
	report.Phase = database.SyncRunPhaseCompleted
	report.FinishedAt = timePtrForMultiuserTest(queuedAt.Add(time.Minute))
	report.SnapshotJSON = `{"run_id":"run-retained","state":"completed"}`
	require.NoError(t, service.repository.UpsertSyncRunReport(report))

	const queryCallbackName = "multiuser_test_forbid_retained_snapshot_profile_hydration"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(queryCallbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "SyncProfile" {
			return
		}
		t.Errorf("GetProfileSnapshot hydrated profile metadata from the database")
		require.ErrorIs(t, tx.AddError(gorm.ErrInvalidValue), gorm.ErrInvalidValue)
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(queryCallbackName)) })

	snapshot := service.GetProfileSnapshot(profileID)
	require.NotNil(t, snapshot)
	require.Equal(t, profileID, snapshot.UserID)
	require.Equal(t, "run-retained", snapshot.RunID)
	require.Equal(t, string(syncsvc.RunPhaseCompleted), snapshot.State)
}

func TestGetSyncRunSnapshotLooksUpOnlyTheRequestedRetainedRun(t *testing.T) {
	service, _ := newStatusLookupService(t)
	const profileID = "profile-exact-report"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Profile", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))
	queuedAt := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	report, err := service.repository.ReserveSyncRun(profileID, "run-exact", false, queuedAt)
	require.NoError(t, err)
	report.Phase = database.SyncRunPhaseFailed
	report.FinishedAt = timePtrForMultiuserTest(queuedAt.Add(time.Minute))
	report.RunError = "retained failure"
	report.SnapshotJSON = `{"run_id":"run-exact","state":"failed"}`
	require.NoError(t, service.repository.UpsertSyncRunReport(report))

	snapshot, err := service.GetSyncRunSnapshot(profileID, "run-exact")
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	require.Equal(t, profileID, snapshot.UserID)
	require.Equal(t, "run-exact", snapshot.RunID)
	require.Equal(t, string(syncsvc.RunPhaseFailed), snapshot.State)
	require.Equal(t, "retained failure", snapshot.RunError)

	missing, err := service.GetSyncRunSnapshot(profileID, "does-not-exist")
	require.NoError(t, err)
	require.Nil(t, missing)
}

func timePtrForMultiuserTest(value time.Time) *time.Time {
	return &value
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

func TestAggregateStatusPreservesUnknownBooksTotalFromExplicitSnapshot(t *testing.T) {
	service, _ := newStatusLookupService(t)
	profileID := "profile-a"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Profile A", "http://audiobookshelf.invalid", "abs-token", "hc-token", database.SyncConfigData{},
	))

	snapshot := syncsvc.SyncSnapshot{
		UserID:         profileID,
		RunID:          "profile-a-run-1",
		State:          "syncing",
		BooksTotal:     0,
		ProcessedSoFar: 3,
		ProcessedCount: 3,
		OutcomeCounts:  syncsvc.OutcomeCounts{NotFound: 3},
	}
	service.updateProfileStatus(profileID, &SyncProfileStatus{
		ProfileID:   profileID,
		ProfileName: "Profile A",
		Status:      "syncing",
		BooksTotal:  0,
		Snapshot:    &snapshot,
	})

	statuses, err := service.GetAllProfileStatuses()
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	require.Zero(t, statuses[0].BooksTotal)
	require.NotNil(t, statuses[0].Snapshot)
	require.Zero(t, statuses[0].Snapshot.BooksTotal)
	require.Equal(t, int32(3), statuses[0].Snapshot.ProcessedSoFar)
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

func installAcceptedTestRun(t *testing.T, service *MultiUserService, profileID, runID string, dryRun bool) activeSyncRun {
	t.Helper()
	queuedAt := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	report, err := service.repository.ReserveSyncRun(profileID, runID, dryRun, queuedAt)
	require.NoError(t, err)
	run := activeSyncRun{
		generation:  report.Generation,
		runID:       runID,
		startedAt:   queuedAt,
		profileName: profileID,
		dryRun:      dryRun,
	}
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	service.syncMutex.Lock()
	service.activeSyncs[profileID] = cancel
	service.activeRuns[profileID] = run
	service.latestRuns[profileID] = run
	service.syncMutex.Unlock()
	return run
}

func acceptedTerminalStatus(profileID string, run activeSyncRun, phase string) *SyncProfileStatus {
	snapshot := newRunSnapshot(profileID, run, phase)
	snapshot.FinishedAt = run.startedAt.Add(time.Minute)
	return &SyncProfileStatus{
		ProfileID:       profileID,
		Status:          "completed",
		DryRun:          run.dryRun,
		LastAttemptedAt: timeValue(run.startedAt),
		Snapshot:        &snapshot,
	}
}

func TestStartSyncWithAcceptedRunMatchesQueuedDurableAndStatusIdentity(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	absServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-requestStarted:
		default:
			close(requestStarted)
		}
		<-releaseRequest
	}))
	t.Cleanup(absServer.Close)

	service, _ := newStatusLookupService(t)
	dataDir := t.TempDir()
	service.globalConfig.Paths.DataDir = dataDir
	service.globalConfig.Paths.CacheDir = filepath.Join(dataDir, "cache")
	service.globalConfig.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	const profileID = "profile-queued"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Queued profile", absServer.URL, "abs-token", "hc-token", database.SyncConfigData{},
	))

	accepted, err := service.StartSyncWithAcceptedRun(profileID)
	require.NoError(t, err)
	require.NotEmpty(t, accepted.RunID)
	require.Equal(t, string(syncsvc.RunPhaseQueued), accepted.State)
	require.False(t, accepted.DryRun)
	require.Equal(t, accepted.QueuedAt, accepted.RunStartedAt)
	report, err := service.repository.GetLatestSyncRunReport(profileID)
	require.NoError(t, err)
	require.NotNil(t, report)
	require.Equal(t, database.SyncRunPhaseQueued, report.Phase)
	require.Equal(t, accepted.RunID, report.RunID)
	require.Equal(t, accepted.QueuedAt, *report.QueuedAt)
	require.Contains(t, report.SnapshotJSON, accepted.RunID)

	status := service.GetProfileStatus(profileID)
	require.NotNil(t, status)
	require.NotNil(t, status.Snapshot)
	require.Equal(t, accepted.RunID, status.Snapshot.RunID)
	require.Equal(t, uint64(report.Generation), service.nextGeneration)

	require.NoError(t, service.CancelSync(profileID))
	close(releaseRequest)
	service.WaitForSyncs()
}

func TestStartSyncWithAcceptedRunRejectsAtomicAcceptanceFailure(t *testing.T) {
	service, db := newStatusLookupService(t)
	const profileID = "profile-accept-failure"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Accept failure", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))

	writeErr := errors.New("queued report write unavailable")
	const callbackName = "multiuser_test_fail_accepted_report_create"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "SyncRunReport" {
			require.ErrorIs(t, tx.AddError(writeErr), writeErr)
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callbackName)) })

	_, err := service.StartSyncWithAcceptedRun(profileID)
	require.ErrorIs(t, err, writeErr)
	require.False(t, service.IsProfileSyncing(profileID))
	report, reportErr := service.repository.GetLatestSyncRunReport(profileID)
	require.NoError(t, reportErr)
	require.Nil(t, report)
	state, stateErr := service.repository.GetSyncState(profileID)
	require.NoError(t, stateErr)
	require.Zero(t, state.RunGeneration)
	require.Nil(t, state.LastAttemptedAt)
	require.Empty(t, state.LastAttemptedRunID)
}

func TestStartSyncReturnsTypedProfileAndActiveErrors(t *testing.T) {
	service, _ := newStatusLookupService(t)
	_, err := service.StartSyncWithAcceptedRun("missing-profile")
	require.ErrorIs(t, err, ErrProfileNotFound)

	const profileID = "profile-active"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Active profile", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))
	installAcceptedTestRun(t, service, profileID, "run-active", false)
	_, err = service.StartSyncWithAcceptedRun(profileID)
	require.ErrorIs(t, err, ErrSyncAlreadyActive)
}

func TestStartSyncWithAcceptedRunKeepsIdentityWhenWorkerFinishesImmediately(t *testing.T) {
	absServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			_, _ = io.WriteString(w, `{"mediaProgress":[],"listeningSessions":[]}`)
		case "/api/libraries":
			_, _ = io.WriteString(w, `{"libraries":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(absServer.Close)

	service, _ := newStatusLookupService(t)
	dataDir := t.TempDir()
	service.globalConfig.Paths.DataDir = dataDir
	service.globalConfig.Paths.CacheDir = filepath.Join(dataDir, "cache")
	service.globalConfig.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	const profileID = "profile-immediate"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Immediate profile", absServer.URL, "abs-token", "hc-token", database.SyncConfigData{},
	))

	accepted, err := service.StartSyncWithAcceptedRun(profileID)
	require.NoError(t, err)
	service.WaitForSyncs()

	report, err := service.repository.GetSyncRunReport(profileID, accepted.RunID)
	require.NoError(t, err)
	require.NotNil(t, report)
	require.Equal(t, accepted.RunID, report.RunID)
	require.Equal(t, accepted.QueuedAt, *report.QueuedAt)
	status := service.GetProfileStatus(profileID)
	require.NotNil(t, status)
	require.NotNil(t, status.Snapshot)
	require.Equal(t, accepted.RunID, status.Snapshot.RunID)
	require.Equal(t, accepted.QueuedAt, status.Snapshot.QueuedAt)
}

func TestPublishFinalStatusRejectsCanceledRunBeforeDurableSuccess(t *testing.T) {
	service, _ := newStatusLookupService(t)
	profileID := "profile-a"
	require.NoError(t, service.repository.CreateProfile(profileID, "Profile A", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{}))
	run := installAcceptedTestRun(t, service, profileID, "run-a", false)

	// Make cancellation win the lifecycle decision before a worker that was
	// already returning a completed snapshot reaches final publication.
	require.NoError(t, service.CancelSync(profileID))
	require.False(t, service.publishFinalStatus(profileID, run.generation, acceptedTerminalStatus(profileID, run, string(syncsvc.RunPhaseCompleted))))
	require.False(t, service.publishFinalStatus(profileID, run.generation, acceptedTerminalStatus(profileID, run, string(syncsvc.RunPhaseFailed))))
	stored, err := service.repository.GetSyncRunReport(profileID, run.runID)
	require.NoError(t, err)
	require.Equal(t, database.SyncRunPhaseCanceled, stored.Phase)
	state, err := service.repository.GetSyncState(profileID)
	require.NoError(t, err)
	require.Zero(t, state.LastSuccessfulGeneration)
}

func TestPublishFinalStatusRejectsReplacedRunBeforeDurableSuccess(t *testing.T) {
	service, _ := newStatusLookupService(t)
	profileID := "profile-a"
	require.NoError(t, service.repository.CreateProfile(profileID, "Profile A", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{}))
	oldRun := installAcceptedTestRun(t, service, profileID, "run-old", false)
	newRun := installAcceptedTestRun(t, service, profileID, "run-new", false)

	require.False(t, service.publishFinalStatus(profileID, oldRun.generation, acceptedTerminalStatus(profileID, oldRun, string(syncsvc.RunPhaseCompleted))))
	oldReport, err := service.repository.GetSyncRunReport(profileID, oldRun.runID)
	require.NoError(t, err)
	require.Equal(t, database.SyncRunPhaseQueued, oldReport.Phase)
	state, err := service.repository.GetSyncState(profileID)
	require.NoError(t, err)
	require.Zero(t, state.LastSuccessfulGeneration)
	require.Equal(t, newRun.runID, service.latestRuns[profileID].runID)
}

func TestDryRunTerminalStatusKeepsAttemptWithoutSuccess(t *testing.T) {
	service, _ := newStatusLookupService(t)
	profileID := "profile-dry"
	require.NoError(t, service.repository.CreateProfile(profileID, "Dry profile", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{}))
	run := installAcceptedTestRun(t, service, profileID, "run-dry", true)

	require.True(t, service.publishFinalStatus(profileID, run.generation, acceptedTerminalStatus(profileID, run, string(syncsvc.RunPhaseCompleted))))
	status := service.GetProfileStatus(profileID)
	require.NotNil(t, status)
	require.Nil(t, status.LastSync)
	require.NotNil(t, status.LastAttemptedAt)
	require.Nil(t, status.LastSuccessfulAt)
	state, err := service.repository.GetSyncState(profileID)
	require.NoError(t, err)
	require.Equal(t, run.runID, state.LastAttemptedRunID)
	require.Zero(t, state.LastSuccessfulGeneration)
}

func TestRestartRestoresNewestTerminalReport(t *testing.T) {
	service, _ := newStatusLookupService(t)
	profileID := "profile-restart"
	require.NoError(t, service.repository.CreateProfile(profileID, "Restart profile", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{}))
	first := installAcceptedTestRun(t, service, profileID, "run-first", false)
	require.True(t, service.publishFinalStatus(profileID, first.generation, acceptedTerminalStatus(profileID, first, string(syncsvc.RunPhaseCompleted))))
	second := installAcceptedTestRun(t, service, profileID, "run-second", false)
	secondStatus := acceptedTerminalStatus(profileID, second, string(syncsvc.RunPhaseCanceled))
	secondStatus.Status = "error"
	require.True(t, service.publishFinalStatus(profileID, second.generation, secondStatus))

	restarted := NewMultiUserService(service.repository, config.DefaultConfig(), logger.Get())
	status := restarted.GetProfileStatus(profileID)
	require.NotNil(t, status)
	require.Equal(t, second.runID, status.Snapshot.RunID)
	require.Equal(t, string(syncsvc.RunPhaseCanceled), status.Snapshot.State)
	require.Equal(t, "error", status.Status)
}

func TestRestartRestoresNormalSyncFailureRunError(t *testing.T) {
	absServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case "/api/libraries":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(absServer.Close)

	service, _ := newStatusLookupService(t)
	dataDir := t.TempDir()
	service.globalConfig.Paths.DataDir = dataDir
	service.globalConfig.Paths.CacheDir = filepath.Join(dataDir, "cache")
	service.globalConfig.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	const profileID = "profile-normal-failure"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Normal failure", absServer.URL, "abs-token", "hc-token", database.SyncConfigData{},
	))

	accepted, err := service.StartSyncWithAcceptedRun(profileID)
	require.NoError(t, err)
	service.WaitForSyncs()

	requireFailedRunRestoration(t, service, profileID, accepted.RunID)
}

func TestRestartRestoresPreServiceSetupFailureRunError(t *testing.T) {
	service, _ := newStatusLookupService(t)
	dataDir := t.TempDir()
	service.globalConfig.Paths.DataDir = dataDir
	service.globalConfig.Paths.CacheDir = filepath.Join(dataDir, "cache")
	service.globalConfig.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	const profileID = "setup/failure"
	const stateFile = "state.json"
	legacyPath := service.legacyProfileStatePath(profileID, stateFile)
	require.NoError(t, os.MkdirAll(filepath.Dir(legacyPath), 0755))
	require.NoError(t, os.WriteFile(legacyPath, []byte(`{not valid JSON`), 0600))
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Setup failure", "http://audiobookshelf.invalid", "abs-token", "hc-token",
		database.SyncConfigData{StateFile: stateFile},
	))

	accepted, err := service.StartSyncWithAcceptedRun(profileID)
	require.NoError(t, err)
	service.WaitForSyncs()

	requireFailedRunRestoration(t, service, profileID, accepted.RunID)
}

func requireFailedRunRestoration(t *testing.T, service *MultiUserService, profileID, runID string) {
	t.Helper()
	report, err := service.repository.GetSyncRunReport(profileID, runID)
	require.NoError(t, err)
	require.NotNil(t, report)
	require.Equal(t, database.SyncRunPhaseFailed, report.Phase)
	require.NotEmpty(t, report.RunError)

	snapshot, err := service.GetSyncRunSnapshot(profileID, runID)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	require.Equal(t, report.RunError, snapshot.RunError)

	status := service.GetProfileStatus(profileID)
	require.NotNil(t, status)
	require.Equal(t, "error", status.Status)
	require.Equal(t, report.RunError, status.Error)
	require.NotNil(t, status.Snapshot)
	require.Equal(t, report.RunError, status.Snapshot.RunError)

	restarted := NewMultiUserService(service.repository, config.DefaultConfig(), logger.Get())
	restored := restarted.GetProfileStatus(profileID)
	require.NotNil(t, restored)
	require.Equal(t, "error", restored.Status)
	require.Equal(t, report.RunError, restored.Error)
	require.NotNil(t, restored.Snapshot)
	require.Equal(t, report.RunError, restored.Snapshot.RunError)
}

func TestRestartRestoresNewestTerminalWhenQueuedReportFollowsTenTerminals(t *testing.T) {
	service, _ := newStatusLookupService(t)
	const profileID = "profile-terminal-history"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Terminal history", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))
	queuedAt := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	for generation := 1; generation <= 10; generation++ {
		runID := fmt.Sprintf("terminal-%d", generation)
		report, err := service.repository.ReserveSyncRun(profileID, runID, false, queuedAt.Add(time.Duration(generation)*time.Minute))
		require.NoError(t, err)
		report.Phase = database.SyncRunPhaseCompleted
		report.FinishedAt = timePtrForMultiuserTest(queuedAt.Add(time.Duration(generation) * time.Minute))
		report.SnapshotJSON = fmt.Sprintf(`{"run_id":%q,"state":"completed"}`, runID)
		require.NoError(t, service.repository.UpsertSyncRunReport(report))
	}
	_, err := service.repository.AcceptSyncRun(&database.SyncRunReport{
		ProfileID: profileID, RunID: "queued-after-history", Phase: database.SyncRunPhaseQueued,
		QueuedAt: timePtrForMultiuserTest(queuedAt.Add(11 * time.Minute)), SnapshotJSON: `{"state":"queued"}`,
	})
	require.NoError(t, err)
	oldest, err := service.repository.GetSyncRunReport(profileID, "terminal-1")
	require.NoError(t, err)
	require.NotNil(t, oldest)

	restarted := NewMultiUserService(service.repository, config.DefaultConfig(), logger.Get())
	status := restarted.GetProfileStatus(profileID)
	require.NotNil(t, status)
	require.NotNil(t, status.Snapshot)
	require.Equal(t, "terminal-10", status.Snapshot.RunID)
	require.Equal(t, string(syncsvc.RunPhaseCompleted), status.Snapshot.State)
}

func TestPublishFinalStatusSurfacesTerminalPersistenceFailure(t *testing.T) {
	service, db := newStatusLookupService(t)
	profileID := "profile-persist-failure"
	require.NoError(t, service.repository.CreateProfile(profileID, "Failure profile", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{}))
	run := installAcceptedTestRun(t, service, profileID, "run-failure", false)

	const callbackName = "multiuser_test_fail_terminal_report_update"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "SyncRunReport" {
			require.Error(t, tx.AddError(errors.New("terminal persistence unavailable")))
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callbackName)) })

	require.False(t, service.publishFinalStatus(profileID, run.generation, acceptedTerminalStatus(profileID, run, string(syncsvc.RunPhaseCompleted))))
	status := service.GetProfileStatus(profileID)
	require.NotNil(t, status)
	require.Equal(t, "error", status.Status)
	require.Contains(t, status.Error, "failed to persist sync report")
	require.NotNil(t, status.Snapshot)
	require.Equal(t, string(syncsvc.RunPhaseFailed), status.Snapshot.State)
}

func TestBlockedProfilePersistenceDoesNotBlockOtherProfileStatus(t *testing.T) {
	service, db := newStatusLookupService(t)
	for _, profileID := range []string{"profile-a", "profile-b"} {
		require.NoError(t, service.repository.CreateProfile(profileID, profileID, "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{}))
	}
	runA := installAcceptedTestRun(t, service, "profile-a", "run-a", false)
	_ = installAcceptedTestRun(t, service, "profile-b", "run-b", false)
	lastSync := runA.startedAt.Add(-time.Minute)
	service.updateProfileStatus("profile-b", &SyncProfileStatus{
		ProfileID: "profile-b", ProfileName: "profile-b", Status: "syncing", LastSync: &lastSync,
		LastAttemptedAt: &lastSync, LastSuccessfulAt: &lastSync,
	})

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	const callbackName = "multiuser_test_block_profile_a_report"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "SyncRunReport" {
			return
		}
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		require.NoError(t, db.Callback().Update().Remove(callbackName))
	})

	persistDone := make(chan bool, 1)
	go func() {
		persistDone <- service.publishFinalStatus("profile-a", runA.generation, acceptedTerminalStatus("profile-a", runA, string(syncsvc.RunPhaseCompleted)))
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for profile A persistence")
	}
	progressDone := make(chan struct{})
	go func() {
		service.updateProfileStatus("profile-b", &SyncProfileStatus{
			ProfileID: "profile-b", ProfileName: "profile-b", Status: "syncing", Progress: "advanced while profile A persists",
			LastSync: &lastSync, LastAttemptedAt: &lastSync, LastSuccessfulAt: &lastSync,
		})
		close(progressDone)
	}()
	select {
	case <-progressDone:
	case <-time.After(time.Second):
		t.Fatal("profile B progress update blocked behind profile A persistence")
	}
	statusDone := make(chan *SyncProfileStatus, 1)
	go func() { statusDone <- service.GetProfileStatus("profile-b") }()
	select {
	case status := <-statusDone:
		require.NotNil(t, status)
		require.Equal(t, "profile-b", status.ProfileID)
		require.Equal(t, "advanced while profile A persists", status.Progress)
	case <-time.After(time.Second):
		t.Fatal("profile B status blocked behind profile A persistence")
	}
	releaseOnce.Do(func() { close(release) })
	require.True(t, <-persistDone)
}

func TestCreateProfileValidatesComposedStateFilenameLength(t *testing.T) {
	for _, test := range []struct {
		name           string
		profileID      string
		configuredPath string
		wantError      bool
	}{
		{name: "default 255 bytes", profileID: strings.Repeat("d", 244)},
		{name: "default 256 bytes", profileID: strings.Repeat("d", 245), wantError: true},
		{name: "custom 255 bytes", profileID: "id", configuredPath: strings.Repeat("c", 252) + ".json"},
		{name: "custom 256 bytes", profileID: "id", configuredPath: strings.Repeat("c", 253) + ".json", wantError: true},
		{name: "parent component 255 bytes", profileID: "id", configuredPath: filepath.Join(strings.Repeat("p", 255), "state.json")},
		{name: "parent component 256 bytes", profileID: "id", configuredPath: filepath.Join(strings.Repeat("p", 256), "state.json"), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newStatusLookupService(t)
			service.globalConfig.Paths.DataDir = t.TempDir()
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

func TestStartSyncRejectsStoredStateFileWithOverlongComponent(t *testing.T) {
	service, _ := newStatusLookupService(t)
	service.globalConfig.Paths.DataDir = t.TempDir()
	profileID := "stored-overlong-component"
	require.NoError(t, service.repository.CreateProfile(
		profileID,
		"Stored overlong profile",
		"http://audiobookshelf.invalid",
		"abs-token",
		"hc-token",
		database.SyncConfigData{StateFile: filepath.Join(strings.Repeat("p", 256), "state.json")},
	))

	err := service.StartSync(profileID)
	require.ErrorIs(t, err, ErrProfileStateFileNameTooLong)
	require.False(t, service.IsProfileSyncing(profileID))
}

func TestProfileStateFileValidationRejectsAbsoluteAndEscapingPaths(t *testing.T) {
	service, _ := newStatusLookupService(t)
	service.globalConfig.Paths.DataDir = t.TempDir()

	for _, test := range []struct {
		name string
		path string
	}{
		{name: "absolute", path: filepath.Join(service.globalConfig.Paths.DataDir, "outside.json")},
		{name: "parent", path: "../outside.json"},
		{name: "nested parent", path: filepath.Join("nested", "..", "..", "outside.json")},
		{name: "current directory", path: "."},
		{name: "collapsing relative", path: "safe/.."},
		{name: "NUL byte", path: "state\x00.json"},
	} {
		t.Run("create "+test.name, func(t *testing.T) {
			profileID := "unsafe-create-" + strings.ReplaceAll(test.name, " ", "-")
			err := service.CreateProfile(
				profileID,
				"Profile",
				"http://audiobookshelf",
				"abs-token",
				"hc-token",
				database.SyncConfigData{StateFile: test.path},
			)
			require.Error(t, err)
			profile, getErr := service.GetProfile(profileID)
			require.NoError(t, getErr)
			require.Nil(t, profile, "rejected state paths must not create a profile")
		})
	}

	profileID := "unsafe-update"
	const originalURL = "http://original.invalid"
	require.NoError(t, service.CreateProfile(
		profileID,
		"Profile",
		originalURL,
		"abs-token",
		"hc-token",
		database.SyncConfigData{StateFile: "safe/state.json"},
	))
	err := service.UpdateProfileConfig(
		profileID,
		"http://should-not-persist.invalid",
		"",
		"",
		database.SyncConfigData{StateFile: "../../outside.json"},
	)
	require.Error(t, err)
	profile, getErr := service.GetProfile(profileID)
	require.NoError(t, getErr)
	require.NotNil(t, profile)
	require.Equal(t, originalURL, profile.AudiobookshelfURL)
	require.Equal(t, "safe/state.json", profile.SyncConfig.StateFile)
	require.Equal(t, "abs-token", profile.AudiobookshelfToken)
	require.Equal(t, "hc-token", profile.HardcoverToken)
}

func TestProfileStateFileAcceptsSafeRelativePathsUnderDataDir(t *testing.T) {
	service, _ := newStatusLookupService(t)
	service.globalConfig.Paths.DataDir = t.TempDir()

	for _, configuredPath := range []string{"state.json", filepath.Join("nested", "state.json")} {
		t.Run(configuredPath, func(t *testing.T) {
			profileID := "safe-" + strings.ReplaceAll(configuredPath, string(filepath.Separator), "-")
			require.NoError(t, service.CreateProfile(
				profileID,
				"Profile",
				"http://audiobookshelf",
				"abs-token",
				"hc-token",
				database.SyncConfigData{StateFile: configuredPath},
			))
			profile, err := service.GetProfile(profileID)
			require.NoError(t, err)
			require.NotNil(t, profile)
			require.Equal(t, configuredPath, profile.SyncConfig.StateFile)
		})
	}
}

func TestMigratedAbsoluteStateFileRemainsUsableForLegacyProfile(t *testing.T) {
	absServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			_, _ = io.WriteString(w, `{"mediaProgress":[],"listeningSessions":[]}`)
		case "/api/libraries":
			_, _ = io.WriteString(w, `{"libraries":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(absServer.Close)

	for _, profileID := range []string{"legacy/profile", "legacy/../../escape"} {
		t.Run(profileID, func(t *testing.T) {
			service, _ := newStatusLookupService(t)
			dataDir := t.TempDir()
			service.globalConfig.Paths.DataDir = dataDir
			service.globalConfig.Paths.CacheDir = filepath.Join(dataDir, "cache")
			service.globalConfig.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")

			migratedStatePath := filepath.Join(dataDir, "migrated.json")
			require.NoError(t, service.repository.CreateProfile(
				profileID,
				"Migrated profile",
				absServer.URL,
				"abs-token",
				"hc-token",
				database.SyncConfigData{StateFile: migratedStatePath},
			))
			require.NoError(t, service.StartSync(profileID))
			service.WaitForSyncs()

			status := service.GetProfileStatus(profileID)
			require.NotNil(t, status)
			require.Equal(t, "completed", status.Status)
			require.NotNil(t, status.Snapshot)
			require.Equal(t, "completed", status.Snapshot.State)
			require.FileExists(t, filepath.Join(dataDir, "migrated."+url.PathEscape(profileID)))
		})
	}
}

func TestStartSyncRejectsStoredStateFileOutsideDataDir(t *testing.T) {
	for _, test := range []struct {
		name string
		path func(dataDir, outsideDir string) string
	}{
		{
			name: "relative traversal",
			path: func(_, outsideDir string) string {
				return filepath.Join("..", filepath.Base(outsideDir), "state.json")
			},
		},
		{
			name: "absolute outside",
			path: func(_, outsideDir string) string {
				return filepath.Join(outsideDir, "state.json")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newStatusLookupService(t)
			rootDir := t.TempDir()
			dataDir := filepath.Join(rootDir, "data")
			outsideDir := filepath.Join(rootDir, "outside")
			service.globalConfig.Paths.DataDir = dataDir
			configuredPath := test.path(dataDir, outsideDir)
			profileID := "stored/profile"

			legacyPath := service.legacyProfileStatePath(profileID, configuredPath)
			legacyState := statepkg.NewState()
			legacyState.UpdateBook("preserved-book", 0.5, "IN_PROGRESS")
			require.NoError(t, legacyState.Save(legacyPath))
			canonicalPath := service.profileSpecificStatePath(profileID, configuredPath)
			require.NotEqual(t, legacyPath, canonicalPath)

			require.NoError(t, service.repository.CreateProfile(
				profileID,
				"Stored unsafe profile",
				"http://audiobookshelf.invalid",
				"abs-token",
				"hc-token",
				database.SyncConfigData{StateFile: configuredPath},
			))

			err := service.StartSync(profileID)
			require.ErrorIs(t, err, ErrProfileStateFilePathNotAllowed)
			require.False(t, service.IsProfileSyncing(profileID))
			require.FileExists(t, legacyPath)
			require.NoFileExists(t, legacyPath+".migrated")
			require.NoFileExists(t, canonicalPath)
		})
	}
}

func TestStartSyncRejectsStoredStateFileThroughExternalSymlink(t *testing.T) {
	service, _ := newStatusLookupService(t)
	rootDir := t.TempDir()
	dataDir := filepath.Join(rootDir, "data")
	outsideDir := filepath.Join(rootDir, "outside")
	service.globalConfig.Paths.DataDir = dataDir
	require.NoError(t, os.MkdirAll(dataDir, 0755))
	require.NoError(t, os.MkdirAll(outsideDir, 0755))
	if err := os.Symlink(outsideDir, filepath.Join(dataDir, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	profileID := "stored/symlink"
	configuredPath := filepath.Join("escape", "state.json")
	canonicalPath := service.profileSpecificStatePath(profileID, configuredPath)
	legacyPath := service.legacyProfileStatePath(profileID, configuredPath)
	require.NoError(t, service.repository.CreateProfile(
		profileID,
		"Stored symlink profile",
		"http://audiobookshelf.invalid",
		"abs-token",
		"hc-token",
		database.SyncConfigData{StateFile: configuredPath},
	))

	err := service.StartSync(profileID)
	require.ErrorIs(t, err, ErrProfileStateFilePathNotAllowed)
	require.False(t, service.IsProfileSyncing(profileID))
	require.NoFileExists(t, canonicalPath)
	require.NoFileExists(t, legacyPath)
	require.NoFileExists(t, legacyPath+".migrated")
	require.NoFileExists(t, filepath.Join(outsideDir, "state."+encodeProfileID(profileID)))
	require.NoFileExists(t, filepath.Join(outsideDir, "state."+profileID))
}

func TestStartSyncRejectsDefaultStateFileSymlinksOutsideDataDir(t *testing.T) {
	for _, test := range []struct {
		name           string
		existingTarget bool
	}{
		{name: "existing outside target", existingTarget: true},
		{name: "dangling outside target"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newStatusLookupService(t)
			rootDir := t.TempDir()
			dataDir := filepath.Join(rootDir, "data")
			outsideDir := filepath.Join(rootDir, "outside")
			service.globalConfig.Paths.DataDir = dataDir
			require.NoError(t, os.MkdirAll(dataDir, 0755))
			require.NoError(t, os.MkdirAll(outsideDir, 0755))

			profileID := "default-symlink-" + strings.ReplaceAll(test.name, " ", "-")
			canonicalPath := service.profileSpecificStatePath(profileID, "")
			outsideTarget := filepath.Join(outsideDir, "state.json")
			if test.existingTarget {
				require.NoError(t, statepkg.NewState().Save(outsideTarget))
			}
			if err := os.Symlink(outsideTarget, canonicalPath); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}

			require.NoError(t, service.repository.CreateProfile(
				profileID,
				"Default symlink profile",
				"http://audiobookshelf.invalid",
				"abs-token",
				"hc-token",
				database.SyncConfigData{},
			))

			var before []byte
			if test.existingTarget {
				before, _ = os.ReadFile(outsideTarget)
			}
			err := service.StartSync(profileID)
			require.ErrorIs(t, err, ErrProfileStateFilePathNotAllowed)
			require.False(t, service.IsProfileSyncing(profileID))
			if test.existingTarget {
				after, readErr := os.ReadFile(outsideTarget)
				require.NoError(t, readErr)
				require.Equal(t, before, after)
			} else {
				require.NoFileExists(t, outsideTarget)
			}
		})
	}
}

func TestStartSyncAcceptsDefaultStateFileSymlinkInsideDataDir(t *testing.T) {
	absServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			_, _ = io.WriteString(w, `{"mediaProgress":[],"listeningSessions":[]}`)
		case "/api/libraries":
			_, _ = io.WriteString(w, `{"libraries":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(absServer.Close)

	service, _ := newStatusLookupService(t)
	dataDir := t.TempDir()
	insideDir := filepath.Join(dataDir, "inside")
	service.globalConfig.Paths.DataDir = dataDir
	service.globalConfig.Paths.CacheDir = filepath.Join(dataDir, "cache")
	service.globalConfig.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	require.NoError(t, os.MkdirAll(insideDir, 0755))

	profileID := "default-inside-symlink"
	canonicalPath := service.profileSpecificStatePath(profileID, "")
	insideTarget := filepath.Join(insideDir, "state.json")
	require.NoError(t, statepkg.NewState().Save(insideTarget))
	if err := os.Symlink(insideTarget, canonicalPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	require.NoError(t, service.repository.CreateProfile(
		profileID,
		"Default inside symlink profile",
		absServer.URL,
		"abs-token",
		"hc-token",
		database.SyncConfigData{},
	))

	require.NoError(t, service.StartSync(profileID))
	service.WaitForSyncs()
	status := service.GetProfileStatus(profileID)
	require.NotNil(t, status)
	require.Equal(t, "completed", status.Status)
	require.FileExists(t, insideTarget)
	info, err := os.Lstat(canonicalPath)
	require.NoError(t, err)
	require.NotEqual(t, os.FileMode(0), info.Mode()&os.ModeSymlink)
}

func TestCreateProfileAcceptsStateFileThroughInternalSymlink(t *testing.T) {
	service, _ := newStatusLookupService(t)
	dataDir := t.TempDir()
	insideDir := filepath.Join(dataDir, "inside")
	service.globalConfig.Paths.DataDir = dataDir
	require.NoError(t, os.MkdirAll(insideDir, 0755))
	if err := os.Symlink(insideDir, filepath.Join(dataDir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	configuredPath := filepath.Join("link", "state.json")
	require.NoError(t, service.CreateProfile(
		"inside-link",
		"Inside symlink profile",
		"http://audiobookshelf.invalid",
		"abs-token",
		"hc-token",
		database.SyncConfigData{StateFile: configuredPath},
	))
	profile, err := service.GetProfile("inside-link")
	require.NoError(t, err)
	require.NotNil(t, profile)
	require.Equal(t, configuredPath, profile.SyncConfig.StateFile)
}

func TestMigratesLegacyRawProfileStatePath(t *testing.T) {
	service, _ := newStatusLookupService(t)
	dataDir := t.TempDir()
	service.globalConfig.Paths.DataDir = dataDir

	for _, profileID := range []string{"legacy profile", "legacy/profile"} {
		t.Run(profileID, func(t *testing.T) {
			configuredPath := "state.json"
			legacyPath := service.legacyProfileStatePath(profileID, configuredPath)
			canonicalPath := service.profileSpecificStatePath(profileID, configuredPath)
			legacyState := statepkg.NewState()
			legacyState.UpdateBook("preserved-book", 0.5, "IN_PROGRESS")
			require.NoError(t, legacyState.Save(legacyPath))

			require.NoError(t, service.migrateLegacyProfileStatePath(profileID, configuredPath))

			migrated, err := statepkg.LoadState(canonicalPath)
			require.NoError(t, err)
			book, exists := migrated.GetBookState("preserved-book")
			require.True(t, exists)
			require.Equal(t, 0.5, book.LastProgress)
			require.NoFileExists(t, legacyPath)
			require.FileExists(t, legacyPath+".migrated")
		})
	}
}

func TestLegacyProfileStateMigrationSkipsUnsafeOrIneligibleCandidates(t *testing.T) {
	t.Run("dot segments cannot alias another legacy profile", func(t *testing.T) {
		service, _ := newStatusLookupService(t)
		dataDir := t.TempDir()
		service.globalConfig.Paths.DataDir = dataDir

		victimID := "sync_state.victim"
		victimPath := service.legacyProfileStatePath(victimID, "state.json")
		victimState := statepkg.NewState()
		victimState.UpdateBook("victim-book", 0.8, "IN_PROGRESS")
		require.NoError(t, victimState.Save(victimPath))

		attackerID := "foo/../sync_state.victim"
		attackerCanonicalPath := service.profileSpecificStatePath(attackerID, "state.json")
		require.NoError(t, service.migrateLegacyProfileStatePath(attackerID, "state.json"))

		require.NoFileExists(t, attackerCanonicalPath)
		require.FileExists(t, victimPath)
		require.NoFileExists(t, victimPath+".migrated")
		preserved, err := statepkg.LoadState(victimPath)
		require.NoError(t, err)
		_, exists := preserved.GetBookState("victim-book")
		require.True(t, exists)
	})

	t.Run("path escapes state directory", func(t *testing.T) {
		service, _ := newStatusLookupService(t)
		rootDir := t.TempDir()
		dataDir := filepath.Join(rootDir, "data")
		service.globalConfig.Paths.DataDir = dataDir
		require.NoError(t, os.MkdirAll(dataDir, 0755))

		profileID := "../../../outside"
		legacyPath := filepath.Clean(service.legacyProfileStatePath(profileID, "state.json"))
		legacyState := statepkg.NewState()
		require.NoError(t, legacyState.Save(legacyPath))
		canonicalPath := service.profileSpecificStatePath(profileID, "state.json")

		require.NoError(t, service.migrateLegacyProfileStatePath(profileID, "state.json"))
		require.NoFileExists(t, canonicalPath)
		require.FileExists(t, legacyPath)
	})

	t.Run("final path is a symlink", func(t *testing.T) {
		service, _ := newStatusLookupService(t)
		dataDir := t.TempDir()
		service.globalConfig.Paths.DataDir = dataDir
		outsidePath := filepath.Join(t.TempDir(), "outside-state.json")
		legacyState := statepkg.NewState()
		require.NoError(t, legacyState.Save(outsidePath))

		profileID := "legacy profile"
		legacyPath := service.legacyProfileStatePath(profileID, "state.json")
		if err := os.Symlink(outsidePath, legacyPath); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		canonicalPath := service.profileSpecificStatePath(profileID, "state.json")

		require.NoError(t, service.migrateLegacyProfileStatePath(profileID, "state.json"))
		require.NoFileExists(t, canonicalPath)
		_, err := os.Lstat(legacyPath)
		require.NoError(t, err)
	})

	t.Run("canonical path already exists", func(t *testing.T) {
		service, _ := newStatusLookupService(t)
		dataDir := t.TempDir()
		service.globalConfig.Paths.DataDir = dataDir
		profileID := "legacy profile"
		canonicalPath := service.profileSpecificStatePath(profileID, "state.json")
		canonicalState := statepkg.NewState()
		canonicalState.UpdateBook("canonical-book", 0.75, "IN_PROGRESS")
		require.NoError(t, canonicalState.Save(canonicalPath))

		legacyPath := service.legacyProfileStatePath(profileID, "state.json")
		legacyState := statepkg.NewState()
		legacyState.UpdateBook("legacy-book", 0.25, "IN_PROGRESS")
		require.NoError(t, legacyState.Save(legacyPath))

		require.NoError(t, service.migrateLegacyProfileStatePath(profileID, "state.json"))
		migrated, err := statepkg.LoadState(canonicalPath)
		require.NoError(t, err)
		_, exists := migrated.GetBookState("canonical-book")
		require.True(t, exists)
		_, exists = migrated.GetBookState("legacy-book")
		require.False(t, exists)
		require.FileExists(t, legacyPath)
	})
}

func TestCreateProfileRejectsStateFilenameLengthAfterLegacyIDEncoding(t *testing.T) {
	service, _ := newStatusLookupService(t)
	service.globalConfig.Paths.DataDir = t.TempDir()
	profileID := strings.Repeat("/", 200)

	err := service.CreateProfile(
		profileID,
		"Profile",
		"http://audiobookshelf",
		"abs-token",
		"hc-token",
		database.SyncConfigData{StateFile: "state.json"},
	)
	require.ErrorIs(t, err, ErrProfileStateFileNameTooLong)
	profile, getErr := service.GetProfile(profileID)
	require.NoError(t, getErr)
	require.Nil(t, profile)
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
