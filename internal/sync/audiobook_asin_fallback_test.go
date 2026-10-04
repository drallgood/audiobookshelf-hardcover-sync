package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type audiobookFallbackClient struct {
	*associationLookupClient
	fallback *hardcover.ASINLookupResult
	err      error
}

func (c *audiobookFallbackClient) SearchBookByEditionASINResult(context.Context, string) (*hardcover.ASINLookupResult, error) {
	return c.fallback, c.err
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
	for _, skipped := range []bool{false, true} {
		t.Run(map[bool]string{false: "synced then already current", true: "skipped"}[skipped], func(t *testing.T) {
			svc, hc := createTestService()
			svc.config.Sync.Incremental = true
			svc.config.Sync.ProcessUnreadBooks = true
			svc.config.Sync.SyncWantToRead = !skipped
			svc.config.Sync.SyncOwned = false
			book := associationTestBook("fallback-outcome", "B0AUDIO001", "")
			book.Progress.CurrentTime = 0
			client := &audiobookFallbackClient{associationLookupClient: &associationLookupClient{MockHardcoverClient: hc}, fallback: &hardcover.ASINLookupResult{Book: &models.HardcoverBook{ID: "901", EditionID: "902"}, MatchKind: hardcover.ASINMatchEditionASIN}}
			svc.hardcover = client
			if !skipped {
				hc.On("GetEdition", mock.Anything, "902").Return(&models.Edition{ID: "902", BookID: "901", ReadingFormatID: "2"}, nil).Once()
				hc.On("GetUserBookID", mock.Anything, 902).Return(903, nil).Once()
				hc.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{ID: 903, StatusID: 1}).Return(nil).Once()
			}
			require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
			first := recordedOutcome(svc, book.ID)
			assert.Equal(t, string(hardcover.ASINMatchEditionASIN), first.MatchMethod)
			assert.True(t, IsAudiobookASINFallbackRecord(first))
			if skipped {
				assert.Equal(t, OutcomeSkipped, first.Outcome)
			} else {
				assert.Equal(t, OutcomeSynced, first.Outcome)
			}
			require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
			second := recordedOutcome(svc, book.ID)
			assert.True(t, IsAudiobookASINFallbackRecord(second))
			if !skipped {
				assert.Equal(t, OutcomeAlreadyCurrent, second.Outcome)
			}
			_, saved := svc.state.GetAssociation(book.ID)
			assert.False(t, saved)
			// A mapping added later must replace the ephemeral fallback even when
			// Audiobookshelf progress has not changed.
			client.result = &hardcover.ASINLookupResult{Book: &models.HardcoverBook{ID: "901", EditionID: "902"}, MatchKind: hardcover.ASINMatchAudibleMapping, RegionalExternalID: "B0AUDIO001:us"}
			require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
			association, saved := svc.state.GetAssociation(book.ID)
			require.True(t, saved)
			assert.Equal(t, "B0AUDIO001:us", association.RegionalExternalID)
			assert.False(t, IsAudiobookASINFallbackRecord(recordedOutcome(svc, book.ID)))
			hc.AssertExpectations(t)
		})
	}
}
