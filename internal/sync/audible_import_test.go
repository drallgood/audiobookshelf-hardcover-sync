package sync

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	statepkg "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestProcessBookClassifiesOnlyUnmatchedUsableASINAsAudibleImportAvailable(t *testing.T) {
	tests := []struct {
		name        string
		lookupErr   error
		wantOutcome SyncOutcome
		wantReason  string
	}{
		{
			name:        "unmatched usable ASIN",
			wantOutcome: OutcomeNeedsReview,
			wantReason:  mismatch.ReasonAudibleImportAvailable,
		},
		{
			name:        "ASIN lookup failure stays retryable",
			lookupErr:   errors.New("Hardcover is temporarily unavailable"),
			wantOutcome: OutcomeFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, hardcoverMock := createTestService()
			svc.config.Sync.Incremental = false
			svc.config.Sync.ProcessUnreadBooks = true
			svc.config.Sync.SyncOwned = false
			book := toAudiobookshelfBook(createTestBook(
				"audible-import-"+tt.name, "ABS Source Title", "Source Author", " b0source12 ", "978-0-306-40615-7",
			))
			lookup := &associationLookupClient{
				MockHardcoverClient: hardcoverMock,
				searchErr:           tt.lookupErr,
			}
			svc.hardcover = lookup

			err := svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{})

			require.NoError(t, err)
			record := recordedOutcome(svc, book.ID)
			require.Equal(t, tt.wantOutcome, record.Outcome)
			if tt.wantReason == mismatch.ReasonAudibleImportAvailable {
				require.Empty(t, record.MatchMethod, "an unmatched Audible import has no confirmed match method")
			}
			if tt.wantReason != "" {
				require.Equal(t, tt.wantReason, record.Reason)
				require.Empty(t, record.HardcoverBookID)
				require.Empty(t, record.EditionID)
				require.Equal(t, "B0SOURCE12", record.SourceASIN)
				require.Empty(t, record.SourceISBN10)
				require.Equal(t, "9780306406157", record.SourceISBN13)
			} else {
				require.NotContains(t, record.Reason, mismatch.ReasonAudibleImportAvailable)
			}
			require.Equal(t, 1, lookup.searchCount, "the identifier lookup is attempted once")
			assertNoHardcoverBookSearches(t, hardcoverMock)
			_, associated := svc.state.GetAssociation(book.ID)
			require.False(t, associated)

			if tt.wantReason != "" {
				mismatches := svc.mismatchCollector.GetAll()
				require.Len(t, mismatches, 1)
				require.Equal(t, mismatch.ReasonAudibleImportAvailable, mismatches[0].Reason)
				export := mismatches[0].ToEditionExport(context.Background(), nil)
				encoded, marshalErr := json.Marshal(export)
				require.NoError(t, marshalErr)
				var exported map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(encoded, &exported))
				require.NotContains(t, exported, "book_id", "an unanchored import export must not imply a Hardcover book ID")
				var info map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(exported["info"], &info))
				require.JSONEq(t, `"audible_import_available"`, string(info["reason"]))
			}
		})
	}
}

func TestProcessBookAudibleImportAvailabilityClearsEarlierTitleCandidate(t *testing.T) {
	svc, hardcoverMock := createTestService()
	svc.config.Sync.ProcessUnreadBooks = true
	svc.config.Sync.SyncOwned = false
	book := toAudiobookshelfBook(createTestBook("audible-import-replaces-candidate", "Source Title", "Source Author", "", ""))

	// A first pass found only a title/author candidate, which is useful review
	// context but does not establish an identifier match.
	hardcoverMock.On("SearchBooks", mock.Anything, "Source Title Source Author", "").Return([]models.HardcoverBook{{
		ID: "hc-candidate", Title: "Source Title", Slug: "possible-title", Authors: []models.Author{{Name: "Candidate Author"}},
	}}, nil).Once()
	hardcoverMock.On("GetBookByID", mock.Anything, "hc-candidate").Return(&models.HardcoverBook{
		ID: "hc-candidate", Title: "Source Title", Slug: "possible-title", Authors: []models.Author{{Name: "Candidate Author"}},
	}, nil).Once()
	// Mismatch enrichment uses its separate title/author API shape after
	// recording the candidate; an empty result leaves the earlier candidate intact.
	hardcoverMock.On("SearchBooks", mock.Anything, "Source Title", "Source Author").Return([]models.HardcoverBook{}, nil).Once()
	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))
	first := recordedOutcome(svc, book.ID)
	require.Equal(t, "hc-candidate", first.HardcoverBookID)

	// On a later pass Hardcover has no exact ASIN mapping. That classification
	// must replace, rather than inherit, the earlier unverified title candidate.
	book.Media.Metadata.ASIN = "B0SOURCE12"
	lookup := &associationLookupClient{MockHardcoverClient: hardcoverMock}
	svc.hardcover = lookup
	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))

	current := recordedOutcome(svc, book.ID)
	require.Equal(t, OutcomeNeedsReview, current.Outcome)
	require.Equal(t, mismatch.ReasonAudibleImportAvailable, current.Reason)
	require.Empty(t, current.MatchMethod, "a previous title candidate must not become the match method for an Audible import")
	require.Empty(t, current.HardcoverBookID)
	require.Empty(t, current.EditionID)
	require.Empty(t, current.HardcoverTitle)
	require.Equal(t, 1, lookup.searchCount)
	mismatches := svc.mismatchCollector.GetAll()
	require.Len(t, mismatches, 2)
	latest := mismatches[len(mismatches)-1]
	require.Equal(t, mismatch.ReasonAudibleImportAvailable, latest.Reason)
	require.Empty(t, latest.HardcoverBookID)
	require.Empty(t, latest.HardcoverTitle)
	exported := latest.ToEditionExport(context.Background(), nil)
	encoded, err := json.Marshal(exported)
	require.NoError(t, err)
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &payload))
	require.NotContains(t, payload, "book_id")
	var info map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload["info"], &info))
	require.JSONEq(t, `"audible_import_available"`, string(info["reason"]))
	attention, exists := svc.attentionCandidates[book.ID]
	require.True(t, exists)
	require.Empty(t, attention.HardcoverBookID)
	require.Empty(t, attention.HardcoverTitle)
}

func TestAudibleImportAttentionRetainsSourceDetailsAfterOutcomeRefresh(t *testing.T) {
	svc, hardcoverMock := createTestService()
	svc.config.Sync.ProcessUnreadBooks = true
	svc.config.Sync.SyncOwned = false
	book := *toAudiobookshelfBook(createTestBook("audible-import-details", "Detailed Source", "Source Author", "B0SOURCE12", "0-306-40615-2"))
	book.Media.Duration = 1234.75
	book.Media.Metadata.Subtitle = "Source Subtitle"
	book.Media.Metadata.NarratorName = "Source Narrator"
	book.Media.Metadata.Abridged = true

	svc.hardcover = &associationLookupClient{MockHardcoverClient: hardcoverMock}
	require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))

	attention, exists := svc.attentionCandidates[book.ID]
	require.True(t, exists)
	require.Equal(t, "0306406152", attention.ISBN10)
	require.Empty(t, attention.ISBN13, "the mismatch preserves the source ISBN form")
	require.Equal(t, "Audible Audio", attention.EditionFormat)
	require.True(t, attention.Abridged)
	require.Equal(t, 1235, attention.DurationSeconds, "duration should be rounded consistently with the original import record")
	require.Equal(t, "Source Subtitle", attention.Subtitle)
	require.Equal(t, "Source Narrator", attention.Narrator)
	require.Empty(t, attention.HardcoverBookID)
	require.Empty(t, attention.HardcoverTitle)
	hardcoverMock.AssertNotCalled(t, "SearchBooks", mock.Anything, mock.Anything, mock.Anything)
}

func TestSecondASINMissAfterBookErrorPublishesAudibleImportAvailable(t *testing.T) {
	svc, hardcoverMock := createTestService()
	svc.config.Sync.ProcessUnreadBooks = true
	svc.config.Sync.SyncOwned = false
	book := *toAudiobookshelfBook(createTestBook("audible-import-second-miss", "Source Title", "Source Author", "B0SOURCE12", "978-0-306-40615-7"))
	book.Media.Duration = 1234.75
	book.Media.Metadata.Abridged = true
	lookup := &sequencedASINLookupClient{
		MockHardcoverClient: hardcoverMock,
		results:             []*hardcover.ASINLookupResult{nil, nil},
		errors: []error{
			hardcover.WithBookID(errors.New("incomplete ASIN lookup"), "hc-incomplete-book"),
			nil,
		},
	}
	svc.hardcover = lookup

	require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))

	record := recordedOutcome(svc, book.ID)
	require.Equal(t, OutcomeNeedsReview, record.Outcome)
	require.Equal(t, mismatch.ReasonAudibleImportAvailable, record.Reason)
	require.Empty(t, record.MatchMethod)
	require.Empty(t, record.HardcoverBookID)
	require.Empty(t, record.EditionID)
	require.Empty(t, record.HardcoverTitle)
	require.Equal(t, 2, lookup.calls)
	attention, exists := svc.attentionCandidates[book.ID]
	require.True(t, exists)
	require.Equal(t, mismatch.ReasonAudibleImportAvailable, attention.Reason)
	require.Empty(t, attention.HardcoverBookID)
	require.Equal(t, "9780306406157", attention.ISBN13)
	require.Equal(t, "Audible Audio", attention.EditionFormat)
	require.True(t, attention.Abridged)
	require.Equal(t, 1235, attention.DurationSeconds)
	require.Empty(t, attention.ReleaseDate, "this source-only review path must not run Audnex enrichment")
	_, associated := svc.state.GetAssociation(book.ID)
	require.False(t, associated)
	_, checkpointed := svc.state.GetBookState(book.ID)
	require.False(t, checkpointed)
	require.Len(t, svc.mismatchCollector.GetAll(), 1)
	hardcoverMock.AssertNotCalled(t, "SearchBooks", mock.Anything, mock.Anything, mock.Anything)
	hardcoverMock.AssertNotCalled(t, "GetBookByID", mock.Anything, mock.Anything)
	hardcoverMock.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
	hardcoverMock.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
	hardcoverMock.AssertNotCalled(t, "GetUserBookID", mock.Anything, mock.Anything)
	hardcoverMock.AssertNotCalled(t, "CreateUserBook", mock.Anything, mock.Anything, mock.Anything)
}

func TestAudibleImportAvailabilityDryRunDoesNotSuppressRealIncrementalRun(t *testing.T) {
	svc, hardcoverMock := createTestService()
	svc.config.Sync.DryRun = true
	svc.config.Sync.Incremental = true
	svc.config.Sync.ProcessUnreadBooks = true
	svc.config.Sync.SyncOwned = false
	book := *toAudiobookshelfBook(createTestBook("audible-import-dry-run", "Dry Run Source", "Source Author", "B0SOURCE12", ""))
	hardcoverMock.On("SearchBookByASIN", mock.Anything, "B0SOURCE12").Return((*models.HardcoverBook)(nil), nil).Twice()

	require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
	dryRunRecord := recordedOutcome(svc, book.ID)
	require.Equal(t, OutcomeNeedsReview, dryRunRecord.Outcome)
	require.Equal(t, mismatch.ReasonAudibleImportAvailable, dryRunRecord.Reason)
	_, associated := svc.state.GetAssociation(book.ID)
	require.False(t, associated)
	_, checkpointed := svc.state.GetBookState(book.ID)
	require.False(t, checkpointed)
	require.True(t, svc.state.NeedsSync(book.ID, 0, "WANT_TO_READ", 0), "a later real incremental run must still attempt this unresolved item")
	require.False(t, svc.state.IsDirty(), "dry-run review classification must not dirty persistent sync state")
	for _, call := range []struct {
		method string
		args   []interface{}
	}{
		{method: "MarkEditionAsOwned", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "UpdateUserBookStatus", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "CreateUserBook", args: []interface{}{mock.Anything, mock.Anything, mock.Anything}},
		{method: "UpdateReadingProgress", args: []interface{}{mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything}},
		{method: "InsertUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "UpdateUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "DeleteUserBookRead", args: []interface{}{mock.Anything, mock.Anything}},
		{method: "UpdateUserBookEdition", args: []interface{}{mock.Anything, mock.Anything, mock.Anything}},
	} {
		hardcoverMock.AssertNotCalled(t, call.method, call.args...)
	}

	svc.config.Sync.DryRun = false
	require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
	hardcoverMock.AssertNumberOfCalls(t, "SearchBookByASIN", 2)
	require.Equal(t, OutcomeNeedsReview, recordedOutcome(svc, book.ID).Outcome)
	_, checkpointed = svc.state.GetBookState(book.ID)
	require.False(t, checkpointed)
	_, associated = svc.state.GetAssociation(book.ID)
	require.False(t, associated)
	hardcoverMock.AssertExpectations(t)
}

func TestUnanchoredAudibleAssociationPersistsAndIsReusedByNextSync(t *testing.T) {
	for _, status := range []hardcover.RegionalAudiobookStatus{hardcover.RegionalAudiobookLoaded, hardcover.RegionalAudiobookCreated} {
		t.Run(string(status), func(t *testing.T) {
			svc, hardcoverMock := createTestService()
			book := toAudiobookshelfBook(createTestBook("audible-association-"+string(status), "Source Title", "Author", " b0source12 ", "978-0-306-40615-7"))
			confirmedAt := time.Date(2026, time.June, 5, 14, 30, 0, 0, time.FixedZone("test", -4*60*60))
			association, err := statepkg.NewAudibleImportAssociation(book, &hardcover.RegionalAudiobookResult{
				Status: status, BookID: 73, EditionID: 900,
				ReadingFormatID:    models.ReadingFormatID(models.ReadingFormatAudiobook),
				RegionalExternalID: "B0OTHER123:uk",
			}, "B0OTHER123:uk", confirmedAt)
			require.NoError(t, err)
			require.Equal(t, "audible_import_unanchored", association.Provenance)
			require.Equal(t, "uk", association.AudnexusConfirmedRegion)
			require.Equal(t, confirmedAt.UTC(), association.AudnexusConfirmedAt)
			require.Equal(t, "B0OTHER123:uk", association.RegionalExternalID)
			require.Equal(t, "B0OTHER123:uk", association.Correction)
			require.Equal(t, "73", association.HardcoverBookID)
			require.Equal(t, "900", association.HardcoverEditionID)
			require.Equal(t, "b0source12", association.SourceASIN, "the ABS source snapshot stays distinct from the corrected Audible identifier")

			require.NoError(t, svc.state.SetAssociation(association))
			statePath := filepath.Join(t.TempDir(), "sync-state.json")
			require.NoError(t, svc.state.Save(statePath))
			reloaded, err := statepkg.LoadState(statePath)
			require.NoError(t, err)
			stored, exists := reloaded.GetAssociation(book.ID)
			require.True(t, exists)
			require.Equal(t, association, stored)

			nextService, nextHardcoverMock := createTestService()
			nextService.state = reloaded
			resolved, err := nextService.findBookInHardcover(
				hardcover.WithReadingFormat(context.Background(), models.ReadingFormatAudiobook), *book,
			)
			require.NoError(t, err)
			require.Equal(t, "73", resolved.ID)
			require.Equal(t, "900", resolved.EditionID)
			nextHardcoverMock.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
			nextHardcoverMock.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
			nextHardcoverMock.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
			nextHardcoverMock.AssertNotCalled(t, "SearchBooks", mock.Anything, mock.Anything, mock.Anything)
			hardcoverMock.AssertExpectations(t)
		})
	}
}
