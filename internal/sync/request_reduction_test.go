package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// An audiobook below the minimum progress must not spend Hardcover requests on
// a match it would then skip. The mock has no expectations, so any call fails.
func TestProcessBookSkipsAudiobookBelowMinimumProgressBeforeLookup(t *testing.T) {
	for _, tt := range []struct {
		name       string
		savedMatch bool
		wantBookID string
	}{
		{name: "no saved match"},
		{name: "saved match is still reported", savedMatch: true, wantBookID: "901"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, hc := createTestService()
			svc.config.Sync.SyncOwned = true
			svc.config.Sync.MinimumProgress = 0.5
			book := toAudiobookshelfBook(createTestBook("min-progress-"+tt.name, "Below Minimum", "Author", "B0MINPROG1", ""))
			book.Media.Duration = 1000
			book.Progress.CurrentTime = 250
			if tt.savedMatch {
				require.NoError(t, svc.state.SetAssociation(state.Association{
					ABSItemID: book.ID, SourceASIN: "B0MINPROG1", HardcoverBookID: "901", HardcoverEditionID: "902",
					ReadingFormat: models.ReadingFormatAudiobook, Provenance: "edition_asin",
				}))
			}

			require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))

			record := recordedOutcome(svc, book.ID)
			assert.Equal(t, OutcomeSkipped, record.Outcome)
			assert.Equal(t, "below minimum progress threshold", record.Reason)
			assert.Equal(t, tt.wantBookID, record.HardcoverBookID)
			assert.Empty(t, hc.Calls, "no Hardcover request may be made")
		})
	}
}

// An unread audiobook with no status to sync still runs the match when unread
// books are processed, because that match is how an unmatched book is reported.
func TestProcessBookStillMatchesAudiobookWithNoStatusToSync(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	svc.config.Sync.ProcessUnreadBooks = true
	svc.config.Sync.SyncWantToRead = false
	book := toAudiobookshelfBook(createTestBook("unread-no-status", "Unread", "Author", "B0UNREAD01", ""))
	book.Progress.CurrentTime = 0
	hc.On("SearchBookByASIN", mock.Anything, "B0UNREAD01").Return((*models.HardcoverBook)(nil), nil).Once()

	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))

	hc.AssertExpectations(t)
	record := recordedOutcome(svc, book.ID)
	assert.Equal(t, OutcomeNeedsReview, record.Outcome)
	assert.Equal(t, mismatch.ReasonAudibleImportAvailable, record.Reason)
	hc.AssertNotCalled(t, "SearchBooks", mock.Anything, mock.Anything, mock.Anything)
}

// Matching alone must not read or create user books: processBook does that once
// after the match and its skip guards.
func TestFindBookInHardcoverByASINDoesNotResolveUserBook(t *testing.T) {
	svc, hc := createTestService()
	book := toAudiobookshelfBook(createTestBook("asin-no-user-book", "Matched", "Author", "B0ASINONLY", ""))
	hc.On("SearchBookByASIN", mock.Anything, "B0ASINONLY").Return(&models.HardcoverBook{ID: "901", EditionID: "902"}, nil).Once()

	found, err, byASIN, _ := svc.findBookInHardcoverWithASINMatch(context.Background(), *book, associationWriteNone)

	require.NoError(t, err)
	require.NotNil(t, found)
	assert.True(t, byASIN)
	assert.Empty(t, found.UserBookID)
	hc.AssertNumberOfCalls(t, "SearchBookByASIN", 1)
	hc.AssertNotCalled(t, "GetEdition", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "GetUserBookID", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "CreateUserBook", mock.Anything, mock.Anything, mock.Anything)
}

// A title-only candidate needs no second request only when its search hit has
// authors, cover, slug, and a usable release date; incomplete hits fall back to
// the full book lookup.
func TestFindBookInHardcoverByTitleAuthorSkipsBookLookupWhenSearchHasRequiredMetadata(t *testing.T) {
	for _, tt := range []struct {
		name            string
		hit             models.HardcoverBook
		fullReleaseDate string
		wantLookups     int
		wantAuthor      string
		wantCover       string
		wantSlug        string
		wantReleaseDate string
	}{
		{
			name:            "search supplies authors cover slug and date",
			hit:             models.HardcoverBook{ID: "901", Title: "Match Title", Slug: "match-title", CoverImageURL: "search-cover", ReleaseDate: "2020-01-02", Authors: []models.Author{{Name: "Search Author"}}},
			wantAuthor:      "Search Author",
			wantCover:       "search-cover",
			wantSlug:        "match-title",
			wantReleaseDate: "2020-01-02",
		},
		{
			name:            "search has required metadata but no date",
			hit:             models.HardcoverBook{ID: "901", Title: "Match Title", Slug: "match-title", CoverImageURL: "search-cover", Authors: []models.Author{{Name: "Search Author"}}},
			fullReleaseDate: "2021-04-05",
			wantLookups:     1,
			wantAuthor:      "Full Author",
			wantCover:       "search-cover",
			wantSlug:        "match-title",
			wantReleaseDate: "2021-04-05",
		},
		{
			name:            "search has invalid date",
			hit:             models.HardcoverBook{ID: "901", Title: "Match Title", Slug: "match-title", CoverImageURL: "search-cover", ReleaseDate: "2021", Authors: []models.Author{{Name: "Search Author"}}},
			fullReleaseDate: "2022-06-07",
			wantLookups:     1,
			wantAuthor:      "Full Author",
			wantCover:       "search-cover",
			wantSlug:        "match-title",
			wantReleaseDate: "2022-06-07",
		},
		{
			name:            "search has no authors",
			hit:             models.HardcoverBook{ID: "901", Title: "Match Title", Slug: "match-title", CoverImageURL: "search-cover", ReleaseDate: "2020-01-02"},
			wantLookups:     1,
			wantAuthor:      "Full Author",
			wantCover:       "search-cover",
			wantSlug:        "match-title",
			wantReleaseDate: "2020-01-02",
		},
		{
			name:            "search has authors but no cover",
			hit:             models.HardcoverBook{ID: "901", Title: "Match Title", Slug: "match-title", ReleaseDate: "2020-01-02", Authors: []models.Author{{Name: "Search Author"}}},
			wantLookups:     1,
			wantAuthor:      "Full Author",
			wantCover:       "full-cover",
			wantSlug:        "match-title",
			wantReleaseDate: "2020-01-02",
		},
		{
			name:            "search has authors but no slug",
			hit:             models.HardcoverBook{ID: "901", Title: "Match Title", CoverImageURL: "search-cover", ReleaseDate: "2020-01-02", Authors: []models.Author{{Name: "Search Author"}}},
			wantLookups:     1,
			wantAuthor:      "Full Author",
			wantCover:       "search-cover",
			wantSlug:        "full-slug",
			wantReleaseDate: "2020-01-02",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, hc := createTestService()
			book := toAudiobookshelfBook(createTestBook("title-only-"+tt.name, "Match Title", "Author", "", ""))
			hc.On("SearchBooks", mock.Anything, "Match Title Author", "").Return([]models.HardcoverBook{tt.hit}, nil).Once()
			hc.On("GetBookByID", mock.Anything, "901").Return(&models.HardcoverBook{
				ID: "901", Slug: "full-slug", CoverImageURL: "full-cover", ReleaseDate: tt.fullReleaseDate, Authors: []models.Author{{Name: "Full Author"}},
			}, nil).Maybe()

			found, err := svc.findBookInHardcoverByTitleAuthor(context.Background(), *book)

			require.Error(t, err)
			require.NotNil(t, found)
			require.Len(t, found.Authors, 1)
			assert.Equal(t, tt.wantAuthor, found.Authors[0].Name)
			assert.Equal(t, tt.wantCover, found.CoverImageURL)
			assert.Equal(t, tt.wantSlug, found.Slug)
			assert.Equal(t, tt.wantReleaseDate, found.ReleaseDate)
			hc.AssertNumberOfCalls(t, "GetBookByID", tt.wantLookups)
		})
	}
}

// An audiobook with an unusable ASIN may fall back to ISBN and then title search.
// Keep that candidate's year when ISBN lookup fails but enrichment succeeds.
func TestProcessBookKeepsCandidateYearAfterAudiobookISBNLookupFailure(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	book := toAudiobookshelfBook(createTestBook("candidate-year", "Possible Match", "Author", "not-a-valid-ASIN", "978-0-306-40615-7"))
	book.Progress.CurrentTime = 300
	lookupErr := assert.AnError
	hc.On("SearchBookByISBN13", mock.Anything, "9780306406157").Return((*models.HardcoverBook)(nil), lookupErr).Once()
	hc.On("SearchBookByISBN10", mock.Anything, "0306406152").Return((*models.HardcoverBook)(nil), nil).Once()
	hc.On("SearchBooks", mock.Anything, "Possible Match Author", "").Return([]models.HardcoverBook{{
		ID: "901", Title: "Possible Match", Slug: "possible-match", CoverImageURL: "candidate-cover",
		Authors: []models.Author{{Name: "Candidate Author"}},
	}}, nil).Once()
	hc.On("GetBookByID", mock.Anything, "901").Return(&models.HardcoverBook{
		ID: "901", Title: "Possible Match", Slug: "possible-match", CoverImageURL: "candidate-cover",
		ReleaseDate: "2021-04-05", Authors: []models.Author{{Name: "Candidate Author"}},
	}, nil).Once()

	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))

	record := recordedOutcome(svc, book.ID)
	assert.Equal(t, OutcomeFailed, record.Outcome)
	assert.Contains(t, record.Error, lookupErr.Error())
	assert.Equal(t, "901", record.HardcoverBookID)
	assert.Equal(t, "2021", record.HardcoverPublishedYear)
	hc.AssertExpectations(t)
}

// userBookWithReadsClient returns a user book and its reads in one call, as the
// concrete Hardcover client does.
type userBookWithReadsClient struct {
	*MockHardcoverClient
	calls     int
	editionID string
	reads     []hardcover.UserBookRead
}

func (c *userBookWithReadsClient) GetUserBookWithReads(_ context.Context, userBookID string) (*models.HardcoverBook, []hardcover.UserBookRead, error) {
	c.calls++
	editionID := c.editionID
	if editionID == "" {
		editionID = "200"
	}
	return &models.HardcoverBook{ID: "100", EditionID: editionID, BookStatusID: 2, UserBookID: userBookID}, c.reads, nil
}

// The user book and its reads are read in one request, and the status handler
// reuses those reads instead of asking Hardcover for them again.
func TestProcessBookReadsUserBookAndReadsInOneRequest(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	editionID := int64(200)
	seconds := 100
	client := &userBookWithReadsClient{
		MockHardcoverClient: hc,
		reads:               []hardcover.UserBookRead{{ID: 400, EditionID: &editionID, ProgressSeconds: &seconds}},
	}
	svc.hardcover = client
	svc.findExistingUserBookForBookFunc = func(context.Context, int64) (int64, error) { return 300, nil }
	book := toAudiobookshelfBook(createTestBook("one-request", "Progress", "Author", "B0ONEREQ01", ""))
	book.Media.Duration = 1000
	book.Progress.CurrentTime = 300
	hc.On("SearchBookByASIN", mock.Anything, "B0ONEREQ01").Return(&models.HardcoverBook{ID: "100", EditionID: "200"}, nil).Once()
	hc.On("GetEdition", mock.Anything, "200").Return(&models.Edition{ID: "200", BookID: "100"}, nil).Maybe()
	hc.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == 400 && input.Object["progress_seconds"] == int64(300)
	})).Return(true, nil).Once()

	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))

	assert.Equal(t, 1, client.calls)
	hc.AssertNotCalled(t, "GetUserBook", mock.Anything, mock.Anything)
	hc.AssertNotCalled(t, "GetUserBookReads", mock.Anything, mock.Anything)
	assert.Equal(t, OutcomeSynced, recordedOutcome(svc, book.ID).Outcome)
	hc.AssertExpectations(t)
}

// Reads from before an edition correction cannot identify the read to update;
// progress must use a fresh read for the corrected edition.
func TestProcessBookRefreshesReadsAfterCorrectingUserBookEdition(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = false
	staleEditionID := int64(201)
	staleProgress := 100
	client := &userBookWithReadsClient{
		MockHardcoverClient: hc,
		editionID:           "201",
		reads:               []hardcover.UserBookRead{{ID: 400, EditionID: &staleEditionID, ProgressSeconds: &staleProgress}},
	}
	svc.hardcover = client
	svc.findExistingUserBookForBookFunc = func(context.Context, int64) (int64, error) { return 300, nil }
	book := toAudiobookshelfBook(createTestBook("corrected-edition", "Progress", "Author", "B0CORRECT1", ""))
	book.Media.Duration = 1000
	book.Progress.CurrentTime = 300
	hc.On("SearchBookByASIN", mock.Anything, "B0CORRECT1").Return(&models.HardcoverBook{ID: "100", EditionID: "200"}, nil).Once()
	hc.On("GetEdition", mock.Anything, "200").Return(&models.Edition{ID: "200", BookID: "100"}, nil).Maybe()
	hc.On("UpdateUserBookEdition", mock.Anything, 300, 200).Return(nil).Once()
	freshEditionID := int64(200)
	freshProgress := 100
	hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 300}).
		Return([]hardcover.UserBookRead{{ID: 401, EditionID: &freshEditionID, ProgressSeconds: &freshProgress}}, nil).Once()
	hc.On("UpdateUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookReadInput) bool {
		return input.ID == 401 && input.Object["progress_seconds"] == int64(300)
	})).Return(true, nil).Once()

	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))

	assert.Equal(t, 1, client.calls, "the existing user book should be fetched once with its reads")
	hc.AssertNotCalled(t, "GetUserBook", mock.Anything, mock.Anything)
	hc.AssertNumberOfCalls(t, "GetUserBookReads", 1)
	assert.Equal(t, OutcomeSynced, recordedOutcome(svc, book.ID).Outcome)
	hc.AssertExpectations(t)
}

// Reads fetched with a user book are used once; a second read asks Hardcover.
func TestUserBookReadsSnapshotIsUsedOnce(t *testing.T) {
	svc, hc := createTestService()
	ctx := withOperationUserBookSnapshots(context.Background())
	setOperationUserBookReadsSnapshot(ctx, 300, []hardcover.UserBookRead{{ID: 1}})
	hc.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 300}).
		Return([]hardcover.UserBookRead{{ID: 2}}, nil).Once()

	first, err := svc.getUserBookReads(ctx, 300)
	require.NoError(t, err)
	require.Len(t, first, 1)
	assert.EqualValues(t, 1, first[0].ID)

	second, err := svc.getUserBookReads(ctx, 300)
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.EqualValues(t, 2, second[0].ID)
	hc.AssertExpectations(t)
}

func ownershipTestBook(id string) *models.AudiobookshelfBook {
	return toAudiobookshelfBook(createTestBook(id, "Owned Book", "Author", "B0OWNED001", ""))
}

func saveOwnershipAssociation(t *testing.T, svc *Service, book *models.AudiobookshelfBook) {
	t.Helper()
	require.NoError(t, svc.state.SetAssociation(state.Association{
		ABSItemID: book.ID, SourceASIN: "B0OWNED001", HardcoverBookID: "123", HardcoverEditionID: "456",
		ReadingFormat: models.ReadingFormatAudiobook, Provenance: "edition_asin",
	}))
}

// A confirmed ownership result is reused for the saved match, so later syncs
// skip the request until the recheck interval passes.
func TestReconcileBookOwnershipReusesRecentConfirmation(t *testing.T) {
	for _, tt := range []struct {
		name      string
		owned     bool
		wantMarks int
	}{
		{name: "already owned", owned: true},
		{name: "marked owned", wantMarks: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, hc := createTestService()
			svc.config.Sync.SyncOwned = true
			svc.config.Hardcover.Token = "ownership-token"
			book := ownershipTestBook("ownership-" + tt.name)
			saveOwnershipAssociation(t, svc, book)
			hcBook := &models.HardcoverBook{ID: "123", EditionID: "456"}
			hc.On("CheckBookOwnership", mock.Anything, 123).Return(tt.owned, nil).Once()
			if !tt.owned {
				hc.On("MarkEditionAsOwned", mock.Anything, 456).Return(nil).Once()
			}

			svc.reconcileBookOwnership(context.Background(), hcBook, *book)
			svc.reconcileBookOwnership(context.Background(), hcBook, *book)

			hc.AssertNumberOfCalls(t, "CheckBookOwnership", 1)
			hc.AssertNumberOfCalls(t, "MarkEditionAsOwned", tt.wantMarks)
			assert.True(t, svc.state.OwnershipVerifiedSince(book.ID, "123", "456", ownershipTokenFingerprint(svc.config.Hardcover.Token), time.Now().Add(-time.Minute)))
		})
	}
}

func TestReconcileBookOwnershipUsesConfiguredRecheckDays(t *testing.T) {
	for _, tt := range []struct {
		name       string
		days       int
		age        time.Duration
		wantChecks int
	}{
		{name: "default interval still fresh", days: 30, age: 25 * 24 * time.Hour},
		{name: "longer interval still fresh", days: 60, age: 40 * 24 * time.Hour},
		{name: "shorter interval expired", days: 1, age: 2 * 24 * time.Hour, wantChecks: 1},
		{name: "shorter interval still fresh", days: 1, age: 12 * time.Hour},
		{name: "zero checks every time", days: 0, age: time.Hour, wantChecks: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, hc := createTestService()
			svc.config.Sync.SyncOwned = true
			svc.config.Sync.OwnershipRecheckDays = tt.days
			svc.config.Hardcover.Token = "ownership-token"
			book := ownershipTestBook("ownership-days-" + tt.name)
			saveOwnershipAssociation(t, svc, book)
			svc.state.RecordOwnershipVerified(book.ID, "123", "456", ownershipTokenFingerprint(svc.config.Hardcover.Token), time.Now().Add(-tt.age))
			if tt.wantChecks > 0 {
				hc.On("CheckBookOwnership", mock.Anything, 123).Return(true, nil).Times(tt.wantChecks)
			}

			for range 2 {
				svc.reconcileBookOwnership(context.Background(), &models.HardcoverBook{ID: "123", EditionID: "456"}, *book)
			}

			hc.AssertNumberOfCalls(t, "CheckBookOwnership", tt.wantChecks)
			hc.AssertExpectations(t)
		})
	}
}

func TestReconcileBookOwnershipChecksAgainAfterIntervalOrMatchChange(t *testing.T) {
	for _, tt := range []struct {
		name     string
		verified time.Time
		hcBook   *models.HardcoverBook
	}{
		{name: "interval elapsed", verified: time.Now().Add(-30*24*time.Hour - time.Hour), hcBook: &models.HardcoverBook{ID: "123", EditionID: "456"}},
		{name: "matched edition changed", verified: time.Now(), hcBook: &models.HardcoverBook{ID: "123", EditionID: "789"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, hc := createTestService()
			svc.config.Sync.SyncOwned = true
			svc.config.Hardcover.Token = "ownership-token"
			book := ownershipTestBook("ownership-recheck-" + tt.name)
			saveOwnershipAssociation(t, svc, book)
			svc.state.RecordOwnershipVerified(book.ID, "123", "456", ownershipTokenFingerprint(svc.config.Hardcover.Token), tt.verified)
			hc.On("CheckBookOwnership", mock.Anything, 123).Return(true, nil).Once()

			svc.reconcileBookOwnership(context.Background(), tt.hcBook, *book)

			hc.AssertNumberOfCalls(t, "CheckBookOwnership", 1)
		})
	}
}

// Dry runs read ownership but never persist it, and a failed check is retried.
func TestReconcileBookOwnershipDoesNotRememberDryRunOrFailure(t *testing.T) {
	for _, tt := range []struct {
		name     string
		dryRun   bool
		checkErr error
	}{
		{name: "dry run", dryRun: true},
		{name: "check failed", checkErr: assert.AnError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, hc := createTestService()
			svc.config.Sync.SyncOwned = true
			svc.config.Hardcover.Token = "ownership-token"
			svc.config.Sync.DryRun = tt.dryRun
			book := ownershipTestBook("ownership-not-saved-" + tt.name)
			saveOwnershipAssociation(t, svc, book)
			hc.On("CheckBookOwnership", mock.Anything, 123).Return(tt.checkErr == nil, tt.checkErr).Twice()
			hcBook := &models.HardcoverBook{ID: "123", EditionID: "456"}

			svc.reconcileBookOwnership(context.Background(), hcBook, *book)
			svc.reconcileBookOwnership(context.Background(), hcBook, *book)

			hc.AssertNumberOfCalls(t, "CheckBookOwnership", 2)
			assert.False(t, svc.state.OwnershipVerifiedSince(book.ID, "123", "456", ownershipTokenFingerprint(svc.config.Hardcover.Token), time.Now().Add(-time.Hour)))
		})
	}
}

func TestReconcileBookOwnershipScopesConfirmationToHardcoverToken(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = true
	const previousToken = "previous-account-token"
	const replacementToken = "replacement-account-token"
	svc.config.Hardcover.Token = previousToken
	book := ownershipTestBook("ownership-token-replacement")
	saveOwnershipAssociation(t, svc, book)
	hcBook := &models.HardcoverBook{ID: "123", EditionID: "456"}
	hc.On("CheckBookOwnership", mock.Anything, 123).Return(true, nil).Once()

	svc.reconcileBookOwnership(context.Background(), hcBook, *book)
	statePath := filepath.Join(t.TempDir(), "sync_state.json")
	require.NoError(t, svc.state.Save(statePath))
	reloadedState, err := state.LoadState(statePath)
	require.NoError(t, err)
	svc.state = reloadedState

	// The reloaded confirmation is reusable while the profile keeps its token.
	svc.reconcileBookOwnership(context.Background(), hcBook, *book)
	hc.AssertNumberOfCalls(t, "CheckBookOwnership", 1)

	// A replacement token must check the new account and mark the match if it
	// is absent from that account's Owned list.
	svc.config.Hardcover.Token = replacementToken
	hc.On("CheckBookOwnership", mock.Anything, 123).Return(false, nil).Once()
	hc.On("MarkEditionAsOwned", mock.Anything, 456).Return(nil).Once()
	svc.reconcileBookOwnership(context.Background(), hcBook, *book)

	hc.AssertExpectations(t)
	hc.AssertNumberOfCalls(t, "CheckBookOwnership", 2)
	hc.AssertNumberOfCalls(t, "MarkEditionAsOwned", 1)
	assert.False(t, svc.state.OwnershipVerifiedSince(book.ID, "123", "456", ownershipTokenFingerprint(previousToken), time.Time{}))
	assert.True(t, svc.state.OwnershipVerifiedSince(book.ID, "123", "456", ownershipTokenFingerprint(replacementToken), time.Now().Add(-time.Minute)))
	require.NoError(t, svc.state.Save(statePath))
	persisted, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.NotContains(t, string(persisted), previousToken)
	assert.NotContains(t, string(persisted), replacementToken)
}

func TestReconcileBookOwnershipRechecksLegacyConfirmation(t *testing.T) {
	svc, hc := createTestService()
	svc.config.Sync.SyncOwned = true
	svc.config.Hardcover.Token = "current-account-token"
	book := ownershipTestBook("ownership-legacy-confirmation")
	saveOwnershipAssociation(t, svc, book)
	// A timestamp with no token fingerprint represents state written before the
	// account scope was added.
	svc.state.RecordOwnershipVerified(book.ID, "123", "456", "", time.Now())
	statePath := filepath.Join(t.TempDir(), "sync_state.json")
	require.NoError(t, svc.state.Save(statePath))
	reloadedState, err := state.LoadState(statePath)
	require.NoError(t, err)
	svc.state = reloadedState
	hc.On("CheckBookOwnership", mock.Anything, 123).Return(true, nil).Once()

	svc.reconcileBookOwnership(context.Background(), &models.HardcoverBook{ID: "123", EditionID: "456"}, *book)

	hc.AssertExpectations(t)
	hc.AssertNumberOfCalls(t, "CheckBookOwnership", 1)
}
