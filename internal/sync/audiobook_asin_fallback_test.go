package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type audiobookFallbackClient struct {
	*associationLookupClient
	fallback      *hardcover.ASINLookupResult
	err           error
	fallbackCalls int
}

func (c *audiobookFallbackClient) SearchBookByEditionASINResult(context.Context, string) (*hardcover.ASINLookupResult, error) {
	c.fallbackCalls++
	return c.fallback, c.err
}

func TestAudiobookISBNVerificationFailuresWithValidASIN(t *testing.T) {
	for _, tc := range []struct {
		name       string
		secondErr  error
		wantReason string
	}{
		{name: "disappeared", wantReason: "ISBN match disappeared"},
		{name: "lookup failure", secondErr: errors.New("ISBN verification unavailable"), wantReason: "ISBN verification unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, hc := createTestService()
			svc.mismatchCollector = newNotFoundAudnexCollector(t)
			svc.config.Sync.ProcessUnreadBooks = true
			svc.config.Sync.SyncWantToRead = true
			svc.config.Sync.SyncOwned = true
			book := associationTestBook("isbn-disappeared-valid-asin", "B0AUDIO001", testISBN13NoTen)
			svc.hardcover = &audiobookFallbackClient{associationLookupClient: &associationLookupClient{MockHardcoverClient: hc}}
			hc.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
				Return(&models.HardcoverBook{ID: "901", EditionID: "902"}, nil).Once()
			hc.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
				Return((*models.HardcoverBook)(nil), tc.secondErr).Once()
			// Failed verification may enrich the attention record using read-only lookups.
			hc.On("GetEdition", mock.Anything, "902").Return((*models.Edition)(nil), nil).Maybe()
			hc.On("GetBookByID", mock.Anything, "901").Return((*models.HardcoverBook)(nil), nil).Maybe()
			hc.On("SearchBookByASIN", mock.Anything, "B0AUDIO001").Return((*models.HardcoverBook)(nil), nil).Maybe()
			hc.On("SearchBooks", mock.Anything, book.Media.Metadata.Title, book.Media.Metadata.AuthorName).
				Return([]models.HardcoverBook(nil), nil).Maybe()

			err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

			require.ErrorIs(t, err, ErrSkippedBook)
			record := recordedOutcome(svc, book.ID)
			assert.Equal(t, OutcomeFailed, record.Outcome)
			assert.Equal(t, reasonHardcoverLookupFailed, record.Reason)
			assert.Contains(t, record.Error, tc.wantReason)
			assert.NotEqual(t, mismatch.ReasonAudibleImportAvailable, record.Reason)
			assert.Equal(t, "901", record.HardcoverBookID)
			assert.Equal(t, "902", record.EditionID)
			assert.False(t, IsAudiobookIdentifierFallbackRecord(record))
			_, associated := svc.state.GetAssociation(book.ID)
			assert.False(t, associated)
			checkpoint, exists := svc.state.GetBookState(book.ID + ":902")
			require.True(t, exists)
			assert.Equal(t, "SKIPPED", checkpoint.Status)
			assert.True(t, svc.state.NeedsSync(book.ID+":902", 0, "WANT_TO_READ", 0))
			for _, attention := range svc.mismatchCollector.GetAll() {
				assert.Equal(t, reasonHardcoverLookupFailed, attention.Reason)
				assert.NotEqual(t, mismatch.ReasonAudibleImportAvailable, attention.Reason)
			}
			hc.AssertNotCalled(t, "CheckBookOwnership", mock.Anything, mock.Anything)
			hc.AssertNotCalled(t, "MarkEditionAsOwned", mock.Anything, mock.Anything)
			hc.AssertNotCalled(t, "GetUserBookID", mock.Anything, mock.Anything)
			hc.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
			hc.AssertExpectations(t)
		})
	}
}

func TestAudiobookISBNVerificationRepeatsOnlyTheMatchedIdentifier(t *testing.T) {
	for _, tc := range []struct {
		name        string
		sourceISBN  string
		match13     bool
		counterpart bool
	}{
		{name: "given ISBN-13", sourceISBN: "9780306406157", match13: true},
		{name: "counterpart ISBN-10", sourceISBN: "9780306406157", counterpart: true},
		{name: "given ISBN-10", sourceISBN: "0306406152"},
		{name: "counterpart ISBN-13", sourceISBN: "0306406152", match13: true, counterpart: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, hc := createTestService()
			svc.config.Sync.Incremental = true
			svc.config.Sync.ProcessUnreadBooks = true
			svc.config.Sync.SyncWantToRead = true
			svc.config.Sync.SyncOwned = false
			book := associationTestBook("isbn-verification-source", "B0AUDIO001", tc.sourceISBN)
			lookup := &audiobookFallbackClient{associationLookupClient: &associationLookupClient{MockHardcoverClient: hc}}
			svc.hardcover = lookup
			hit := &models.HardcoverBook{ID: "901", EditionID: "902"}
			if tc.match13 {
				hc.On("SearchBookByISBN13", mock.Anything, "9780306406157").Return(hit, nil).Times(3)
				if tc.counterpart {
					hc.On("SearchBookByISBN10", mock.Anything, "0306406152").Return((*models.HardcoverBook)(nil), nil).Twice()
				}
			} else {
				hc.On("SearchBookByISBN10", mock.Anything, "0306406152").Return(hit, nil).Times(3)
				if tc.counterpart {
					hc.On("SearchBookByISBN13", mock.Anything, "9780306406157").Return((*models.HardcoverBook)(nil), nil).Twice()
				}
			}
			hc.On("GetEdition", mock.Anything, "902").Return(&models.Edition{ID: "902", BookID: "901", ReadingFormatID: "2"}, nil).Once()
			hc.On("GetUserBookID", mock.Anything, 902).Return(903, nil).Once()
			hc.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{ID: 903, StatusID: 1}).Return(nil).Once()

			require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
			assert.Equal(t, OutcomeSynced, recordedOutcome(svc, book.ID).Outcome)
			assert.Equal(t, 1, lookup.searchCount, "confirmation must not repeat the mapping lookup")
			require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
			assert.Equal(t, OutcomeAlreadyCurrent, recordedOutcome(svc, book.ID).Outcome)
			assert.Equal(t, 2, lookup.searchCount, "a later sync must still check for a new mapping")
			assert.Zero(t, lookup.fallbackCalls, "ISBN confirmation must not search edition ASIN")
			_, saved := svc.state.GetAssociation(book.ID)
			assert.False(t, saved)
			hc.AssertExpectations(t)
		})
	}
}

func TestAudiobookFallbackSourceIdentifierAndFormat(t *testing.T) {
	// Route tests cover eligible match methods and outcomes. Keep the legacy
	// identifier preference and format boundaries focused here.
	assert.True(t, IsAudiobookIdentifierFallbackRecord(BookOutcomeRecord{
		Format: "Audiobook", MatchMethod: "isbn", Outcome: OutcomeSynced, ASIN: "B00SOURCE1",
	}))
	for _, record := range []BookOutcomeRecord{
		{Format: "Ebook", MatchMethod: "isbn", Outcome: OutcomeSynced, SourceASIN: "B00SOURCE1"},
		{Format: "Audiobook", MatchMethod: "isbn", Outcome: OutcomeSynced, SourceASIN: "invalid", ASIN: "B00SOURCE1"},
		{Format: "Audiobook", MatchMethod: "isbn", Outcome: OutcomeSynced},
	} {
		assert.False(t, IsAudiobookIdentifierFallbackRecord(record), "%+v", record)
	}
}

func TestAudiobookIdentifierPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mapping     bool
		isbnMatch   bool
		counterpart bool
		fallback    bool
		isbnErr     error
		fallbackErr error
		wantEdition string
		wantKind    hardcover.ASINMatchKind
		wantErr     error
	}{
		{name: "mapping beats ISBN and fallback", mapping: true, isbnMatch: true, fallback: true, wantEdition: "201", wantKind: hardcover.ASINMatchAudibleMapping},
		{name: "ISBN beats fallback", isbnMatch: true, fallback: true, wantEdition: "202"},
		{name: "ISBN counterpart beats fallback", isbnMatch: true, counterpart: true, fallback: true, wantEdition: "202"},
		{name: "fallback after both ISBN misses", fallback: true, wantEdition: "203", wantKind: hardcover.ASINMatchEditionASIN},
		{name: "all identifiers miss", wantErr: errAudibleImportAvailable},
		{name: "ISBN failure cannot become fallback", isbnErr: errors.New("ISBN unavailable"), fallback: true, wantErr: errHardcoverLookupFailed},
		{name: "ambiguous fallback", fallbackErr: hardcover.ErrASINLookupConflict, wantErr: hardcover.ErrASINLookupConflict},
		{name: "fallback failure", fallbackErr: errors.New("fallback unavailable"), wantErr: errHardcoverLookupFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, hc := createTestService()
			book := associationTestBook("fallback-precedence", "b0audio001", "9780306406157")
			client := &audiobookFallbackClient{associationLookupClient: &associationLookupClient{MockHardcoverClient: hc}, err: tc.fallbackErr}
			if tc.mapping {
				client.result = &hardcover.ASINLookupResult{Book: &models.HardcoverBook{ID: "101", EditionID: "201"}, MatchKind: hardcover.ASINMatchAudibleMapping, RegionalExternalID: "B0AUDIO001:us"}
			} else {
				var primary, counterpart *models.HardcoverBook
				if tc.isbnMatch {
					primary = &models.HardcoverBook{ID: "102", EditionID: "202"}
					if tc.counterpart {
						counterpart, primary = primary, nil
					}
				}
				hc.On("SearchBookByISBN13", mock.Anything, "9780306406157").Return(primary, tc.isbnErr).Once()
				if primary == nil {
					hc.On("SearchBookByISBN10", mock.Anything, "0306406152").Return(counterpart, tc.isbnErr).Once()
				}
			}
			if tc.fallback {
				client.fallback = &hardcover.ASINLookupResult{Book: &models.HardcoverBook{ID: "103", EditionID: "203"}, MatchKind: hardcover.ASINMatchEditionASIN}
			}
			svc.hardcover = client
			got, err, _, result := svc.findBookInHardcoverWithASINMatch(hardcover.WithReadingFormat(context.Background(), models.ReadingFormatAudiobook), book, associationWriteASINOnly)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, tc.wantEdition, got.EditionID)
				if tc.wantKind != "" {
					require.NotNil(t, result)
					assert.Equal(t, tc.wantKind, result.MatchKind)
				} else {
					assert.Nil(t, result)
				}
			}
			_, saved := svc.state.GetAssociation(book.ID)
			assert.Equal(t, tc.mapping, saved, "only a regional mapping is remembered")
			hc.AssertExpectations(t)
		})
	}
}

func TestAudiobookFallbackOutcomesAndIncrementalReResolution(t *testing.T) {
	t.Run("synced then already current", func(t *testing.T) {
		svc, hc := createTestService()
		svc.config.Sync.Incremental = true
		svc.config.Sync.ProcessUnreadBooks = true
		svc.config.Sync.SyncWantToRead = true
		svc.config.Sync.SyncOwned = false
		book := associationTestBook("fallback-outcome", "B0AUDIO001", "")
		book.Progress.CurrentTime = 0
		client := &audiobookFallbackClient{associationLookupClient: &associationLookupClient{MockHardcoverClient: hc}, fallback: &hardcover.ASINLookupResult{Book: &models.HardcoverBook{ID: "901", EditionID: "902"}, MatchKind: hardcover.ASINMatchEditionASIN}}
		svc.hardcover = client
		hc.On("GetEdition", mock.Anything, "902").Return(&models.Edition{ID: "902", BookID: "901", ReadingFormatID: "2"}, nil).Once()
		hc.On("GetUserBookID", mock.Anything, 902).Return(903, nil).Once()
		hc.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{ID: 903, StatusID: 1}).Return(nil).Once()
		require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
		first := recordedOutcome(svc, book.ID)
		assert.Equal(t, string(hardcover.ASINMatchEditionASIN), first.MatchMethod)
		assert.True(t, IsAudiobookIdentifierFallbackRecord(first))
		assert.Equal(t, OutcomeSynced, first.Outcome)
		require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
		second := recordedOutcome(svc, book.ID)
		assert.True(t, IsAudiobookIdentifierFallbackRecord(second))
		assert.Equal(t, OutcomeAlreadyCurrent, second.Outcome)
		_, saved := svc.state.GetAssociation(book.ID)
		assert.False(t, saved)
		// A mapping added later must replace the ephemeral fallback even when
		// Audiobookshelf progress has not changed.
		client.result = &hardcover.ASINLookupResult{Book: &models.HardcoverBook{ID: "901", EditionID: "902"}, MatchKind: hardcover.ASINMatchAudibleMapping, RegionalExternalID: "B0AUDIO001:us"}
		require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
		association, saved := svc.state.GetAssociation(book.ID)
		require.True(t, saved)
		assert.Equal(t, "B0AUDIO001:us", association.RegionalExternalID)
		assert.False(t, IsAudiobookIdentifierFallbackRecord(recordedOutcome(svc, book.ID)))
		hc.AssertExpectations(t)
	})
}

func TestConfiguredSkippedAudiobooksDoNotMatchIdentifiersOrEnableEditionImport(t *testing.T) {
	tests := []struct {
		name               string
		processUnreadBooks bool
		syncWantToRead     bool
		progress           float64
		minimumProgress    float64
	}{
		{name: "unread books disabled", syncWantToRead: true},
		{name: "below minimum progress", processUnreadBooks: true, syncWantToRead: true, progress: 0.25, minimumProgress: 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, hc := createTestService()
			svc.config.Sync.ProcessUnreadBooks = tt.processUnreadBooks
			svc.config.Sync.SyncWantToRead = tt.syncWantToRead
			svc.config.Sync.MinimumProgress = tt.minimumProgress
			book := associationTestBook("fallback-skipped-"+tt.name, "B0AUDIO001", "9780306406157")
			book.Media.Duration = 1000
			book.Progress.CurrentTime = tt.progress * book.Media.Duration
			fallback := &audiobookFallbackClient{
				associationLookupClient: &associationLookupClient{MockHardcoverClient: hc},
				fallback:                &hardcover.ASINLookupResult{Book: &models.HardcoverBook{ID: "901", EditionID: "902"}, MatchKind: hardcover.ASINMatchEditionASIN},
			}
			svc.hardcover = fallback

			require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))

			record := recordedOutcome(svc, book.ID)
			assert.Equal(t, OutcomeSkipped, record.Outcome)
			assert.Empty(t, record.MatchMethod)
			assert.False(t, IsAudiobookIdentifierFallbackRecord(record))
			assert.Zero(t, fallback.searchCount, "skipped audiobooks must not search Audible mappings")
			assert.Zero(t, fallback.fallbackCalls, "skipped audiobooks must not search edition ASINs")
			assertNoHardcoverBookSearches(t, hc)
			_, saved := svc.state.GetAssociation(book.ID)
			assert.False(t, saved, "skipped audiobooks must not persist a match association")
			hc.AssertNotCalled(t, "GetEdition", mock.Anything, mock.Anything)
			hc.AssertNotCalled(t, "GetUserBookID", mock.Anything, mock.Anything)
			hc.AssertExpectations(t)
		})
	}
}
