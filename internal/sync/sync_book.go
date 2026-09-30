package sync

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
)

type operationUserBookSnapshotKey struct{}

type operationUserBookSnapshots struct {
	mu        sync.RWMutex
	userBooks map[int]*models.HardcoverBook
	// reads holds a user book's reads fetched in the same request as the user
	// book, before any mutation in this operation. Each entry is used once.
	reads map[int][]hardcover.UserBookRead
}

func withOperationUserBookSnapshots(ctx context.Context) context.Context {
	return context.WithValue(ctx, operationUserBookSnapshotKey{}, &operationUserBookSnapshots{
		userBooks: make(map[int]*models.HardcoverBook),
		reads:     make(map[int][]hardcover.UserBookRead),
	})
}

func setOperationUserBookReadsSnapshot(ctx context.Context, userBookID int, reads []hardcover.UserBookRead) {
	if snapshots := operationUserBookSnapshotsFromContext(ctx); snapshots != nil {
		snapshots.mu.Lock()
		snapshots.reads[userBookID] = reads
		snapshots.mu.Unlock()
	}
}

// takeOperationUserBookReadsSnapshot returns and removes the reads fetched
// together with the user book, so a later call in the same operation reads fresh.
func takeOperationUserBookReadsSnapshot(ctx context.Context, userBookID int) ([]hardcover.UserBookRead, bool) {
	snapshots := operationUserBookSnapshotsFromContext(ctx)
	if snapshots == nil {
		return nil, false
	}
	snapshots.mu.Lock()
	defer snapshots.mu.Unlock()
	reads, found := snapshots.reads[userBookID]
	delete(snapshots.reads, userBookID)
	return reads, found
}

// hardcoverUserBookWithReadsClient is implemented by the concrete client, which
// can return a user book and its reads in one request.
type hardcoverUserBookWithReadsClient interface {
	GetUserBookWithReads(context.Context, string) (*models.HardcoverBook, []hardcover.UserBookRead, error)
}

// getUserBookReads returns a user book's reads, using the copy fetched together
// with the user book when this operation has one and requesting them otherwise.
func (s *Service) getUserBookReads(ctx context.Context, userBookID int64) ([]hardcover.UserBookRead, error) {
	if reads, found := takeOperationUserBookReadsSnapshot(ctx, int(userBookID)); found {
		return reads, nil
	}
	return s.hardcover.GetUserBookReads(ctx, hardcover.GetUserBookReadsInput{UserBookID: userBookID})
}

func operationUserBookSnapshotsFromContext(ctx context.Context) *operationUserBookSnapshots {
	if ctx == nil {
		return nil
	}
	snapshots, _ := ctx.Value(operationUserBookSnapshotKey{}).(*operationUserBookSnapshots)
	return snapshots
}

func (s *Service) getUserBookSnapshot(ctx context.Context, userBookID int) (*models.HardcoverBook, bool) {
	if snapshots := operationUserBookSnapshotsFromContext(ctx); snapshots != nil {
		snapshots.mu.RLock()
		book, found := snapshots.userBooks[userBookID]
		snapshots.mu.RUnlock()
		if found {
			return book, true
		}
	}
	if s.userBookCache == nil {
		return nil, false
	}
	return s.userBookCache.GetByUserBook(userBookID)
}

func setOperationUserBookSnapshot(ctx context.Context, userBookID int, book *models.HardcoverBook) {
	if snapshots := operationUserBookSnapshotsFromContext(ctx); snapshots != nil && book != nil {
		snapshots.mu.Lock()
		snapshots.userBooks[userBookID] = book
		snapshots.mu.Unlock()
	}
}

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
	return s.syncBook(ctx, book, nil, syncState, statePath)
}

// SyncBookWithEdition follows the same per-book sync path as SyncBook while
// reusing a fresh edition snapshot supplied by the operation that created the
// matching association. The snapshot must match the association in syncState.
func (s *Service) SyncBookWithEdition(ctx context.Context, book models.AudiobookshelfBook, verifiedEdition *models.Edition, syncState *state.State, statePath string) (BookResyncResult, error) {
	if err := validateResyncEdition(book, verifiedEdition, syncState); err != nil {
		return BookResyncResult{}, err
	}
	return s.syncBook(ctx, book, verifiedEdition, syncState, statePath)
}

func validateResyncEdition(book models.AudiobookshelfBook, edition *models.Edition, syncState *state.State) error {
	if book.ID == "" {
		return errors.New("audiobookshelf item ID is required")
	}
	if edition == nil {
		return errors.New("verified Hardcover edition is required")
	}
	if syncState == nil {
		return errors.New("sync state is required")
	}
	association, exists := syncState.GetAssociation(book.ID)
	if !exists {
		return fmt.Errorf("verified Hardcover edition has no saved association for Audiobookshelf item %s", book.ID)
	}
	if !associationMatchesBook(association, book) {
		return fmt.Errorf("verified Hardcover edition has no matching saved association for Audiobookshelf item %s", book.ID)
	}
	editionID, editionIDErr := strconv.Atoi(edition.ID)
	bookID, bookIDErr := strconv.Atoi(edition.BookID)
	formatID, formatIDErr := strconv.Atoi(edition.ReadingFormatID)
	if editionIDErr != nil || editionID <= 0 || bookIDErr != nil || bookID <= 0 || formatIDErr != nil ||
		(association.ReadingFormat != models.ReadingFormatAudiobook && association.ReadingFormat != models.ReadingFormatEbook) ||
		association.ABSItemID != book.ID || association.HardcoverEditionID != edition.ID || association.HardcoverBookID != edition.BookID ||
		formatID != models.ReadingFormatID(association.ReadingFormat) {
		return fmt.Errorf("verified Hardcover edition does not match the saved association for Audiobookshelf item %s", book.ID)
	}
	return nil
}

func (s *Service) syncBook(ctx context.Context, book models.AudiobookshelfBook, verifiedEdition *models.Edition, syncState *state.State, statePath string) (BookResyncResult, error) {
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
		if ctxErr := ctx.Err(); ctxErr != nil {
			return BookResyncResult{}, ctxErr
		}
		if !hasReliableEmbeddedProgress(book) {
			return BookResyncResult{}, fmt.Errorf("failed to fetch user progress data: %w", err)
		}
		userProgress = nil
	}
	if err := ctx.Err(); err != nil {
		return BookResyncResult{}, err
	}

	processErr := s.processBookWithVerifiedEdition(ctx, book, userProgress, verifiedEdition)
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
