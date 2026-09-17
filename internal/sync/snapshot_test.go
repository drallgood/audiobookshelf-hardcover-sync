package sync

import (
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/stretchr/testify/require"
)

func TestSnapshotDeepCopiesCanonicalOutcomeDetails(t *testing.T) {
	svc, _ := createTestService()
	svc.beginOutcomeRun()
	book := *toAudiobookshelfBook(createTestBook("snapshot-copy", "Snapshot Copy", "Author", "", ""))
	svc.recordBookOutcomeWithMatchMethod(book, OutcomeNeedsReview, "manual review", nil, nil, "")
	svc.enrichAttentionCandidate(mismatch.BookMismatch{
		BookID:      book.ID,
		AuthorIDs:   []int{11},
		NarratorIDs: []int{22},
		Reason:      "manual review",
	})
	svc.recordBookOutcomeWithMatchMethod(book, OutcomeNotFound, "not found", nil, nil, "")

	first := svc.GetSnapshot()
	require.Len(t, first.BookOutcomes, 1)
	first.BookOutcomes[0].Title = "caller mutation"

	// A distinct needs-review item exercises deep-copying of mismatch-owned
	// slices while the outcome and legacy stores are read together.
	otherBook := *toAudiobookshelfBook(createTestBook("snapshot-mismatch", "Mismatch", "Author", "", ""))
	svc.recordBookOutcomeWithMatchMethod(otherBook, OutcomeNeedsReview, "review", nil, nil, "")
	svc.enrichAttentionCandidate(mismatch.BookMismatch{
		BookID:      otherBook.ID,
		AuthorIDs:   []int{33},
		NarratorIDs: []int{44},
		Reason:      "review",
	})
	first = svc.GetSnapshot()
	require.Len(t, first.BookOutcomes, 2)
	first.BookOutcomes[0].Title = "changed"

	second := svc.GetSnapshot()
	require.Len(t, second.BookOutcomes, 2)
	require.Equal(t, "Snapshot Copy", second.BookOutcomes[0].Title)
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
	require.False(t, status.QueuedAt.IsZero())
	require.Equal(t, string(RunPhaseQueued), status.State)
	require.Equal(t, int32(2), status.BooksTotal)
	require.Equal(t, int32(2), status.ProcessedSoFar)
	require.Equal(t, OutcomeCounts{NeedsReview: 1, NotFound: 1}, status.OutcomeCounts)
	require.Nil(t, status.BookOutcomes)
	require.Empty(t, status.AudiobookshelfURL, "aggregate status snapshots omit profile configuration")

	full := svc.GetSnapshot()
	require.Equal(t, "https://audiobookshelf.example/base", full.AudiobookshelfURL)
	require.Len(t, full.BookOutcomes, 2)
	for _, record := range full.BookOutcomes {
		require.Equal(t, "https://audiobookshelf.example/base/api/items/"+record.BookID+"/cover", record.CoverURL)
		require.Equal(t, "Audiobook", record.Format)
		require.Equal(t, "Test Series", record.Series)
		require.Equal(t, "2", record.SeriesNumber)
	}
}

func TestSnapshotStatusKeepsUnknownTotalSeparateFromProcessedOutcomes(t *testing.T) {
	svc, _ := createTestService()
	svc.beginOutcomeRun()

	book := *toAudiobookshelfBook(createTestBook("unknown-total", "Unknown total", "Author", "", ""))
	svc.recordBookOutcomeWithMatchMethod(book, OutcomeNotFound, "not found", nil, nil, "")

	status := svc.GetSnapshotStatus()
	require.Zero(t, status.BooksTotal, "the denominator remains unknown until a library count is observed")
	require.Equal(t, int32(1), status.ProcessedSoFar)
	require.Equal(t, OutcomeCounts{NotFound: 1}, status.OutcomeCounts)
	require.Equal(t, int32(1), status.ProcessedSoFar)
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
			name:           "omits credential-bearing opaque URL",
			audiobookshelf: "https:reader:secret@audiobookshelf.example/base",
			wantURL:        "",
			wantCoverURL:   "",
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
			require.Equal(t, tt.wantCoverURL, snapshot.BookOutcomes[0].CoverURL)
		})
	}
}
