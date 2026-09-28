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

type sequencedASINLookupClient struct {
	*MockHardcoverClient
	results []*hardcover.ASINLookupResult
	errors  []error
	calls   int
}

func (c *sequencedASINLookupClient) SearchBookByASINResult(_ context.Context, _ string) (*hardcover.ASINLookupResult, error) {
	index := c.calls
	c.calls++
	if index >= len(c.results) {
		return nil, nil
	}
	return c.results[index], c.errors[index]
}

func TestProcessBookPersistsConfirmedASINProvenance(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "persisted", true: "dry run"}[dryRun], func(t *testing.T) {
			svc, client := createTestService()
			svc.config.Sync.IncludeEbooks = true
			svc.config.Sync.SyncOwned = false
			svc.config.Sync.MinimumProgress = 0.9
			svc.config.Sync.DryRun = dryRun

			book := *toAudiobookshelfBook(createTestBook("confirmed-asin", "Association Book", "Author", "ASIN-123", "9791032305690"))
			book.MediaType = "ebook"
			book.Progress.CurrentTime = 1800

			lookupClient := &sequencedASINLookupClient{
				MockHardcoverClient: client,
				results: []*hardcover.ASINLookupResult{
					nil,
					{
						Book:      &models.HardcoverBook{ID: "901", EditionID: "902"},
						MatchKind: hardcover.ASINMatchEditionASIN,
					},
				},
				errors: []error{errors.New("temporary ASIN miss"), nil},
			}
			svc.hardcover = lookupClient
			client.On("SearchBookByISBN13", mock.Anything, "9791032305690").
				Return(&models.HardcoverBook{ID: "901", EditionID: "902"}, nil).Once()

			err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

			require.NoError(t, err)
			assert.Equal(t, 2, lookupClient.calls, "lookup should confirm the same match through ASIN")
			association, saved := svc.state.GetAssociation(book.ID)
			if dryRun {
				assert.False(t, saved, "dry runs must not persist a confirmed ASIN association")
				assert.False(t, svc.state.IsDirty())
			} else {
				require.True(t, saved)
				assert.Equal(t, string(hardcover.ASINMatchEditionASIN), association.Provenance)
				assert.Equal(t, "901", association.HardcoverBookID)
				assert.Equal(t, "902", association.HardcoverEditionID)
			}
			client.AssertExpectations(t)
		})
	}
}

func TestProcessBookReconcilesOwnershipAfterBookOnlyISBNMatchResolvesEdition(t *testing.T) {
	svc, client := createTestService()
	svc.config.Sync.IncludeEbooks = true
	svc.config.Sync.SyncOwned = true
	book := *toAudiobookshelfBook(createTestBook("book-only-isbn", "Association Book", "Author", "", "0306406152"))
	book.MediaType = "ebook"
	book.Progress.CurrentTime = 300

	bookOnlyErr := hardcover.WithBookID(errors.New("edition unavailable for ISBN-10"), "901")
	client.On("SearchBookByISBN10", mock.Anything, "0306406152").Return((*models.HardcoverBook)(nil), bookOnlyErr).Twice()
	client.On("GetEdition", mock.Anything, "901").Return(&models.Edition{ID: "902", BookID: "901"}, nil).Once()
	client.On("GetEdition", mock.Anything, "902").Return(&models.Edition{ID: "902", BookID: "901"}, nil).Maybe()
	client.On("GetUserBookID", mock.Anything, 902).Return(903, nil).Maybe()
	client.On("CheckBookOwnership", mock.Anything, 901).Return(false, nil).Once()
	client.On("MarkEditionAsOwned", mock.Anything, 902).Return(nil).Once()
	client.On("GetUserBook", mock.Anything, "903").Return(&models.HardcoverBook{
		ID: "901", EditionID: "902", BookStatusID: 2,
	}, nil).Once()
	client.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{UserBookID: 903}).
		Return([]hardcover.UserBookRead{}, nil).Once()
	client.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
		return input.UserBookID == 903 && input.DatesRead.ProgressSeconds != nil && *input.DatesRead.ProgressSeconds == 300
	})).Return(1234, nil).Once()

	err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

	require.NoError(t, err)
	_, saved := svc.state.GetAssociation(book.ID)
	assert.False(t, saved, "an ISBN BookError without an edition must not become a durable association after edition resolution")
	client.AssertNumberOfCalls(t, "CheckBookOwnership", 1)
	client.AssertNumberOfCalls(t, "MarkEditionAsOwned", 1)
	client.AssertNumberOfCalls(t, "InsertUserBookRead", 1)
	client.AssertExpectations(t)
}
