package api

import (
	"encoding/json"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/types"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
	"github.com/stretchr/testify/require"
)

func TestSyncSummaryResponseUsesCanonicalFields(t *testing.T) {
	data, err := json.Marshal(types.SyncSummaryResponse{
		ProcessedSoFar: 3,
		ProcessedCount: 3,
		OutcomeCounts:  sync.OutcomeCounts{Synced: 2, NeedsReview: 1},
		BookOutcomes:   []sync.BookOutcomeRecord{{BookID: "book-1", Outcome: sync.OutcomeNeedsReview}},
	})
	require.NoError(t, err)
	require.Contains(t, string(data), `"processed_count":3`)
	require.Contains(t, string(data), `"outcome_counts"`)
	for _, retired := range []string{"total_books_processed", "books_synced", "books_not_found", "mismatches", "last_sync_summary"} {
		require.NotContains(t, string(data), retired)
	}
}
