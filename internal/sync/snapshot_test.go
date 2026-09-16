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
	svc.config.Audiobookshelf.URL = "https://audiobookshelf.example/base"
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
	require.Empty(t, status.AudiobookshelfURL, "aggregate status snapshots omit profile configuration")

	full := svc.GetSnapshot()
	require.Equal(t, "https://audiobookshelf.example/base", full.AudiobookshelfURL)
	require.Len(t, full.BookOutcomes, 2)
	require.Len(t, full.AttentionRecords, 2)
	for _, record := range full.BookOutcomes {
		require.Equal(t, "https://audiobookshelf.example/base/api/items/"+record.BookID+"/cover", record.CoverURL)
		require.Equal(t, "Audiobook", record.Format)
		require.Equal(t, "Test Series", record.Series)
		require.Equal(t, "2", record.SeriesNumber)
	}
	require.Len(t, full.BooksNotFound, 1)
	require.Len(t, full.Mismatches, 1)
}

func TestSnapshotStatusKeepsUnknownTotalSeparateFromProcessedOutcomes(t *testing.T) {
	svc, _ := createTestService()
	svc.beginOutcomeRun()

	book := *toAudiobookshelfBook(createTestBook("unknown-total", "Unknown total", "Author", "", ""))
	svc.recordBookOutcomeWithMatchMethod(book, OutcomeNotFound, "not found", nil, nil, "")

	status := svc.GetSnapshotStatus()
	require.Zero(t, status.BooksTotal, "the denominator remains unknown until a library count is observed")
	require.Equal(t, int32(1), status.ProcessedSoFar)
	require.Equal(t, int32(1), status.ProcessedCount)
	require.Equal(t, OutcomeCounts{NotFound: 1}, status.OutcomeCounts)
	require.Equal(t, int32(1), status.TotalBooksProcessed)
}

func TestSnapshotSanitizesAudiobookshelfURLs(t *testing.T) {
	tests := []struct {
		name           string
		audiobookshelf string
		wantURL        string
		wantCoverURL   string
	}{
		{
			name:           "strips userinfo",
			audiobookshelf: "https://reader:secret@audiobookshelf.example/base",
			wantURL:        "https://audiobookshelf.example/base",
			wantCoverURL:   "https://audiobookshelf.example/base/api/items/snapshot-sanitize/cover",
		},
		{
			name:           "preserves credential-free URL",
			audiobookshelf: "https://audiobookshelf.example/base",
			wantURL:        "https://audiobookshelf.example/base",
			wantCoverURL:   "https://audiobookshelf.example/base/api/items/snapshot-sanitize/cover",
		},
		{
			name:           "strips query and fragment",
			audiobookshelf: "https://reader:secret@audiobookshelf.example/base?token=query-secret#fragment",
			wantURL:        "https://audiobookshelf.example/base",
			wantCoverURL:   "https://audiobookshelf.example/base/api/items/snapshot-sanitize/cover",
		},
		{
			name:           "omits unparsable URL",
			audiobookshelf: "https://reader:%zz@audiobookshelf.example/base",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := createTestService()
			svc.config.Audiobookshelf.URL = tt.audiobookshelf
			svc.beginOutcomeRun()
			book := *toAudiobookshelfBook(createTestBook("snapshot-sanitize", "Snapshot", "Author", "", ""))
			svc.recordBookOutcomeWithMatchMethod(book, OutcomeNeedsReview, "manual review", nil, nil, "")

			snapshot := svc.GetSnapshot()
			require.Equal(t, tt.wantURL, snapshot.AudiobookshelfURL)
			require.Len(t, snapshot.BookOutcomes, 1)
			require.Len(t, snapshot.AttentionRecords, 1)
			require.Len(t, snapshot.Mismatches, 1)
			require.Equal(t, tt.wantCoverURL, snapshot.BookOutcomes[0].CoverURL)
			require.Equal(t, tt.wantCoverURL, snapshot.AttentionRecords[0].CoverURL)
			require.Equal(t, tt.wantCoverURL, snapshot.Mismatches[0].CoverURL)
			require.Equal(t, tt.wantCoverURL, snapshot.Mismatches[0].ImageURL)
		})
	}
}
