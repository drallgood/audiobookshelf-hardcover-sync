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
