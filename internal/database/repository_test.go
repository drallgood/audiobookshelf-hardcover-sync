package database

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/require"
)

func newRepositoryForTest(t *testing.T) (*Database, *Repository) {
	t.Helper()
	db, err := NewDatabase(&DatabaseConfig{
		Type: DatabaseTypeSQLite,
		Path: filepath.Join(t.TempDir(), "sync.db"),
	}, logger.Get())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db, NewRepository(db, nil, logger.Get())
}

func createTestProfile(t *testing.T, db *Database, profileID string) {
	t.Helper()
	require.NoError(t, db.GetDB().Create(&SyncProfile{ID: profileID, Name: profileID, Active: true}).Error)
}

func TestReserveSyncRunAllocatesPerProfileGenerationAndAttemptMetadata(t *testing.T) {
	db, repo := newRepositoryForTest(t)
	createTestProfile(t, db, "profile-a")
	createTestProfile(t, db, "profile-b")
	queuedAt := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC)
	legacyLastSync := queuedAt.Add(-time.Hour)
	require.NoError(t, db.GetDB().Create(&ProfileSyncState{
		ProfileID: "profile-a", StateData: "{}", LastSync: &legacyLastSync,
	}).Error)

	first, err := repo.ReserveSyncRun("profile-a", "run-a-1", false, queuedAt)
	require.NoError(t, err)
	require.Equal(t, uint64(1), first.Generation)
	require.Equal(t, SyncRunPhaseQueued, first.Phase)
	require.Equal(t, queuedAt, *first.QueuedAt)
	first.Phase = SyncRunPhaseCompleted
	first.FinishedAt = timePtrForDatabaseTest(queuedAt.Add(time.Minute))
	first.SnapshotJSON = `{"finished":true}`
	require.NoError(t, repo.UpsertSyncRunReport(first))
	stored, err := repo.GetSyncRunReport("profile-a", "run-a-1")
	require.NoError(t, err)
	require.Equal(t, queuedAt, *stored.QueuedAt)
	require.Equal(t, `{"finished":true}`, stored.SnapshotJSON)

	second, err := repo.ReserveSyncRun("profile-a", "run-a-2", true, queuedAt.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, uint64(2), second.Generation)

	other, err := repo.ReserveSyncRun("profile-b", "run-b-1", false, queuedAt)
	require.NoError(t, err)
	require.Equal(t, uint64(1), other.Generation)

	state, err := repo.GetSyncState("profile-a")
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.RunGeneration)
	require.Equal(t, second.RunID, state.LastAttemptedRunID)
	require.Equal(t, uint64(2), state.LastAttemptedGeneration)
	require.Equal(t, queuedAt.Add(time.Minute), *state.LastAttemptedAt)
	require.Equal(t, legacyLastSync, *state.LastSync)
	require.Equal(t, uint64(1), state.LastSuccessfulGeneration)
}

func timePtrForDatabaseTest(value time.Time) *time.Time {
	return &value
}

func TestSyncRunReportLookupIsScopedToProfile(t *testing.T) {
	db, repo := newRepositoryForTest(t)
	createTestProfile(t, db, "profile-a")
	createTestProfile(t, db, "profile-b")
	for _, report := range []*SyncRunReport{
		{ProfileID: "profile-a", RunID: "same-run", Generation: 1, Phase: SyncRunPhaseFailed, SnapshotJSON: `{"profile":"a"}`},
		{ProfileID: "profile-b", RunID: "same-run", Generation: 1, Phase: SyncRunPhaseCanceled, SnapshotJSON: `{"profile":"b"}`},
	} {
		require.NoError(t, repo.UpsertSyncRunReport(report))
	}

	retrieved, err := repo.GetSyncRunReport("profile-b", "same-run")
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	require.Equal(t, "profile-b", retrieved.ProfileID)
	require.Equal(t, `{"profile":"b"}`, retrieved.SnapshotJSON)

	missing, err := repo.GetSyncRunReport("profile-c", "same-run")
	require.NoError(t, err)
	require.Nil(t, missing)
}

func TestUpsertSyncRunReportAdvancesOnlyNewerCompletedNonDryRun(t *testing.T) {
	db, repo := newRepositoryForTest(t)
	createTestProfile(t, db, "profile-a")
	finish := time.Date(2026, time.September, 16, 12, 30, 0, 0, time.UTC)
	completed := func(generation uint64, runID string, dryRun bool, phase string) *SyncRunReport {
		return &SyncRunReport{
			ProfileID: "profile-a", RunID: runID, Generation: generation,
			Phase: phase, DryRun: dryRun, FinishedAt: &finish,
			SnapshotJSON: `{"state":"terminal"}`,
		}
	}

	require.NoError(t, repo.UpsertSyncRunReport(completed(2, "run-2", false, SyncRunPhaseCompleted)))
	state, err := repo.GetSyncState("profile-a")
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.LastSuccessfulGeneration)
	require.Equal(t, "run-2", state.LastSuccessfulRunID)
	require.Equal(t, finish, *state.LastSuccessfulAt)

	for _, report := range []*SyncRunReport{
		completed(3, "run-failed", false, SyncRunPhaseFailed),
		completed(4, "run-canceled", false, SyncRunPhaseCanceled),
		completed(5, "run-dry", true, SyncRunPhaseCompleted),
		completed(1, "run-old", false, SyncRunPhaseCompleted),
	} {
		require.NoError(t, repo.UpsertSyncRunReport(report))
	}
	state, err = repo.GetSyncState("profile-a")
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.LastSuccessfulGeneration)
	require.Equal(t, "run-2", state.LastSuccessfulRunID)

	newFinish := finish.Add(time.Hour)
	require.NoError(t, repo.UpsertSyncRunReport(&SyncRunReport{
		ProfileID: "profile-a", RunID: "run-6", Generation: 6,
		Phase: SyncRunPhaseCompleted, FinishedAt: &newFinish,
	}))
	state, err = repo.GetSyncState("profile-a")
	require.NoError(t, err)
	require.Equal(t, uint64(6), state.LastSuccessfulGeneration)
	require.Equal(t, "run-6", state.LastSuccessfulRunID)
	require.Equal(t, newFinish, *state.LastSuccessfulAt)
}

func TestUpsertSyncRunReportRetainsNewestTenAcrossTerminalPhases(t *testing.T) {
	db, repo := newRepositoryForTest(t)
	createTestProfile(t, db, "profile-a")
	phases := []string{SyncRunPhaseCompleted, SyncRunPhaseFailed, SyncRunPhaseCanceled}
	for generation := uint64(1); generation <= 11; generation++ {
		require.NoError(t, repo.UpsertSyncRunReport(&SyncRunReport{
			ProfileID: "profile-a", RunID: "run-" + string(rune('a'+generation-1)),
			Generation: generation, Phase: phases[(generation-1)%uint64(len(phases))],
			SnapshotJSON: `{"terminal":true}`,
		}))
	}

	reports, err := repo.ListSyncRunReports("profile-a", 100)
	require.NoError(t, err)
	require.Len(t, reports, 10)
	for index, report := range reports {
		require.Equal(t, uint64(11-index), report.Generation)
	}
	oldest, err := repo.GetSyncRunReport("profile-a", "run-a")
	require.NoError(t, err)
	require.Nil(t, oldest)
	latest, err := repo.GetLatestSyncRunReport("profile-a")
	require.NoError(t, err)
	require.NotNil(t, latest)
	require.Equal(t, uint64(11), latest.Generation)
}

func TestMigrateLegacyLastSyncToLastAttemptedAtIdempotently(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	legacy := time.Date(2026, time.September, 15, 8, 0, 0, 0, time.UTC)
	driver := &PureSQLiteDriver{}
	raw, err := driver.Connect(&DatabaseConfig{Type: DatabaseTypeSQLite, Path: dbPath}, logger.Get())
	require.NoError(t, err)
	require.NoError(t, raw.Exec(`CREATE TABLE sync_profiles (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, active BOOLEAN
	)`).Error)
	require.NoError(t, raw.Exec(`INSERT INTO sync_profiles (id, name, active) VALUES (?, ?, ?)`,
		"profile-a", "profile-a", true).Error)
	require.NoError(t, raw.Exec(`CREATE TABLE profile_sync_states (
		profile_id TEXT PRIMARY KEY, state_data TEXT, last_sync DATETIME,
		created_at DATETIME, updated_at DATETIME
	)`).Error)
	require.NoError(t, raw.Exec(`INSERT INTO profile_sync_states
		(profile_id, state_data, last_sync, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		"profile-a", "{}", legacy, legacy, legacy).Error)
	sqlDB, err := raw.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	db, err := NewDatabase(&DatabaseConfig{Type: DatabaseTypeSQLite, Path: dbPath}, logger.Get())
	require.NoError(t, err)
	repo := NewRepository(db, nil, logger.Get())
	state, err := repo.GetSyncState("profile-a")
	require.NoError(t, err)
	require.Equal(t, legacy, *state.LastSync)
	require.Equal(t, legacy, *state.LastAttemptedAt)
	require.Nil(t, state.LastSuccessfulAt)
	require.Zero(t, state.LastSuccessfulGeneration)
	require.NoError(t, db.Close())

	db, err = NewDatabase(&DatabaseConfig{Type: DatabaseTypeSQLite, Path: dbPath}, logger.Get())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	state, err = NewRepository(db, nil, logger.Get()).GetSyncState("profile-a")
	require.NoError(t, err)
	require.Equal(t, legacy, *state.LastAttemptedAt)
	require.Nil(t, state.LastSuccessfulAt)
}
