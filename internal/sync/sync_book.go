package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
)

// BookResyncResult is the final outcome of a single-book resync.
type BookResyncResult struct {
	Outcome SyncOutcome `json:"outcome"`
	Reason  string      `json:"reason,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// SyncBook synchronizes one Audiobookshelf item through the same per-book path
// a full sync uses, so progress, ownership, finished-state, checkpoint, and
// dry-run mutation boundaries are unchanged.
//
// The caller must already hold the cross-process lock for statePath and must
// guarantee that no full sync is running for the same profile. syncState is the
// state loaded under that lock; it is checkpointed to statePath after the book
// unless the service is in dry run, in which case nothing is persisted.
func (s *Service) SyncBook(ctx context.Context, book models.AudiobookshelfBook, syncState *state.State, statePath string) (BookResyncResult, error) {
	if book.ID == "" {
		return BookResyncResult{}, errors.New("audiobookshelf item ID is required")
	}
	if syncState == nil {
		return BookResyncResult{}, errors.New("sync state is required")
	}
	if err := ctx.Err(); err != nil {
		return BookResyncResult{}, err
	}

	s.mismatchCollector = mismatch.NewCollector()
	s.beginOutcomeRun()
	s.state = syncState
	s.statePath = statePath
	s.hardcover.ClearUserBookCache()
	s.createdReadsMutex.Lock()
	s.createdReadsThisRun = make(map[int64]struct{})
	s.createdReadsMutex.Unlock()

	userProgress, err := s.audiobookshelf.GetUserProgress(ctx)
	if err != nil {
		return BookResyncResult{}, fmt.Errorf("failed to fetch user progress data: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return BookResyncResult{}, err
	}

	processErr := s.processBook(ctx, book, userProgress)
	if checkpointErr := s.checkpointState(book.ID); checkpointErr != nil {
		return BookResyncResult{}, checkpointErr
	}
	if !s.config.Sync.DryRun {
		if cacheErr := s.userBookCache.Save(); cacheErr != nil {
			s.log.Warn("Failed to save persistent user book cache", map[string]interface{}{
				"error": cacheErr.Error(),
			})
		}
	}
	if err := ctx.Err(); err != nil {
		return BookResyncResult{}, err
	}

	s.runStateMutex.RLock()
	record, recorded := s.outcomeRecords[book.ID]
	s.runStateMutex.RUnlock()
	if !recorded {
		if processErr != nil {
			return BookResyncResult{}, fmt.Errorf("failed to sync book %s: %w", book.ID, processErr)
		}
		return BookResyncResult{}, fmt.Errorf("sync of book %s produced no outcome", book.ID)
	}
	return BookResyncResult{Outcome: record.Outcome, Reason: record.Reason, Error: record.Error}, nil
}
