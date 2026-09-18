package sync

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/stretchr/testify/require"
)

func TestRunPhaseTransitionsAreLegalAndTerminalPhasesAreImmutable(t *testing.T) {
	svc, _ := createTestService()
	svc.beginOutcomeRun()

	snapshot := svc.GetSnapshotStatus()
	require.Equal(t, string(RunPhaseQueued), snapshot.State)
	require.False(t, snapshot.QueuedAt.IsZero())

	require.False(t, svc.transitionRunPhase(RunPhaseFinalizing, nil))
	require.Equal(t, string(RunPhaseQueued), svc.GetSnapshotStatus().State)
	require.True(t, svc.transitionRunPhase(RunPhaseRunning, nil))
	require.True(t, svc.transitionRunPhase(RunPhaseFinalizing, nil))
	require.True(t, svc.transitionRunPhase(RunPhaseCompleted, nil))

	completed := svc.GetSnapshotStatus()
	require.Equal(t, string(RunPhaseCompleted), completed.State)
	require.False(t, completed.FinishedAt.IsZero())
	require.False(t, completed.ProcessingStartedAt.IsZero())
	require.False(t, svc.transitionRunPhase(RunPhaseFailed, errors.New("stale failure")))

	terminal := svc.GetSnapshotStatus()
	require.Equal(t, completed.State, terminal.State)
	require.Equal(t, completed.FinishedAt, terminal.FinishedAt)
	require.Empty(t, terminal.RunError)
}

func TestCancellationReservationWinsTerminalTransition(t *testing.T) {
	t.Run("cancellation reservation rejects finalization", func(t *testing.T) {
		svc, _ := createTestService()
		svc.beginOutcomeRun()
		require.True(t, svc.transitionRunPhase(RunPhaseRunning, nil))
		require.True(t, svc.RequestCancellation())
		require.False(t, svc.transitionRunPhase(RunPhaseFinalizing, nil))
		require.True(t, svc.transitionRunPhase(RunPhaseCanceled, context.Canceled))
		require.False(t, svc.RequestCancellation())

		snapshot := svc.GetSnapshotStatus()
		require.Equal(t, string(RunPhaseCanceled), snapshot.State)
		require.Equal(t, context.Canceled.Error(), snapshot.RunError)
	})

	t.Run("finalization reservation rejects later cancellation", func(t *testing.T) {
		svc, _ := createTestService()
		svc.beginOutcomeRun()
		require.True(t, svc.transitionRunPhase(RunPhaseRunning, nil))
		require.True(t, svc.transitionRunPhase(RunPhaseFinalizing, nil))
		require.False(t, svc.RequestCancellation())
		require.True(t, svc.transitionRunPhase(RunPhaseCompleted, errors.New("late completion")))

		snapshot := svc.GetSnapshotStatus()
		require.Equal(t, string(RunPhaseCompleted), snapshot.State)
		require.Equal(t, "late completion", snapshot.RunError)
	})
}

func TestTerminalTransitionWinsCancellationReservation(t *testing.T) {
	for _, test := range []struct {
		name        string
		terminalize func(*Service) bool
		wantState   RunPhase
		wantError   string
	}{
		{
			name: "completed",
			terminalize: func(svc *Service) bool {
				return svc.transitionRunPhase(RunPhaseFinalizing, nil) &&
					svc.transitionRunPhase(RunPhaseCompleted, nil)
			},
			wantState: RunPhaseCompleted,
		},
		{
			name: "failed",
			terminalize: func(svc *Service) bool {
				return svc.transitionRunPhase(RunPhaseFailed, errors.New("terminal failure"))
			},
			wantState: RunPhaseFailed,
			wantError: "terminal failure",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := createTestService()
			svc.beginOutcomeRun()
			require.True(t, svc.transitionRunPhase(RunPhaseRunning, nil))
			require.True(t, test.terminalize(svc))
			require.False(t, svc.RequestCancellation())
			snapshot := svc.GetSnapshotStatus()
			require.Equal(t, string(test.wantState), snapshot.State)
			require.Equal(t, test.wantError, snapshot.RunError)
		})
	}
}

func TestRunPhaseAllowsOnlyDocumentedTerminalPaths(t *testing.T) {
	tests := []struct {
		name string
		path []RunPhase
	}{
		{name: "queued canceled", path: []RunPhase{RunPhaseCanceled}},
		{name: "queued failed", path: []RunPhase{RunPhaseFailed}},
		{name: "running canceled", path: []RunPhase{RunPhaseRunning, RunPhaseCanceled}},
		{name: "running failed", path: []RunPhase{RunPhaseRunning, RunPhaseFailed}},
		{name: "finalizing completed", path: []RunPhase{RunPhaseRunning, RunPhaseFinalizing, RunPhaseCompleted}},
		{name: "finalizing canceled", path: []RunPhase{RunPhaseRunning, RunPhaseFinalizing, RunPhaseCanceled}},
		{name: "finalizing failed", path: []RunPhase{RunPhaseRunning, RunPhaseFinalizing, RunPhaseFailed}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			svc, _ := createTestService()
			svc.beginOutcomeRun()
			for _, phase := range test.path {
				require.True(t, svc.transitionRunPhase(phase, errors.New("run ended")))
			}
			require.Equal(t, string(test.path[len(test.path)-1]), svc.GetSnapshotStatus().State)
		})
	}
}

func TestRunPhaseTerminalCancellationRetainsPartialSnapshot(t *testing.T) {
	svc, _ := createTestService()
	svc.beginOutcomeRun()
	require.True(t, svc.transitionRunPhase(RunPhaseRunning, nil))
	book := *toAudiobookshelfBook(createTestBook("partial-cancel", "Partial", "Author", "", ""))
	svc.recordBookOutcomeWithMatchMethod(book, OutcomeNeedsReview, "needs verification", nil, nil, "title_author")
	svc.recordLibraryCandidateTotal("library", 3)

	runErr := errors.New("context canceled while processing")
	require.True(t, svc.transitionRunPhase(RunPhaseCanceled, runErr))
	snapshot := svc.GetSnapshot()
	require.Equal(t, string(RunPhaseCanceled), snapshot.State)
	require.Equal(t, runErr.Error(), snapshot.RunError)
	require.Equal(t, int32(1), snapshot.ProcessedSoFar)
	require.Equal(t, int32(1), snapshot.OutcomeCounts.NeedsReview)
	require.Equal(t, int32(2), snapshot.UnattemptedCount)
	require.Len(t, snapshot.BookOutcomes, 1)
	require.Equal(t, snapshot.OutcomeCounts.Total(), snapshot.ProcessedSoFar)
}

func TestSnapshotActivityAndProcessedTimestampsFollowTheirEvents(t *testing.T) {
	svc, _ := createTestService()
	svc.beginOutcomeRun()
	queued := svc.GetSnapshotStatus()

	time.Sleep(time.Millisecond)
	require.True(t, svc.transitionRunPhase(RunPhaseRunning, nil))
	running := svc.GetSnapshotStatus()
	require.True(t, running.ProcessingStartedAt.After(queued.QueuedAt))
	require.True(t, running.LastActivityAt.After(queued.LastActivityAt))
	require.True(t, running.LastProcessedAt.IsZero())

	time.Sleep(time.Millisecond)
	svc.recordLibraryCandidateTotal("library", 2)
	candidate := svc.GetSnapshotStatus()
	require.True(t, candidate.LastActivityAt.After(running.LastActivityAt))
	require.True(t, candidate.LastProcessedAt.IsZero())

	time.Sleep(time.Millisecond)
	book := *toAudiobookshelfBook(createTestBook("activity", "Activity", "Author", "", ""))
	svc.recordBookOutcomeWithMatchMethod(book, OutcomeSkipped, "configured skip", nil, nil, "")
	processed := svc.GetSnapshotStatus()
	require.True(t, processed.LastActivityAt.After(candidate.LastActivityAt))
	require.True(t, processed.LastProcessedAt.After(candidate.LastProcessedAt))

	time.Sleep(time.Millisecond)
	svc.enrichAttentionCandidate(mismatch.BookMismatch{BookID: book.ID, Reason: "enriched"})
	enriched := svc.GetSnapshotStatus()
	require.True(t, enriched.LastActivityAt.After(processed.LastActivityAt))
	require.Equal(t, processed.LastProcessedAt, enriched.LastProcessedAt)
}

func TestSnapshotReportsDryRunAndReconcilesUnattemptedCount(t *testing.T) {
	svc, _ := createTestService()
	svc.config.Sync.DryRun = true
	svc.beginOutcomeRun()
	svc.recordLibraryCandidateTotal("library", 5)

	for i, outcome := range []SyncOutcome{OutcomeWouldSync, OutcomeAlreadyCurrent, OutcomeSkipped} {
		book := *toAudiobookshelfBook(createTestBook(
			"reconcile-"+string(rune('a'+i)),
			"Reconcile",
			"Author",
			"",
			"",
		))
		svc.recordBookOutcomeWithMatchMethod(book, outcome, "test", nil, nil, "")
	}

	snapshot := svc.GetSnapshotStatus()
	require.True(t, snapshot.DryRun)
	require.Equal(t, int32(5), snapshot.BooksTotal)
	require.Equal(t, int32(3), snapshot.ProcessedSoFar)
	require.Equal(t, snapshot.OutcomeCounts.Total(), snapshot.ProcessedSoFar)
	require.Equal(t, int32(2), snapshot.UnattemptedCount)
}

func TestNewServiceWithRunIdentityRetainsOpaqueAcceptedRun(t *testing.T) {
	cfg := createTestConfig(false)
	cfg.Sync.StateFile = filepath.Join(t.TempDir(), "state.json")
	cfg.Paths.CacheDir = t.TempDir()
	queuedAt := time.Date(2026, 9, 16, 1, 2, 3, 4, time.FixedZone("test", -4*60*60))
	runID := "profile/id:opaque-run"

	svc, err := NewServiceWithRunIdentity(nil, &MockHardcoverClient{}, cfg, runID, queuedAt)
	require.NoError(t, err)
	snapshot := svc.GetSnapshotStatus()
	require.Equal(t, runID, snapshot.RunID)
	require.Equal(t, string(RunPhaseQueued), snapshot.State)
	require.Equal(t, queuedAt.UTC(), snapshot.QueuedAt)

	// beginOutcomeRun is called by Sync and must not replace the owner-issued
	// opaque ID or accepted-start timestamp.
	svc.beginOutcomeRun()
	snapshot = svc.GetSnapshotStatus()
	require.Equal(t, runID, snapshot.RunID)
	require.Equal(t, queuedAt.UTC(), snapshot.QueuedAt)
}
