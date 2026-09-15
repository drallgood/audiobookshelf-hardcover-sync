package api

import (
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/multiuser"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

func TestSummaryFromProfileStatusUsesPersistedProcessedCount(t *testing.T) {
	lastSync := time.Now()

	tests := []struct {
		name            string
		lastSyncSummary *sync.SyncSummary
		wantProcessed   int32
	}{
		{
			name: "persisted summary",
			lastSyncSummary: &sync.SyncSummary{
				TotalBooksProcessed: 3,
			},
			wantProcessed: 3,
		},
		{
			name:          "legacy status fallback",
			wantProcessed: 9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &multiuser.SyncProfileStatus{
				LastSync:        &lastSync,
				BooksTotal:      9,
				LastSyncSummary: tt.lastSyncSummary,
			}

			got := summaryFromProfileStatus(status)
			if got == nil {
				t.Fatal("summaryFromProfileStatus() returned nil")
			}
			if got.TotalBooksProcessed != tt.wantProcessed {
				t.Fatalf("TotalBooksProcessed = %d, want %d", got.TotalBooksProcessed, tt.wantProcessed)
			}
		})
	}
}
