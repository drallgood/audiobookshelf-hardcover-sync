package types

import (
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

// SyncSummaryResponse represents the sync summary data returned by the API
type SyncSummaryResponse struct {
	Snapshot *sync.SyncSnapshot `json:"snapshot,omitempty"`

	// These fields describe one coherent current or completed run. The legacy
	// fields below remain available for existing API clients.
	UserID              string                   `json:"user_id,omitempty"`
	RunID               string                   `json:"run_id,omitempty"`
	RunStartedAt        time.Time                `json:"run_started_at,omitempty"`
	QueuedAt            time.Time                `json:"queued_at,omitempty"`
	ProcessingStartedAt time.Time                `json:"processing_started_at,omitempty"`
	LastActivityAt      time.Time                `json:"last_activity_at,omitempty"`
	LastProcessedAt     time.Time                `json:"last_processed_at,omitempty"`
	FinishedAt          time.Time                `json:"finished_at,omitempty"`
	DryRun              bool                     `json:"dry_run"`
	RunError            string                   `json:"run_error,omitempty"`
	UnattemptedCount    int32                    `json:"unattempted_count"`
	State               string                   `json:"state,omitempty"`
	LastAttemptedAt     *time.Time               `json:"last_attempted_at,omitempty"`
	LastSuccessfulAt    *time.Time               `json:"last_successful_at,omitempty"`
	BooksTotal          int32                    `json:"books_total"`
	ProcessedSoFar      int32                    `json:"processed_so_far"`
	ProcessedCount      int32                    `json:"processed_count"`
	OutcomeCounts       sync.OutcomeCounts       `json:"outcome_counts"`
	BookOutcomes        []sync.BookOutcomeRecord `json:"book_outcomes"`
	AttentionRecords    []sync.BookOutcomeRecord `json:"attention_records"`

	TotalBooksProcessed int32                   `json:"total_books_processed"`
	BooksSynced         int32                   `json:"books_synced"`
	BooksNotFound       []BookNotFoundInfo      `json:"books_not_found"`
	Mismatches          []mismatch.BookMismatch `json:"mismatches"`
}

// BookNotFoundInfo represents a book that couldn't be found in Hardcover
type BookNotFoundInfo struct {
	BookID string `json:"book_id"`
	Title  string `json:"title"`
	Author string `json:"author"`
	ASIN   string `json:"asin,omitempty"`
	ISBN   string `json:"isbn,omitempty"`
	Error  string `json:"error"`
}
