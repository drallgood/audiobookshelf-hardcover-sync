package sync

import (
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/stretchr/testify/require"
)

func TestSnapshotDeepCopiesLegacyAttentionDetails(t *testing.T) {
	svc, _ := createTestService()
	svc.beginOutcomeRun()
	book := *toAudiobookshelfBook(createTestBook("snapshot-copy", "Snapshot Copy", "Author", "", ""))
	svc.recordBookOutcomeWithMatchMethod(book, OutcomeNeedsReview, "manual review", nil, nil, "")
	svc.enrichLiveMismatch(mismatch.BookMismatch{
		BookID:      book.ID,
		AuthorIDs:   []int{11},
		NarratorIDs: []int{22},
		Reason:      "manual review",
	})
	svc.recordBookOutcomeWithMatchMethod(book, OutcomeNotFound, "not found", nil, nil, "")

	first := svc.GetSnapshot()
	require.Len(t, first.Mismatches, 0, "replacing attention with not_found removes its mismatch")
	require.Len(t, first.BooksNotFound, 1)
	first.BooksNotFound[0].Title = "caller mutation"

	// A distinct needs-review item exercises deep-copying of mismatch-owned
	// slices while the outcome and legacy stores are read together.
	otherBook := *toAudiobookshelfBook(createTestBook("snapshot-mismatch", "Mismatch", "Author", "", ""))
	svc.recordBookOutcomeWithMatchMethod(otherBook, OutcomeNeedsReview, "review", nil, nil, "")
	svc.enrichLiveMismatch(mismatch.BookMismatch{
		BookID:      otherBook.ID,
		AuthorIDs:   []int{33},
		NarratorIDs: []int{44},
		Reason:      "review",
	})
	first = svc.GetSnapshot()
	require.Len(t, first.Mismatches, 1)
	first.Mismatches[0].AuthorIDs[0] = 99
	first.Mismatches[0].NarratorIDs[0] = 98

	second := svc.GetSnapshot()
	require.Len(t, second.BooksNotFound, 1)
	require.Equal(t, "Snapshot Copy", second.BooksNotFound[0].Title)
	require.Len(t, second.Mismatches, 1)
	require.Equal(t, []int{33}, second.Mismatches[0].AuthorIDs)
	require.Equal(t, []int{44}, second.Mismatches[0].NarratorIDs)
	require.Equal(t, second.ProcessedSoFar, second.OutcomeCounts.Total())
}

func TestSnapshotStatusCopiesScalarsWithoutDetails(t *testing.T) {
	svc, _ := createTestService()
	svc.beginOutcomeRun()

	svc.summary.Lock()
	svc.summary.UserID = "profile-a"
	svc.summary.BooksTotal = 2
	svc.summary.Unlock()

	needsReview := *toAudiobookshelfBook(createTestBook("snapshot-status-review", "Review", "Author", "", ""))
	svc.recordBookOutcomeWithMatchMethod(needsReview, OutcomeNeedsReview, "manual review", nil, nil, "")
	notFound := *toAudiobookshelfBook(createTestBook("snapshot-status-missing", "Missing", "Author", "", ""))
	svc.recordBookOutcomeWithMatchMethod(notFound, OutcomeNotFound, "not found", nil, nil, "")

	status := svc.GetSnapshotStatus()
	require.Equal(t, "profile-a", status.UserID)
	require.NotEmpty(t, status.RunID)
	require.False(t, status.RunStartedAt.IsZero())
	require.Equal(t, "syncing", status.State)
	require.Equal(t, int32(2), status.BooksTotal)
	require.Equal(t, int32(2), status.ProcessedSoFar)
	require.Equal(t, int32(2), status.ProcessedCount)
	require.Equal(t, OutcomeCounts{NeedsReview: 1, NotFound: 1}, status.OutcomeCounts)
	require.Equal(t, int32(2), status.TotalBooksProcessed)
	require.Zero(t, status.BooksSynced)
	require.Nil(t, status.BookOutcomes)
	require.Nil(t, status.AttentionRecords)
	require.Nil(t, status.BooksNotFound)
	require.Nil(t, status.Mismatches)

	full := svc.GetSnapshot()
	require.Len(t, full.BookOutcomes, 2)
	require.Len(t, full.AttentionRecords, 2)
	require.Len(t, full.BooksNotFound, 1)
	require.Len(t, full.Mismatches, 1)
}
