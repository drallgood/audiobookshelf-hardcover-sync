package multiuser

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition/draft"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/isbn"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
)

// EditionCreateTimeout bounds one edition creation, including the cover
// upload. The HTTP layer sizes its response write deadline from it.
const EditionCreateTimeout = 2 * time.Minute

var (
	// ErrEditionNotFound indicates that the run, or the book record within it, does not exist.
	ErrEditionNotFound = errors.New("sync run or book record not found")

	// ErrEditionNotEligible indicates that the run record is not a needs-review
	// outcome with a Hardcover book to attach an edition to.
	ErrEditionNotEligible = errors.New("book is not eligible for edition creation")

	// ErrEditionItemNotFound indicates that Audiobookshelf no longer has the item.
	ErrEditionItemNotFound = errors.New("audiobookshelf item not found")

	// ErrEditionNoIdentifier indicates that the Audiobookshelf item has neither
	// an ASIN nor a valid ISBN, so an edition created for it could never be
	// matched by a later sync.
	ErrEditionNoIdentifier = errors.New("audiobookshelf item has no asin or isbn")
)

// EditionUpstreamError reports a failed Audiobookshelf or Hardcover call. The
// wrapped error may contain remote details and must not be shown to users.
type EditionUpstreamError struct {
	Service string
	Err     error
}

func (e *EditionUpstreamError) Error() string {
	return fmt.Sprintf("%s request failed: %v", e.Service, e.Err)
}
func (e *EditionUpstreamError) Unwrap() error { return e.Err }

// editionTarget is a needs-review run record resolved for edition creation.
type editionTarget struct {
	profile         *database.ProfileWithTokens
	hardcoverBookID int
}

// PrepareEditionDraft builds a previewable edition for a needs-review book from
// a retained or live sync run. It only reads from Audiobookshelf and Hardcover.
func (s *MultiUserService) PrepareEditionDraft(ctx context.Context, profileID, runID, bookID string) (*draft.Draft, error) {
	// A draft is read-only and bound to ctx, so it needs no drain on shutdown or
	// profile deletion; it only refuses to start once either has begun.
	if err := s.checkEditionAdmission(profileID); err != nil {
		return nil, err
	}

	target, err := s.resolveEditionTarget(profileID, runID, bookID)
	if err != nil {
		return nil, err
	}
	item, err := s.fetchEditionItem(ctx, target.profile, bookID)
	if err != nil {
		return nil, err
	}
	if !hasEditionIdentifier(item) {
		return nil, ErrEditionNoIdentifier
	}

	hcClient := s.newHardcoverClient(target.profile.HardcoverToken)
	built, err := draft.New(ctx, *item, target.hardcoverBookID, hcClient, target.profile.SyncConfig.AudnexusRegion)
	if err != nil {
		var upstream *draft.UpstreamError
		if errors.As(err, &upstream) {
			return nil, &EditionUpstreamError{Service: "hardcover", Err: err}
		}
		return nil, fmt.Errorf("build edition draft: %w", err)
	}
	built.DryRun = target.profile.SyncConfig.DryRun
	return built, nil
}

// resolveEditionTarget loads the profile and finds the needs-review record for
// bookID in runID. The Hardcover book ID is taken only from that record.
func (s *MultiUserService) resolveEditionTarget(profileID, runID, bookID string) (*editionTarget, error) {
	if runID == "" || bookID == "" {
		return nil, ErrEditionNotFound
	}
	profile, err := s.GetProfile(profileID)
	if err != nil {
		return nil, fmt.Errorf("load sync profile: %w", err)
	}
	if profile == nil {
		return nil, ErrProfileNotFound
	}

	snapshot, err := s.GetSyncRunSnapshot(profileID, runID)
	if err != nil {
		return nil, fmt.Errorf("load sync run: %w", err)
	}
	if snapshot == nil || snapshot.RunID != runID || (snapshot.ProfileID != "" && snapshot.ProfileID != profileID) {
		return nil, ErrEditionNotFound
	}

	for _, record := range snapshot.BookOutcomes {
		if record.BookID != bookID {
			continue
		}
		if record.Outcome != sync.OutcomeNeedsReview {
			return nil, ErrEditionNotEligible
		}
		hardcoverBookID, err := strconv.Atoi(record.HardcoverBookID)
		if err != nil || hardcoverBookID <= 0 {
			return nil, ErrEditionNotEligible
		}
		return &editionTarget{profile: profile, hardcoverBookID: hardcoverBookID}, nil
	}
	return nil, ErrEditionNotFound
}

// hasEditionIdentifier reports whether the Audiobookshelf item carries an ASIN
// or a well-formed ISBN. Without one a sync can never match an edition created
// for the book, so no edition is drafted or created.
func hasEditionIdentifier(item *models.AudiobookshelfBook) bool {
	meta := item.Media.Metadata
	if strings.TrimSpace(meta.ASIN) != "" {
		return true
	}
	_, ok := isbn.Parse(meta.ISBN)
	return ok
}

// fetchEditionItem reads the current Audiobookshelf item for bookID.
func (s *MultiUserService) fetchEditionItem(ctx context.Context, profile *database.ProfileWithTokens, bookID string) (*models.AudiobookshelfBook, error) {
	absClient := audiobookshelf.NewClient(profile.AudiobookshelfURL, profile.AudiobookshelfToken)
	item, err := absClient.GetLibraryItem(ctx, bookID)
	if err != nil {
		if errors.Is(err, audiobookshelf.ErrItemNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrEditionItemNotFound, bookID)
		}
		return nil, &EditionUpstreamError{Service: "audiobookshelf", Err: err}
	}
	return item, nil
}
