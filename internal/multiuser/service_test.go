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
	"sync/atomic"
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
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	syncsvc "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
	statepkg "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
)

func TestProfileMismatchExportsRemainIsolated(t *testing.T) {
	service, _ := newStatusLookupService(t)
	service.globalConfig.Paths.MismatchOutputDir = filepath.Join(t.TempDir(), "mismatches")
	ctx := logger.NewContext(context.Background(), logger.Get())

	profiles := []struct {
		id    string
		title string
	}{
		{id: "profile-a", title: "Profile A"},
		{id: "profile-b", title: "Profile B"},
	}
	configs := make([]*config.Config, 0, len(profiles))
	collectors := make([]*mismatch.Collector, 0, len(profiles))
	for _, profile := range profiles {
		profileConfig := &database.ProfileWithTokens{
			Profile: database.SyncProfile{ID: profile.id},
		}
		configs = append(configs, service.createProfileSpecificConfig(profileConfig))

		collector := mismatch.NewCollector()
		collector.Add(mismatch.BookMismatch{BookID: profile.id, Title: profile.title})
		collectors = append(collectors, collector)
	}

	require.NotEqual(t, configs[0].Paths.MismatchOutputDir, configs[1].Paths.MismatchOutputDir)
	for i, collector := range collectors {
		require.NoError(t, collector.SaveToFile(ctx, nil, "", configs[i]))
	}

	for i, profile := range profiles {
		path := filepath.Join(
			configs[i].Paths.MismatchOutputDir,
			"edition_001_"+mismatch.SanitizeFilename(profile.title)+".json",
		)
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(data), `"title": "`+profile.title+`"`)
	}
}

func TestGetSyncRunSnapshotUsesCurrentRunWithoutProfileHydration(t *testing.T) {
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
		t.Errorf("GetSyncRunSnapshot hydrated profile metadata from the database")
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
	liveService, err := syncsvc.NewServiceWithRunIdentity(
		audiobookshelf.NewClient("http://audiobookshelf.invalid", "abs-token"),
		hardcover.NewClientWithConfig(hcConfig, "hc-token", logger.Get()),
		cfg,
		"",
		time.Time{},
	)
	require.NoError(t, err)

	run := activeSyncRun{generation: 1, runID: "profile-a-run-1", startedAt: time.Now().UTC()}
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	service.syncMutex.Lock()
	service.activeRuns[profileID] = run
	service.activeSyncs[profileID] = cancel
	service.syncMutex.Unlock()
	require.True(t, service.registerSyncService(profileID, run.generation, liveService))
	t.Cleanup(func() {
		service.removeSyncService(profileID, run.generation, liveService)
		service.finishActiveRun(profileID, run.generation)
	})

	snapshot, err := service.GetSyncRunSnapshot(profileID, run.runID)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	require.Equal(t, run.runID, snapshot.RunID)
	require.Equal(t, profileID, snapshot.UserID)
	require.Equal(t, string(syncsvc.RunPhaseQueued), snapshot.State)
}

func TestGetSyncRunSnapshotRestoresRetainedRunWithoutProfileHydration(t *testing.T) {
	service, db := newStatusLookupService(t)
	profileID := "profile-retained"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Profile", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))
	queuedAt := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	report := acceptTestSyncRun(t, service.repository, profileID, "run-retained", false, queuedAt)
	report.Phase = database.SyncRunPhaseCompleted
	report.FinishedAt = timePtrForMultiuserTest(queuedAt.Add(time.Minute))
	report.SnapshotJSON = `{"run_id":"run-retained","state":"completed"}`
	require.NoError(t, service.repository.UpsertSyncRunReport(report))

	const queryCallbackName = "multiuser_test_forbid_retained_snapshot_profile_hydration"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(queryCallbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Name != "SyncProfile" {
			return
		}
		t.Errorf("GetSyncRunSnapshot hydrated profile metadata from the database")
		require.ErrorIs(t, tx.AddError(gorm.ErrInvalidValue), gorm.ErrInvalidValue)
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(queryCallbackName)) })

	snapshot, err := service.GetSyncRunSnapshot(profileID, report.RunID)
	require.NoError(t, err)
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
	report := acceptTestSyncRun(t, service.repository, profileID, "run-exact", false, queuedAt)
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

func acceptTestSyncRun(t *testing.T, repo *database.Repository, profileID, runID string, dryRun bool, queuedAt time.Time) *database.SyncRunReport {
	t.Helper()
	report, err := repo.AcceptSyncRun(&database.SyncRunReport{
		ProfileID: profileID, RunID: runID, Phase: database.SyncRunPhaseQueued,
		DryRun: dryRun, QueuedAt: &queuedAt,
		SnapshotJSON: "{}",
	})
	require.NoError(t, err)
	return report
}

func TestStatusAggregateOmitsRunErrorAndDetailsRetainIt(t *testing.T) {
	service, _ := newStatusLookupService(t)
	profileID := "profile-a"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Profile A", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))

	lastSync := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	status := &SyncProfileStatus{
		ProfileID:        profileID,
		ProfileName:      "Profile A",
		LastAttemptedAt:  &lastSync,
		LastSuccessfulAt: &lastSync,
		Snapshot: &syncsvc.SyncSnapshot{
			RunID:            "profile-a-run-1",
			State:            "failed",
			RunError:         "upstream response: sensitive details",
			BooksTotal:       12,
			ProcessedSoFar:   1,
			UnattemptedCount: 11,
			OutcomeCounts:    syncsvc.OutcomeCounts{Failed: 1},
			BookOutcomes: []syncsvc.BookOutcomeRecord{{
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
	require.Equal(t, status.LastAttemptedAt, aggregate[0].LastAttemptedAt)
	require.Equal(t, status.LastSuccessfulAt, aggregate[0].LastSuccessfulAt)
	require.NotNil(t, aggregate[0].Snapshot)
	require.Equal(t, status.Snapshot.RunID, aggregate[0].Snapshot.RunID)
	require.Equal(t, status.Snapshot.OutcomeCounts, aggregate[0].Snapshot.OutcomeCounts)
	require.Equal(t, status.Snapshot.UnattemptedCount, aggregate[0].Snapshot.UnattemptedCount)
	require.Empty(t, aggregate[0].Snapshot.RunError)
	require.Nil(t, aggregate[0].Snapshot.BookOutcomes)

	direct := profileStatusForTest(t, service, profileID)
	require.NotNil(t, direct)
	require.Equal(t, status.Snapshot.RunError, direct.Snapshot.RunError)
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
		State:          string(syncsvc.RunPhaseRunning),
		BooksTotal:     0,
		ProcessedSoFar: 3,
		OutcomeCounts:  syncsvc.OutcomeCounts{NotFound: 3},
	}
	service.updateProfileStatus(profileID, &SyncProfileStatus{
		ProfileID:   profileID,
		ProfileName: "Profile A",
		Snapshot:    &snapshot,
	})

	statuses, err := service.GetAllProfileStatuses()
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	require.NotNil(t, statuses[0].Snapshot)
	require.Zero(t, statuses[0].Snapshot.BooksTotal)
	require.Equal(t, int32(3), statuses[0].Snapshot.ProcessedSoFar)
}

func TestAggregateStatusMapsLiveTerminalSnapshotState(t *testing.T) {
	for _, test := range []struct {
		name          string
		librariesCode int
		wantState     string
		wantSyncError bool
	}{
		{name: "completed", librariesCode: http.StatusOK, wantState: "completed"},
		{name: "failed", librariesCode: http.StatusInternalServerError, wantState: "failed", wantSyncError: true},
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
			liveService, err := syncsvc.NewServiceWithRunIdentity(
				audiobookshelf.NewClient(absServer.URL, "abs-token"),
				hardcover.NewClientWithConfig(hcConfig, "hc-token", logger.Get()),
				cfg,
				"",
				time.Time{},
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
	report := acceptTestSyncRun(t, service.repository, profileID, runID, dryRun, queuedAt)
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
	report, err := service.repository.GetSyncRunReport(profileID, accepted.RunID)
	require.NoError(t, err)
	require.NotNil(t, report)
	require.Equal(t, database.SyncRunPhaseQueued, report.Phase)
	require.Equal(t, accepted.RunID, report.RunID)
	require.Equal(t, accepted.QueuedAt, *report.QueuedAt)
	require.Contains(t, report.SnapshotJSON, accepted.RunID)

	status := profileStatusForTest(t, service, profileID)
	require.NotNil(t, status)
	require.NotNil(t, status.Snapshot)
	require.Equal(t, accepted.RunID, status.Snapshot.RunID)

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
	var reportCount int64
	require.NoError(t, db.Model(&database.SyncRunReport{}).
		Where("profile_id = ?", profileID).
		Count(&reportCount).Error)
	require.Zero(t, reportCount)
	state, stateErr := service.repository.GetSyncState(profileID)
	require.NoError(t, stateErr)
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

func TestShutdownClosesAdmissionCancelsActiveRunsAndDrainsWorkers(t *testing.T) {
	requestStarted := make(chan struct{})
	var requestStartedOnce sync.Once
	absServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/me" {
			http.NotFound(w, r)
			return
		}
		requestStartedOnce.Do(func() { close(requestStarted) })
		<-r.Context().Done()
	}))
	t.Cleanup(absServer.Close)

	service, _ := newStatusLookupService(t)
	dataDir := t.TempDir()
	service.globalConfig.Paths.DataDir = dataDir
	service.globalConfig.Paths.CacheDir = filepath.Join(dataDir, "cache")
	service.globalConfig.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	const profileID = "profile-shutdown"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Shutdown profile", absServer.URL, "abs-token", "hc-token", database.SyncConfigData{},
	))

	_, err := service.StartSyncWithAcceptedRun(profileID)
	require.NoError(t, err)
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for active sync request")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	require.NoError(t, service.Shutdown(shutdownCtx))
	require.False(t, service.IsProfileSyncing(profileID))
	_, err = service.StartSyncWithAcceptedRun(profileID)
	require.ErrorIs(t, err, ErrServiceShuttingDown)
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
	status := profileStatusForTest(t, service, profileID)
	require.NotNil(t, status)
	require.NotNil(t, status.Snapshot)
	require.Equal(t, accepted.RunID, status.Snapshot.RunID)
	require.Equal(t, accepted.QueuedAt, status.Snapshot.QueuedAt)
}

func TestCanceledProfileWorkersRunInAcceptanceOrder(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	var requestOnce sync.Once
	var requestCount atomic.Int32
	absServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me":
			requestCount.Add(1)
			requestOnce.Do(func() { close(requestStarted) })
			<-releaseRequest
			_, _ = io.WriteString(w, `{}`)
		case "/api/libraries":
			_, _ = io.WriteString(w, `{"libraries":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(func() {
		select {
		case <-releaseRequest:
		default:
			close(releaseRequest)
		}
		absServer.Close()
	})

	service, _ := newStatusLookupService(t)
	dataDir := t.TempDir()
	service.globalConfig.Paths.DataDir = dataDir
	service.globalConfig.Paths.CacheDir = filepath.Join(dataDir, "cache")
	service.globalConfig.Paths.MismatchOutputDir = filepath.Join(dataDir, "mismatches")
	const profileID = "profile-gated-workers"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Gated workers", absServer.URL, "abs-token", "hc-token", database.SyncConfigData{},
	))

	// Reserve the first execution position with a worker that blocks before its
	// filesystem work. Cancellation must return while this worker is blocked.
	oldRun := installAcceptedTestRun(t, service, profileID, "run-old", false)
	gate := service.profileGate(profileID)
	gate.mu.Lock()
	oldTicket := gate.enqueueExecutionLocked()
	gate.mu.Unlock()
	oldEntered := make(chan struct{})
	oldRelease := make(chan struct{})
	oldDone := make(chan struct{})
	go func() {
		service.runSyncWorker(profileID, oldRun.runID, oldRun.generation, gate, oldTicket, func() {
			close(oldEntered)
			<-oldRelease
		})
		close(oldDone)
	}()
	select {
	case <-oldEntered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the old worker")
	}

	require.NoError(t, service.CancelSync(profileID))
	firstReplacement, err := service.StartSyncWithAcceptedRun(profileID)
	require.NoError(t, err)
	require.NoError(t, service.CancelSync(profileID))
	secondReplacement, err := service.StartSyncWithAcceptedRun(profileID)
	require.NoError(t, err)

	// Both replacements are queued behind the blocked old worker. The first
	// replacement was canceled while queued and must not enter Sync at all.
	select {
	case <-requestStarted:
		t.Fatal("replacement worker overtook the blocked old worker")
	default:
	}

	close(oldRelease)
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the non-stale replacement worker")
	}
	close(releaseRequest)
	service.WaitForSyncs()
	select {
	case <-oldDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the old worker to exit")
	}

	require.Equal(t, int32(1), requestCount.Load(), "the queued canceled replacement must not initialize a sync client")
	firstReport, err := service.repository.GetSyncRunReport(profileID, firstReplacement.RunID)
	require.NoError(t, err)
	require.NotNil(t, firstReport)
	require.Equal(t, database.SyncRunPhaseCanceled, firstReport.Phase)
	secondReport, err := service.repository.GetSyncRunReport(profileID, secondReplacement.RunID)
	require.NoError(t, err)
	require.NotNil(t, secondReport)
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
	require.Nil(t, state.LastSuccessfulAt)
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
	require.Nil(t, oldReport)
	state, err := service.repository.GetSyncState(profileID)
	require.NoError(t, err)
	require.Nil(t, state.LastSuccessfulAt)
	require.Equal(t, newRun.runID, service.latestRuns[profileID].runID)
}

func TestDryRunTerminalStatusKeepsAttemptWithoutSuccess(t *testing.T) {
	service, _ := newStatusLookupService(t)
	profileID := "profile-dry"
	require.NoError(t, service.repository.CreateProfile(profileID, "Dry profile", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{}))
	run := installAcceptedTestRun(t, service, profileID, "run-dry", true)

	require.True(t, service.publishFinalStatus(profileID, run.generation, acceptedTerminalStatus(profileID, run, string(syncsvc.RunPhaseCompleted))))
	status := profileStatusForTest(t, service, profileID)
	require.NotNil(t, status)
	require.NotNil(t, status.LastAttemptedAt)
	require.Nil(t, status.LastSuccessfulAt)
	state, err := service.repository.GetSyncState(profileID)
	require.NoError(t, err)
	require.Equal(t, run.runID, state.LastAttemptedRunID)
	require.Nil(t, state.LastSuccessfulAt)
}

func TestRestartRestoresNewestTerminalReport(t *testing.T) {
	service, _ := newStatusLookupService(t)
	profileID := "profile-restart"
	require.NoError(t, service.repository.CreateProfile(profileID, "Restart profile", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{}))
	first := installAcceptedTestRun(t, service, profileID, "run-first", false)
	require.True(t, service.publishFinalStatus(profileID, first.generation, acceptedTerminalStatus(profileID, first, string(syncsvc.RunPhaseCompleted))))
	second := installAcceptedTestRun(t, service, profileID, "run-second", false)
	secondStatus := acceptedTerminalStatus(profileID, second, string(syncsvc.RunPhaseCanceled))
	require.True(t, service.publishFinalStatus(profileID, second.generation, secondStatus))

	restarted := NewMultiUserService(service.repository, config.DefaultConfig(), logger.Get())
	status := profileStatusForTest(t, restarted, profileID)
	require.NotNil(t, status)
	require.Equal(t, second.runID, status.Snapshot.RunID)
	require.Equal(t, string(syncsvc.RunPhaseCanceled), status.Snapshot.State)
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

	status := profileStatusForTest(t, service, profileID)
	require.NotNil(t, status)
	require.NotNil(t, status.Snapshot)
	require.Equal(t, report.RunError, status.Snapshot.RunError)

	restarted := NewMultiUserService(service.repository, config.DefaultConfig(), logger.Get())
	restored, err := restarted.GetSyncRunSnapshot(profileID, runID)
	require.NoError(t, err)
	require.NotNil(t, restored)
	require.Equal(t, report.RunError, restored.RunError)
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
		report := acceptTestSyncRun(t, service.repository, profileID, runID, false, queuedAt.Add(time.Duration(generation)*time.Minute))
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
	status := profileStatusForTest(t, restarted, profileID)
	require.NotNil(t, status)
	require.NotNil(t, status.Snapshot)
	require.Equal(t, "terminal-10", status.Snapshot.RunID)
	require.Equal(t, string(syncsvc.RunPhaseCompleted), status.Snapshot.State)
}

func TestRestartRestoresTerminalWhenQueuedReportsExceedLookupLimit(t *testing.T) {
	service, _ := newStatusLookupService(t)
	const profileID = "profile-terminal-before-queued"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Terminal before queued", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))
	queuedAt := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	terminal := acceptTestSyncRun(t, service.repository, profileID, "terminal-retained", false, queuedAt)
	terminal.Phase = database.SyncRunPhaseCompleted
	terminal.FinishedAt = timePtrForMultiuserTest(queuedAt.Add(time.Minute))
	terminal.SnapshotJSON = `{"state":"completed"}`
	require.NoError(t, service.repository.UpsertSyncRunReport(terminal))

	for generation := 1; generation <= 10; generation++ {
		acceptTestSyncRun(t, service.repository, profileID, fmt.Sprintf("queued-%d", generation), false,
			queuedAt.Add(time.Duration(generation+1)*time.Minute))
	}

	restarted := NewMultiUserService(service.repository, config.DefaultConfig(), logger.Get())
	status := profileStatusForTest(t, restarted, profileID)
	require.NotNil(t, status)
	require.NotNil(t, status.Snapshot)
	require.Equal(t, "terminal-retained", status.Snapshot.RunID)
	require.Equal(t, string(syncsvc.RunPhaseCompleted), status.Snapshot.State)
}

func TestAggregateStatusRestoresTerminalScalarsWithoutDetails(t *testing.T) {
	service, _ := newStatusLookupService(t)
	const profileID = "profile-aggregate-restart"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Aggregate restart", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))
	queuedAt := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	report := acceptTestSyncRun(t, service.repository, profileID, "run-aggregate", false, queuedAt)
	report.Phase = database.SyncRunPhaseCompleted
	report.FinishedAt = timePtrForMultiuserTest(queuedAt.Add(time.Minute))
	report.SnapshotJSON = `{"books_total":12,"processed_so_far":7,"processed_count":7,"unattempted_count":5,"outcome_counts":{"synced":5},"total_books_processed":99,"books_synced":99,"books_not_found":[],"mismatches":[],"last_sync_summary":{"books_synced":99},"book_outcomes":[{"book_id":"book-1"}],"attention_records":[{"book_id":"book-1"}]}`
	require.NoError(t, service.repository.UpsertSyncRunReport(report))

	restarted := NewMultiUserService(service.repository, config.DefaultConfig(), logger.Get())
	for poll := 0; poll < 2; poll++ {
		statuses, err := restarted.GetAllProfileStatuses()
		require.NoError(t, err)
		require.Len(t, statuses, 1)
		status := statuses[0]
		require.NotNil(t, status.Snapshot)
		require.Equal(t, "run-aggregate", status.Snapshot.RunID)
		require.Equal(t, string(syncsvc.RunPhaseCompleted), status.Snapshot.State)
		require.Equal(t, int32(5), status.Snapshot.UnattemptedCount)
		require.Equal(t, int32(12), status.Snapshot.BooksTotal)
		require.Equal(t, int32(7), status.Snapshot.ProcessedSoFar)
		require.Equal(t, int32(5), status.Snapshot.OutcomeCounts.Synced)
		require.Empty(t, status.Snapshot.BookOutcomes)
	}
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
	status := profileStatusForTest(t, service, profileID)
	require.NotNil(t, status)
	require.NotNil(t, status.Snapshot)
	require.Equal(t, string(syncsvc.RunPhaseFailed), status.Snapshot.State)
	require.Contains(t, status.Snapshot.RunError, "failed to persist sync report")
}

func TestCancelSyncReturnsPersistenceFailureAfterPublishingCanceledRunError(t *testing.T) {
	service, db := newStatusLookupService(t)
	const profileID = "profile-cancel-persist-failure"
	require.NoError(t, service.repository.CreateProfile(
		profileID, "Cancel persistence failure", "http://audiobookshelf", "abs-token", "hc-token", database.SyncConfigData{},
	))
	run := installAcceptedTestRun(t, service, profileID, "run-cancel-persist-failure", false)

	writeErr := errors.New("canceled report persistence unavailable")
	const callbackName = "multiuser_test_fail_canceled_report_update"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "SyncRunReport" {
			require.ErrorIs(t, tx.AddError(writeErr), writeErr)
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callbackName)) })

	err := service.CancelSync(profileID)
	require.ErrorIs(t, err, writeErr)
	require.False(t, service.IsProfileSyncing(profileID))

	status := profileStatusForTest(t, service, profileID)
	require.NotNil(t, status)
	require.NotNil(t, status.Snapshot)
	require.Equal(t, run.runID, status.Snapshot.RunID)
	require.Equal(t, string(syncsvc.RunPhaseFailed), status.Snapshot.State)
	require.Contains(t, status.Snapshot.RunError, "failed to persist canceled sync report")
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
		ProfileID: "profile-b", ProfileName: "profile-b",
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
	advanced := lastSync.Add(time.Second)
	go func() {
		service.updateProfileStatus("profile-b", &SyncProfileStatus{
			ProfileID: "profile-b", ProfileName: "profile-b",
			LastAttemptedAt: &advanced, LastSuccessfulAt: &lastSync,
		})
		close(progressDone)
	}()
	select {
	case <-progressDone:
	case <-time.After(time.Second):
		t.Fatal("profile B progress update blocked behind profile A persistence")
	}
	statusDone := make(chan *SyncProfileStatus, 1)
	go func() { statusDone <- profileStatusForTest(t, service, "profile-b") }()
	select {
	case status := <-statusDone:
		require.NotNil(t, status)
		require.Equal(t, "profile-b", status.ProfileID)
		require.Equal(t, advanced, *status.LastAttemptedAt)
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

	_, err := service.StartSyncWithAcceptedRun(profileID)
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
			_, err := service.StartSyncWithAcceptedRun(profileID)
			require.NoError(t, err)
			service.WaitForSyncs()

			status := profileStatusForTest(t, service, profileID)
			require.NotNil(t, status)
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

			_, err := service.StartSyncWithAcceptedRun(profileID)
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

	_, err := service.StartSyncWithAcceptedRun(profileID)
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
			_, err := service.StartSyncWithAcceptedRun(profileID)
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

	_, err := service.StartSyncWithAcceptedRun(profileID)
	require.NoError(t, err)
	service.WaitForSyncs()
	status := profileStatusForTest(t, service, profileID)
	require.NotNil(t, status)
	require.Equal(t, string(syncsvc.RunPhaseCompleted), status.Snapshot.State)
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

func profileStatusForTest(t *testing.T, service *MultiUserService, profileID string) *SyncProfileStatus {
	t.Helper()
	service.statusMutex.RLock()
	status := cloneProfileStatus(service.profileStatuses[profileID])
	service.statusMutex.RUnlock()
	if status != nil {
		return status
	}
	statuses, err := service.GetAllProfileStatuses()
	require.NoError(t, err)
	for _, candidate := range statuses {
		if candidate != nil && candidate.ProfileID == profileID {
			if candidate.Snapshot != nil && candidate.Snapshot.RunID != "" {
				snapshot, snapshotErr := service.GetSyncRunSnapshot(profileID, candidate.Snapshot.RunID)
				require.NoError(t, snapshotErr)
				if snapshot != nil {
					candidate.Snapshot = snapshot
				}
			}
			return candidate
		}
	}
	return nil
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
