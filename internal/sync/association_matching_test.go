package sync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/isbn"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type associationLookupClient struct {
	*MockHardcoverClient
	result             *hardcover.ASINLookupResult
	searchErr          error
	searchCount        int
	freshEdition       *models.Edition
	freshEditionErr    error
	freshEditionLookup int
}

func (c *associationLookupClient) SearchBookByASINResult(ctx context.Context, asin string) (*hardcover.ASINLookupResult, error) {
	c.searchCount++
	return c.result, c.searchErr
}

func (c *associationLookupClient) GetEditionUncached(ctx context.Context, editionID string) (*models.Edition, error) {
	c.freshEditionLookup++
	return c.freshEdition, c.freshEditionErr
}

func associationTestBook(id, asin, isbnValue string) models.AudiobookshelfBook {
	book := toAudiobookshelfBook(createTestBook(id, "Association Book", "Author", asin, isbnValue))
	return *book
}

func expectASINEditionRead(t *testing.T, client *MockHardcoverClient, bookID, editionID string) {
	t.Helper()
	editionInt, err := strconv.Atoi(editionID)
	require.NoError(t, err)
	// Only the final sync path reads the edition and user book, so a test that
	// stops earlier never reaches these. They are not the subject of these tests.
	client.On("GetEdition", mock.Anything, editionID).Return(&models.Edition{
		ID: editionID, BookID: bookID,
	}, nil).Maybe()
	client.On("GetUserBookID", mock.Anything, editionInt).Return(9021, nil).Maybe()
}

func TestFindBookInHardcoverReusesMatchingAssociationFirst(t *testing.T) {
	svc, client := createTestService()
	svc.config.Sync.SyncOwned = false
	book := associationTestBook("association-first", " ASIN-123 ", "978-0-306-40615-7")
	association := state.Association{
		ABSItemID:          book.ID,
		SourceASIN:         "ASIN-123",
		SourceISBN13:       "9780306406157",
		HardcoverBookID:    "901",
		HardcoverEditionID: "902",
		ReadingFormat:      models.ReadingFormatAudiobook,
		Provenance:         string(hardcover.ASINMatchAudibleMapping),
	}
	require.NoError(t, svc.state.SetAssociation(association))

	got, err := svc.findBookInHardcover(context.Background(), book)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, association.HardcoverBookID, got.ID)
	assert.Equal(t, association.HardcoverEditionID, got.EditionID)
	client.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
	client.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
	client.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
	client.AssertExpectations(t)
}

func TestFindBookInHardcoverReusesMatchingEbookAssociationFirst(t *testing.T) {
	tests := []struct {
		name       string
		provenance string
		asin       string
		isbn       string
	}{
		{
			name:       "edition_asin provenance",
			provenance: string(hardcover.ASINMatchEditionASIN),
			asin:       " ASIN-123 ",
		},
		{
			name:       "isbn provenance",
			isbn:       "978-0-306-40615-7",
			provenance: "isbn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, firstClient := createTestService()
			svc.config.Sync.SyncOwned = false
			book := associationTestBook("association-ebook-first-"+tt.name, tt.asin, tt.isbn)
			book.MediaType = "ebook"
			if tt.asin != "" {
				svc.hardcover = &associationLookupClient{
					MockHardcoverClient: firstClient,
					result: &hardcover.ASINLookupResult{
						Book:      &models.HardcoverBook{ID: "901", EditionID: "902"},
						MatchKind: hardcover.ASINMatchEditionASIN,
					},
				}
			} else {
				firstClient.On("SearchBookByISBN13", mock.Anything, "9780306406157").
					Return(&models.HardcoverBook{ID: "901", EditionID: "902"}, nil).Once()
			}
			expectASINEditionRead(t, firstClient, "901", "902")
			ctx := hardcover.WithReadingFormat(context.Background(), models.ReadingFormatEbook)
			first, err := svc.findBookInHardcover(ctx, book)
			require.NoError(t, err)
			require.NotNil(t, first)
			firstClient.AssertExpectations(t)

			path := filepath.Join(t.TempDir(), "sync-state.json")
			require.NoError(t, svc.state.Save(path))
			reloaded, err := state.LoadState(path)
			require.NoError(t, err)
			association, saved := reloaded.GetAssociation(book.ID)
			require.True(t, saved)
			assert.Equal(t, tt.provenance, association.Provenance)
			assert.Equal(t, "901", association.HardcoverBookID)
			assert.Equal(t, "902", association.HardcoverEditionID)

			nextSvc, nextClient := createTestService()
			nextSvc.state = reloaded
			nextSvc.config.Sync.SyncOwned = false
			got, err := nextSvc.findBookInHardcover(ctx, book)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, first.ID, got.ID)
			assert.Equal(t, first.EditionID, got.EditionID)
			// No Hardcover expectations are configured for the second lookup.
			// Any live identifier search would fail the mock.
			nextClient.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
			nextClient.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
			nextClient.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
		})
	}
}

func TestProcessBookReusedAssociationDoesNotReportSourceIdentifiersAsHardcover(t *testing.T) {
	svc, client := createTestService()
	svc.config.Sync.IncludeEbooks = true
	svc.config.Sync.SyncOwned = false
	svc.config.Sync.ProcessUnreadBooks = true
	svc.config.Sync.SyncWantToRead = false
	book := associationTestBook("association-ebook-source-identifiers", "ASIN-123", "978-0-306-40615-7")
	book.MediaType = "ebook"
	// The durable association stores ABS source identifiers but no verified
	// Hardcover edition identifiers, so reuse must not expose them as such.
	require.NoError(t, svc.state.SetAssociation(state.Association{
		ABSItemID:          book.ID,
		SourceASIN:         "ASIN-123",
		SourceISBN13:       "9780306406157",
		HardcoverBookID:    "901",
		HardcoverEditionID: "902",
		ReadingFormat:      models.ReadingFormatEbook,
		Provenance:         string(hardcover.ASINMatchEditionASIN),
	}))

	err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

	require.NoError(t, err)
	record := recordedOutcome(svc, book.ID)
	assert.Equal(t, "901", record.HardcoverBookID)
	assert.Equal(t, "902", record.EditionID)
	assert.Empty(t, record.HardcoverASIN)
	assert.Empty(t, record.HardcoverISBN)
	client.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
	client.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
	client.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
}

func TestFindBookInHardcoverPersistsVerifiedASINMatches(t *testing.T) {
	tests := []struct {
		name       string
		format     string
		matchKind  hardcover.ASINMatchKind
		wantSaved  bool
		wantRegion string
	}{
		{name: "verified audiobook mapping", format: models.ReadingFormatAudiobook, matchKind: hardcover.ASINMatchAudibleMapping, wantSaved: true, wantRegion: "B0AUDIO001:uk"},
		{name: "temporary edition ASIN fallback", format: models.ReadingFormatAudiobook, matchKind: hardcover.ASINMatchEditionASIN},
		{name: "ebook edition ASIN", format: models.ReadingFormatEbook, matchKind: hardcover.ASINMatchEditionASIN, wantSaved: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockClient := createTestService()
			svc.config.Sync.SyncOwned = false
			sourceASIN := "ASIN-123"
			if tt.format == models.ReadingFormatAudiobook {
				sourceASIN = "B0AUDIO001"
			}
			book := associationTestBook("association-"+tt.name, sourceASIN, "")
			if tt.format == models.ReadingFormatEbook {
				book.MediaType = "ebook"
			}
			hcClient := &associationLookupClient{
				MockHardcoverClient: mockClient,
				result: &hardcover.ASINLookupResult{
					Book:               &models.HardcoverBook{ID: "901", EditionID: "902"},
					MatchKind:          tt.matchKind,
					RegionalExternalID: tt.wantRegion,
				},
			}
			svc.hardcover = hcClient
			expectASINEditionRead(t, mockClient, "901", "902")
			ctx := hardcover.WithReadingFormat(context.Background(), tt.format)

			got, err := svc.findBookInHardcover(ctx, book)

			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, "902", got.EditionID)
			association, saved := svc.state.GetAssociation(book.ID)
			assert.Equal(t, tt.wantSaved, saved)
			if tt.wantSaved {
				assert.Equal(t, tt.wantRegion, association.RegionalExternalID)
				assert.Equal(t, sourceASIN, association.SourceASIN)
				assert.Equal(t, string(tt.matchKind), association.Provenance)
			} else {
				assert.False(t, svc.state.IsDirty())
			}
			mockClient.AssertExpectations(t)
		})
	}
}

func TestFindBookInHardcoverPersistsVerifiedISBNMatches(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		isbn      string
		wantSaved bool
	}{
		{name: "ebook ISBN-13", format: models.ReadingFormatEbook, isbn: "978-0-306-40615-7", wantSaved: true},
		{name: "ebook ISBN-10", format: models.ReadingFormatEbook, isbn: "0-306-40615-2", wantSaved: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockClient := createTestService()
			svc.config.Sync.SyncOwned = false
			book := associationTestBook("association-isbn-"+tt.name, "", tt.isbn)
			if tt.format == models.ReadingFormatEbook {
				book.MediaType = "ebook"
			}
			ctx := hardcover.WithReadingFormat(context.Background(), tt.format)
			candidates := isbnSearchCandidates(book.Media.Metadata.ISBN)
			require.NotEmpty(t, candidates)
			for _, candidate := range candidates {
				if candidate.is13 {
					mockClient.On("SearchBookByISBN13", mock.Anything, candidate.value).Return(&models.HardcoverBook{ID: "901", EditionID: "902"}, nil).Maybe()
				} else {
					mockClient.On("SearchBookByISBN10", mock.Anything, candidate.value).Return(&models.HardcoverBook{ID: "901", EditionID: "902"}, nil).Maybe()
				}
			}
			expectASINEditionRead(t, mockClient, "901", "902")

			got, err := svc.findBookInHardcover(ctx, book)

			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, "902", got.EditionID)
			association, saved := svc.state.GetAssociation(book.ID)
			assert.Equal(t, tt.wantSaved, saved)
			if tt.wantSaved {
				assert.Equal(t, "isbn", association.Provenance)
				assert.Equal(t, "901", association.HardcoverBookID)
				assert.Equal(t, "902", association.HardcoverEditionID)
				assert.True(t, association.SourceISBN10 != "" || association.SourceISBN13 != "")
			} else {
				assert.False(t, svc.state.IsDirty())
			}
			mockClient.AssertExpectations(t)
		})
	}
}

func TestDryRunDoesNotStageVerifiedEbookMatches(t *testing.T) {
	t.Run("editions.asin", func(t *testing.T) {
		svc, _ := createTestService()
		svc.config.Sync.DryRun = true
		book := associationTestBook("association-ebook-dry-run-asin", "ASIN-123", "")
		book.MediaType = "ebook"
		svc.recordVerifiedASINAssociation(book, &hardcover.ASINLookupResult{
			Book:      &models.HardcoverBook{ID: "901", EditionID: "902"},
			MatchKind: hardcover.ASINMatchEditionASIN,
		})

		_, exists := svc.state.GetAssociation(book.ID)
		assert.False(t, exists)
		assert.False(t, svc.state.IsDirty())
	})

	t.Run("isbn", func(t *testing.T) {
		svc, _ := createTestService()
		svc.config.Sync.DryRun = true
		book := associationTestBook("association-ebook-dry-run-isbn", "", "978-0-306-40615-7")
		book.MediaType = "ebook"
		svc.recordVerifiedISBNAssociation(book, &models.HardcoverBook{ID: "901", EditionID: "902"})

		_, exists := svc.state.GetAssociation(book.ID)
		assert.False(t, exists)
		assert.False(t, svc.state.IsDirty())
	})
}

func TestProcessBookDoesNotRepeatTemporaryASINFallbackLookup(t *testing.T) {
	svc, mockClient := createTestService()
	svc.config.Sync.SyncOwned = false
	svc.config.Sync.SyncWantToRead = false
	book := associationTestBook("association-temporary-process", "B0AUDIO001", "")
	svc.config.Sync.ProcessUnreadBooks = true
	book.Progress.CurrentTime = 0
	client := &associationLookupClient{
		MockHardcoverClient: mockClient,
		result: &hardcover.ASINLookupResult{
			Book:      &models.HardcoverBook{ID: "901", EditionID: "902"},
			MatchKind: hardcover.ASINMatchEditionASIN,
		},
	}
	svc.hardcover = client
	editionInt, err := strconv.Atoi("902")
	require.NoError(t, err)
	mockClient.On("GetEdition", mock.Anything, "902").Return(&models.Edition{ID: "902", BookID: "901"}, nil).Maybe()
	mockClient.On("GetUserBookID", mock.Anything, editionInt).Return(9021, nil).Maybe()

	err = svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

	require.NoError(t, err)
	assert.Equal(t, 1, client.searchCount, "one item should make one external ASIN lookup even when the result is not persistable")
	_, saved := svc.state.GetAssociation(book.ID)
	assert.False(t, saved, "edition-ASIN fallback remains temporary")
	mockClient.AssertExpectations(t)
}

func TestProcessBookReconcilesOwnershipForSavedEbookAssociation(t *testing.T) {
	tests := []struct {
		name    string
		isOwned bool
	}{
		{name: "marks unowned book"},
		{name: "leaves already owned book alone", isOwned: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, client := createTestService()
			svc.config.Sync.IncludeEbooks = true
			svc.config.Sync.SyncOwned = true
			svc.config.Sync.ProcessUnreadBooks = true
			svc.config.Sync.SyncWantToRead = true
			book := associationTestBook("association-ebook-owned", "ASIN-123", "")
			book.MediaType = "ebook"
			require.NoError(t, svc.state.SetAssociation(state.Association{
				ABSItemID:          book.ID,
				SourceASIN:         "ASIN-123",
				HardcoverBookID:    "901",
				HardcoverEditionID: "902",
				ReadingFormat:      models.ReadingFormatEbook,
				Provenance:         string(hardcover.ASINMatchEditionASIN),
			}))

			expectASINEditionRead(t, client, "901", "902")
			client.On("CheckBookOwnership", mock.Anything, 901).Return(tt.isOwned, nil).Once()
			if !tt.isOwned {
				client.On("MarkEditionAsOwned", mock.Anything, 902).Return(nil).Once()
			}
			client.On("UpdateUserBookStatus", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookStatusInput) bool {
				return input.ID == 9021 && input.StatusID == 1
			})).Return(nil).Once()

			err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

			require.NoError(t, err)
			client.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
			if tt.isOwned {
				client.AssertNotCalled(t, "MarkEditionAsOwned", mock.Anything, mock.Anything)
			}
			client.AssertExpectations(t)
		})
	}
}

func TestProcessBookReconcilesOwnershipForFreshEbookASINMatch(t *testing.T) {
	svc, mockClient := createTestService()
	svc.config.Sync.IncludeEbooks = true
	svc.config.Sync.SyncOwned = true
	svc.config.Sync.ProcessUnreadBooks = true
	svc.config.Sync.SyncWantToRead = true
	book := associationTestBook("association-ebook-fresh-owned", "ASIN-123", "978-0-306-40615-7")
	book.MediaType = "ebook"
	client := &associationLookupClient{
		MockHardcoverClient: mockClient,
		result: &hardcover.ASINLookupResult{
			Book: &models.HardcoverBook{
				ID: "901", EditionID: "902", EditionASIN: "ASIN-123",
			},
			MatchKind: hardcover.ASINMatchEditionASIN,
		},
	}
	svc.hardcover = client
	expectASINEditionRead(t, mockClient, "901", "902")
	expectASINEditionRead(t, mockClient, "901", "902")
	mockClient.On("CheckBookOwnership", mock.Anything, 901).Return(false, nil).Once()
	mockClient.On("MarkEditionAsOwned", mock.Anything, 902).Return(nil).Once()
	mockClient.On("UpdateUserBookStatus", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookStatusInput) bool {
		return input.ID == 9021 && input.StatusID == 1
	})).Return(nil).Once()

	err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

	require.NoError(t, err)
	assert.Equal(t, 1, client.searchCount, "a fresh ASIN match should make one identifier lookup")
	association, exists := svc.state.GetAssociation(book.ID)
	require.True(t, exists)
	assert.Equal(t, "ASIN-123", association.SourceASIN)
	assert.Equal(t, isbn.Normalize(book.Media.Metadata.ISBN), isbn.Normalize(association.SourceISBN13))
	assert.Equal(t, "901", association.HardcoverBookID)
	assert.Equal(t, "902", association.HardcoverEditionID)
	mockClient.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
	mockClient.AssertExpectations(t)
}

func TestProcessBookReconcilesOwnershipForFreshAudiobookASINMatch(t *testing.T) {
	tests := []struct {
		name      string
		matchKind hardcover.ASINMatchKind
		regionID  string
		wantSaved bool
	}{
		{
			name:      "regional Audible mapping is remembered",
			matchKind: hardcover.ASINMatchAudibleMapping,
			regionID:  "B0AUDIO001:uk",
			wantSaved: true,
		},
		{
			name:      "edition ASIN fallback stays temporary",
			matchKind: hardcover.ASINMatchEditionASIN,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockClient := createTestService()
			svc.config.Sync.SyncOwned = true
			svc.config.Sync.ProcessUnreadBooks = true
			svc.config.Sync.SyncWantToRead = true
			book := associationTestBook("association-audiobook-fresh-"+tt.name, "B0AUDIO001", "")
			client := &associationLookupClient{
				MockHardcoverClient: mockClient,
				result: &hardcover.ASINLookupResult{
					Book:               &models.HardcoverBook{ID: "901", EditionID: "902"},
					MatchKind:          tt.matchKind,
					RegionalExternalID: tt.regionID,
				},
			}
			svc.hardcover = client
			expectASINEditionRead(t, mockClient, "901", "902")
			expectASINEditionRead(t, mockClient, "901", "902")
			mockClient.On("CheckBookOwnership", mock.Anything, 901).Return(false, nil).Once()
			mockClient.On("MarkEditionAsOwned", mock.Anything, 902).Return(nil).Once()
			mockClient.On("UpdateUserBookStatus", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookStatusInput) bool {
				return input.ID == 9021 && input.StatusID == 1
			})).Return(nil).Once()

			err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

			require.NoError(t, err)
			assert.Equal(t, 1, client.searchCount)
			association, saved := svc.state.GetAssociation(book.ID)
			assert.Equal(t, tt.wantSaved, saved)
			if tt.wantSaved {
				assert.Equal(t, "B0AUDIO001:uk", association.RegionalExternalID)
				assert.Equal(t, string(tt.matchKind), association.Provenance)
			}
			mockClient.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
			mockClient.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
			mockClient.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
			mockClient.AssertExpectations(t)
		})
	}
}

func TestProcessBookReconcilesOwnershipForReusedAudiobookAssociation(t *testing.T) {
	tests := []struct {
		name    string
		isOwned bool
		dryRun  bool
	}{
		{name: "marks unowned edition"},
		{name: "leaves already owned edition alone", isOwned: true},
		{name: "dry run does not mark edition", dryRun: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, client := createTestService()
			svc.config.Sync.SyncOwned = true
			svc.config.Sync.ProcessUnreadBooks = true
			svc.config.Sync.SyncWantToRead = true
			svc.config.Sync.DryRun = tt.dryRun
			book := associationTestBook("association-audiobook-reused-"+tt.name, "B0AUDIO001", "")
			require.NoError(t, svc.state.SetAssociation(state.Association{
				ABSItemID: book.ID, SourceASIN: "B0AUDIO001", RegionalExternalID: "B0AUDIO001:uk",
				HardcoverBookID: "901", HardcoverEditionID: "902",
				ReadingFormat: models.ReadingFormatAudiobook, Provenance: string(hardcover.ASINMatchAudibleMapping),
			}))

			expectASINEditionRead(t, client, "901", "902")
			client.On("CheckBookOwnership", mock.Anything, 901).Return(tt.isOwned, nil).Once()
			if !tt.isOwned && !tt.dryRun {
				client.On("MarkEditionAsOwned", mock.Anything, 902).Return(nil).Once()
			}
			if !tt.dryRun {
				client.On("UpdateUserBookStatus", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookStatusInput) bool {
					return input.ID == 9021 && input.StatusID == 1
				})).Return(nil).Once()
			}

			err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

			require.NoError(t, err)
			client.AssertNumberOfCalls(t, "CheckBookOwnership", 1)
			client.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
			if tt.isOwned || tt.dryRun {
				client.AssertNotCalled(t, "MarkEditionAsOwned", mock.Anything, mock.Anything)
			} else {
				client.AssertNumberOfCalls(t, "MarkEditionAsOwned", 1)
			}
			if tt.dryRun {
				client.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
				assert.Equal(t, OutcomeWouldSync, recordedOutcome(svc, book.ID).Outcome)
			} else {
				client.AssertNumberOfCalls(t, "UpdateUserBookStatus", 1)
			}
			client.AssertExpectations(t)
		})
	}
}

func TestProcessBookRechecksEbookISBNBeforeSavingAssociation(t *testing.T) {
	svc, client := createTestService()
	svc.config.Sync.IncludeEbooks = true
	svc.config.Sync.SyncOwned = false
	book := isbnSearchBook(testISBN13NoTen)
	book.MediaType = "ebook"
	book.Progress.CurrentTime = 300

	// The first result disappears before the pre-mutation lookup. The first
	// result must not become a reusable association or be used for progress.
	client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
		Return(&models.HardcoverBook{ID: "901", EditionID: "902"}, nil).Once()
	client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
		Return((*models.HardcoverBook)(nil), nil).Maybe()

	err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

	require.ErrorIs(t, err, ErrSkippedBook)
	assert.Equal(t, OutcomeNotFound, recordedOutcome(svc, book.ID).Outcome)
	_, saved := svc.state.GetAssociation(book.ID)
	assert.False(t, saved, "the unconfirmed first result must not survive")
	client.AssertExpectations(t)
}

func TestProcessBookRejectsChangedEbookISBNTarget(t *testing.T) {
	svc, client := createTestService()
	svc.config.Sync.IncludeEbooks = true
	svc.config.Sync.SyncOwned = false
	book := isbnSearchBook(testISBN13NoTen)
	book.MediaType = "ebook"
	book.Progress.CurrentTime = 300

	client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
		Return(&models.HardcoverBook{ID: "901", EditionID: "902"}, nil).Once()
	client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
		Return(&models.HardcoverBook{ID: "903", EditionID: "904"}, nil).Once()
	client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
		Return((*models.HardcoverBook)(nil), nil).Maybe()
	client.On("GetEdition", mock.Anything, "904").
		Return(&models.Edition{ID: "904", BookID: "903"}, nil).Maybe()
	client.On("GetBookByID", mock.Anything, "903").
		Return(&models.HardcoverBook{ID: "903", EditionID: "904"}, nil).Maybe()

	err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

	require.ErrorIs(t, err, ErrSkippedBook)
	assert.Equal(t, OutcomeFailed, recordedOutcome(svc, book.ID).Outcome)
	_, saved := svc.state.GetAssociation(book.ID)
	assert.False(t, saved, "neither inconsistent result should be saved")
	client.AssertExpectations(t)
}

func TestProcessBookSavesEbookISBNAfterStableRecheck(t *testing.T) {
	svc, client := createTestService()
	svc.config.Sync.IncludeEbooks = true
	svc.config.Sync.SyncOwned = false
	svc.config.Sync.ProcessUnreadBooks = true
	svc.config.Sync.SyncWantToRead = true
	book := isbnSearchBook(testISBN13NoTen)
	book.MediaType = "ebook"

	client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
		Return(&models.HardcoverBook{ID: "901", EditionID: "902"}, nil).Twice()
	expectASINEditionRead(t, client, "901", "902")
	client.On("GetEdition", mock.Anything, "902").
		Return(&models.Edition{ID: "902", BookID: "901"}, nil).Maybe()
	client.On("GetUserBookID", mock.Anything, 902).Return(9021, nil).Maybe()
	client.On("UpdateUserBookStatus", mock.Anything, mock.MatchedBy(func(input hardcover.UpdateUserBookStatusInput) bool {
		return input.ID == 9021 && input.StatusID == 1
	})).Return(nil).Once()

	err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

	require.NoError(t, err)
	association, saved := svc.state.GetAssociation(book.ID)
	require.True(t, saved)
	assert.Equal(t, "901", association.HardcoverBookID)
	assert.Equal(t, "902", association.HardcoverEditionID)
	assert.Equal(t, "isbn", association.Provenance)
	client.AssertExpectations(t)
}

func TestProcessBookConfirmsEbookISBNBeforePostMatchSkips(t *testing.T) {
	tests := []struct {
		name             string
		progress         float64
		minimumProgress  float64
		syncWantToRead   bool
		incremental      bool
		reuseAssociation bool
		dryRun           bool
	}{
		{name: "unread when want-to-read sync is disabled"},
		{name: "below minimum progress", progress: 0.25, minimumProgress: 0.5, syncWantToRead: true},
		{name: "composite incremental state is current", progress: 0.5, syncWantToRead: true, incremental: true},
		{name: "reused association on unread book", reuseAssociation: true},
		{name: "dry-run unread match", dryRun: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, client := createTestService()
			svc.config.Sync.IncludeEbooks = true
			svc.config.Sync.ProcessUnreadBooks = true
			svc.config.Sync.SyncWantToRead = tt.syncWantToRead
			svc.config.Sync.SyncOwned = true
			svc.config.Sync.Incremental = tt.incremental
			svc.config.Sync.DryRun = tt.dryRun
			svc.config.Sync.MinimumProgress = tt.minimumProgress
			book := isbnSearchBook(testISBN13NoTen)
			book.ID = "association-post-match-skip-" + tt.name
			book.MediaType = "ebook"
			book.Progress.CurrentTime = tt.progress * book.Media.Duration
			book.Progress.StartedAt = 0

			if tt.incremental {
				// The base checkpoint differs enough to pass the pre-match filter,
				// while the matched edition checkpoint is already current.
				stateKey := book.ID + ":902"
				svc.state.UpdateBook(stateKey, tt.progress, "IN_PROGRESS")
				svc.state.SetHasProgressSeconds(stateKey)
				svc.state.UpdateBook(book.ID, 0.1, "IN_PROGRESS")
				svc.state.SetHasProgressSeconds(book.ID)
			}
			if tt.reuseAssociation {
				require.NoError(t, svc.state.SetAssociation(state.Association{
					ABSItemID: book.ID, SourceISBN13: testISBN13NoTen,
					HardcoverBookID: "901", HardcoverEditionID: "902",
					ReadingFormat: models.ReadingFormatEbook, Provenance: "isbn",
				}))
			} else {
				client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
					Return(&models.HardcoverBook{ID: "901", EditionID: "902"}, nil).Twice()
			}
			client.On("CheckBookOwnership", mock.Anything, 901).Return(false, nil).Once()
			if !tt.dryRun {
				client.On("MarkEditionAsOwned", mock.Anything, 902).Return(nil).Once()
			}

			err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

			require.NoError(t, err)
			association, saved := svc.state.GetAssociation(book.ID)
			if tt.dryRun {
				assert.False(t, saved, "dry runs must not persist a confirmed ISBN association")
				assert.False(t, svc.state.IsDirty())
			} else {
				require.True(t, saved, "the stable ebook ISBN match should be remembered before a post-match skip")
				assert.Equal(t, "901", association.HardcoverBookID)
				assert.Equal(t, "902", association.HardcoverEditionID)
				client.AssertNumberOfCalls(t, "MarkEditionAsOwned", 1)
			}
			client.AssertNumberOfCalls(t, "CheckBookOwnership", 1)
			client.AssertNotCalled(t, "GetUserBookID", mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "CreateUserBook", mock.Anything, mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "UpdateUserBookRead", mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "DeleteUserBookRead", mock.Anything, mock.Anything)
			if tt.reuseAssociation {
				client.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
			} else {
				client.AssertNumberOfCalls(t, "SearchBookByISBN13", 2)
			}
			if tt.dryRun {
				client.AssertNotCalled(t, "MarkEditionAsOwned", mock.Anything, mock.Anything)
			}
			client.AssertExpectations(t)
		})
	}
}

func TestProcessBookDoesNotReconcileAudiobookISBNOnSkippedBooks(t *testing.T) {
	tests := []struct {
		name            string
		progress        float64
		minimumProgress float64
		syncWantToRead  bool
		wantLookups     int
	}{
		{name: "unread when want-to-read sync is disabled", wantLookups: 1},
		{name: "below minimum progress", progress: 0.25, minimumProgress: 0.5, syncWantToRead: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, client := createTestService()
			svc.config.Sync.ProcessUnreadBooks = true
			svc.config.Sync.SyncWantToRead = tt.syncWantToRead
			svc.config.Sync.SyncOwned = true
			svc.config.Sync.MinimumProgress = tt.minimumProgress
			book := isbnSearchBook(testISBN13NoTen)
			book.MediaType = "book"
			book.ID = "association-audiobook-post-match-skip-" + tt.name
			book.Progress.CurrentTime = tt.progress * book.Media.Duration
			book.Progress.StartedAt = 0
			if tt.wantLookups > 0 {
				client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
					Return(hardcoverHit(), nil).Once()
			}

			err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

			require.NoError(t, err)
			_, saved := svc.state.GetAssociation(book.ID)
			assert.False(t, saved, "audiobook ISBNs must not create saved matches")
			client.AssertNumberOfCalls(t, "SearchBookByISBN13", tt.wantLookups)
			client.AssertNotCalled(t, "CheckBookOwnership", mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "MarkEditionAsOwned", mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "GetUserBookID", mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "CreateUserBook", mock.Anything, mock.Anything, mock.Anything)
			client.AssertExpectations(t)
		})
	}
}

func TestProcessBookIncrementalAudiobookISBNRechecksWithoutRepeatingWrites(t *testing.T) {
	for _, tt := range []struct {
		name        string
		asin        string
		changedISBN bool
	}{
		{name: "missing ASIN"},
		{name: "malformed ASIN", asin: "not-a-valid-ASIN"},
		{name: "changed ISBN selects a new edition", changedISBN: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, client := createTestService()
			svc.config.Sync.Incremental = true
			svc.config.Sync.ProcessUnreadBooks = true
			svc.config.Sync.SyncWantToRead = true
			svc.config.Sync.SyncOwned = false
			book := isbnSearchBook(testISBN13NoTen)
			book.MediaType = "book"
			book.Media.Metadata.ASIN = tt.asin
			firstLookupCount := 3
			if tt.changedISBN {
				firstLookupCount = 2
			}
			client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
				Return(hardcoverHit(), nil).Times(firstLookupCount)
			client.On("GetEdition", mock.Anything, "902").Return(&models.Edition{
				ID: "902", BookID: "901", ReadingFormatID: "2",
			}, nil).Once()
			client.On("GetUserBookID", mock.Anything, 902).Return(903, nil).Once()
			client.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
				ID: 903, StatusID: 1,
			}).Return(nil).Once()

			require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
			firstCheckpoint, exists := svc.state.GetBookState(book.ID + ":902")
			require.True(t, exists)
			require.Equal(t, "WANT_TO_READ", firstCheckpoint.Status)

			if tt.changedISBN {
				book.Media.Metadata.ISBN = testISBN13
				client.On("SearchBookByISBN13", mock.Anything, testISBN13).
					Return(&models.HardcoverBook{ID: "905", EditionID: "904"}, nil).Twice()
				client.On("GetEdition", mock.Anything, "904").Return(&models.Edition{
					ID: "904", BookID: "905", ReadingFormatID: "2",
				}, nil).Once()
				client.On("GetUserBookID", mock.Anything, 904).Return(906, nil).Once()
				client.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
					ID: 906, StatusID: 1,
				}).Return(nil).Once()
			}

			require.NoError(t, svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{}))
			if tt.changedISBN {
				assert.Equal(t, OutcomeSynced, recordedOutcome(svc, book.ID).Outcome)
				newCheckpoint, exists := svc.state.GetBookState(book.ID + ":904")
				require.True(t, exists)
				assert.Equal(t, "WANT_TO_READ", newCheckpoint.Status)
			} else {
				assert.Equal(t, OutcomeAlreadyCurrent, recordedOutcome(svc, book.ID).Outcome)
				checkpoint, exists := svc.state.GetBookState(book.ID + ":902")
				require.True(t, exists)
				assert.Equal(t, firstCheckpoint, checkpoint)
				client.AssertNumberOfCalls(t, "UpdateUserBookStatus", 1)
			}
			_, saved := svc.state.GetAssociation(book.ID)
			assert.False(t, saved, "fresh ISBN lookups must remain ephemeral")
			client.AssertExpectations(t)
		})
	}
}

func TestProcessBookRechecksAudiobookISBNIdentityBeforeMutations(t *testing.T) {
	tests := []struct {
		name       string
		secondBook *models.HardcoverBook
		wantSynced bool
	}{
		{name: "stable match", secondBook: &models.HardcoverBook{ID: "901", EditionID: "902"}, wantSynced: true},
		{name: "changed book", secondBook: &models.HardcoverBook{ID: "903", EditionID: "902"}},
		{name: "changed edition", secondBook: &models.HardcoverBook{ID: "901", EditionID: "904"}},
		{name: "disappeared match"},
	}
	for _, sourceASIN := range []string{"", "not-a-valid-ASIN"} {
		asinName := "missing ASIN"
		if sourceASIN != "" {
			asinName = "malformed ASIN"
		}
		verificationCases := tests
		if sourceASIN != "" {
			// The malformed identifier case verifies the normal ISBN fallback;
			// failure outcomes also run mismatch enrichment, which would issue a
			// live Audnex request for any non-empty source ASIN.
			verificationCases = tests[:1]
		}
		for _, tt := range verificationCases {
			t.Run(asinName+"/"+tt.name, func(t *testing.T) {
				svc, client := createTestService()
				svc.config.Sync.ProcessUnreadBooks = true
				svc.config.Sync.SyncOwned = true
				svc.config.Sync.SyncWantToRead = true
				book := isbnSearchBook(testISBN13NoTen)
				book.ID = "audiobook-isbn-recheck-" + asinName + "-" + tt.name
				book.MediaType = "book"
				book.Media.Metadata.ASIN = sourceASIN

				client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
					Return(&models.HardcoverBook{ID: "901", EditionID: "902"}, nil).Once()
				client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
					Return(tt.secondBook, nil).Once()
				if tt.wantSynced {
					client.On("CheckBookOwnership", mock.Anything, 901).Return(false, nil).Once()
					client.On("MarkEditionAsOwned", mock.Anything, 902).Return(nil).Once()
					client.On("GetEdition", mock.Anything, "902").Return(&models.Edition{
						ID: "902", BookID: "901", ReadingFormatID: "2",
					}, nil).Once()
					client.On("GetUserBookID", mock.Anything, 902).Return(903, nil).Once()
					client.On("UpdateUserBookStatus", mock.Anything, hardcover.UpdateUserBookStatusInput{
						ID: 903, StatusID: 1,
					}).Return(nil).Once()
				} else {
					client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
						Return((*models.HardcoverBook)(nil), nil).Maybe()
					client.On("GetEdition", mock.Anything, mock.Anything).
						Return((*models.Edition)(nil), nil).Maybe()
					client.On("GetBookByID", mock.Anything, mock.Anything).
						Return((*models.HardcoverBook)(nil), nil).Maybe()
				}

				err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

				stateKey := book.ID + ":902"
				bookState, hasCheckpoint := svc.state.GetBookState(stateKey)
				_, hasAssociation := svc.state.GetAssociation(book.ID)
				assert.False(t, hasAssociation, "audiobook ISBN matches remain ephemeral")
				if tt.wantSynced {
					require.NoError(t, err)
					require.True(t, hasCheckpoint)
					assert.Equal(t, "WANT_TO_READ", bookState.Status)
					assert.Equal(t, OutcomeSynced, recordedOutcome(svc, book.ID).Outcome)
				} else {
					require.ErrorIs(t, err, ErrSkippedBook)
					assert.Equal(t, OutcomeFailed, recordedOutcome(svc, book.ID).Outcome)
					require.True(t, hasCheckpoint)
					assert.Equal(t, "SKIPPED", bookState.Status, "an unstable target must not receive a successful checkpoint")
					client.AssertNotCalled(t, "CheckBookOwnership", mock.Anything, mock.Anything)
					client.AssertNotCalled(t, "MarkEditionAsOwned", mock.Anything, mock.Anything)
					client.AssertNotCalled(t, "GetUserBookID", mock.Anything, mock.Anything)
					client.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
				}
				client.AssertExpectations(t)
			})
		}
	}
}

func TestProcessBookRejectsUnstableEbookISBNBeforePostMatchSkip(t *testing.T) {
	temporaryErr := errors.New("temporary lookup failure")
	tests := []struct {
		name        string
		secondBook  *models.HardcoverBook
		secondErr   error
		wantOutcome SyncOutcome
	}{
		{name: "missing", wantOutcome: OutcomeNotFound},
		{name: "changed target", secondBook: &models.HardcoverBook{ID: "903", EditionID: "904"}, wantOutcome: OutcomeFailed},
		{name: "lookup error", secondErr: temporaryErr, wantOutcome: OutcomeFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, client := createTestService()
			svc.config.Sync.IncludeEbooks = true
			svc.config.Sync.ProcessUnreadBooks = true
			svc.config.Sync.SyncWantToRead = false
			svc.config.Sync.SyncOwned = true
			book := isbnSearchBook(testISBN13NoTen)
			book.MediaType = "ebook"
			client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
				Return(&models.HardcoverBook{ID: "901", EditionID: "902"}, nil).Once()
			client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
				Return(tt.secondBook, tt.secondErr).Once()
			client.On("SearchBookByISBN13", mock.Anything, testISBN13NoTen).
				Return((*models.HardcoverBook)(nil), nil).Maybe()
			client.On("GetEdition", mock.Anything, mock.Anything).
				Return((*models.Edition)(nil), nil).Maybe()
			client.On("GetBookByID", mock.Anything, mock.Anything).
				Return((*models.HardcoverBook)(nil), nil).Maybe()

			err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

			require.ErrorIs(t, err, ErrSkippedBook)
			assert.Equal(t, tt.wantOutcome, recordedOutcome(svc, book.ID).Outcome)
			_, saved := svc.state.GetAssociation(book.ID)
			assert.False(t, saved, "an unconfirmed ebook result must not be remembered")
			client.AssertNotCalled(t, "CheckBookOwnership", mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "MarkEditionAsOwned", mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "GetUserBookID", mock.Anything, mock.Anything)
			client.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
			client.AssertExpectations(t)
		})
	}
}

func TestFindBookInHardcoverIdentifierChangeInvalidatesAndRematches(t *testing.T) {
	svc, mockClient := createTestService()
	svc.config.Sync.SyncOwned = false
	book := associationTestBook("association-changed", "B0NEWASIN1", "")
	require.NoError(t, svc.state.SetAssociation(state.Association{
		ABSItemID: book.ID, SourceASIN: "ASIN-OLD", HardcoverBookID: "old-book",
		HardcoverEditionID: "old-edition", ReadingFormat: models.ReadingFormatAudiobook,
	}))
	client := &associationLookupClient{
		MockHardcoverClient: mockClient,
		result: &hardcover.ASINLookupResult{
			Book:               &models.HardcoverBook{ID: "901", EditionID: "902"},
			MatchKind:          hardcover.ASINMatchAudibleMapping,
			RegionalExternalID: "B0NEWASIN1:us",
		},
	}
	svc.hardcover = client
	expectASINEditionRead(t, mockClient, "901", "902")

	got, err := svc.findBookInHardcover(context.Background(), book)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "901", got.ID)
	association, exists := svc.state.GetAssociation(book.ID)
	require.True(t, exists)
	assert.Equal(t, "B0NEWASIN1", association.SourceASIN)
	assert.Equal(t, "901", association.HardcoverBookID)
	mockClient.AssertExpectations(t)
}

func TestFindBookInHardcoverStaleEbookAssociationDiscardedAndRematches(t *testing.T) {
	svc, mockClient := createTestService()
	svc.config.Sync.SyncOwned = false
	book := associationTestBook("association-ebook-changed", "ASIN-NEW", "")
	book.MediaType = "ebook"
	require.NoError(t, svc.state.SetAssociation(state.Association{
		ABSItemID: book.ID, SourceASIN: "ASIN-OLD", HardcoverBookID: "old-book",
		HardcoverEditionID: "old-edition", ReadingFormat: models.ReadingFormatEbook,
		Provenance: string(hardcover.ASINMatchEditionASIN),
	}))
	client := &associationLookupClient{
		MockHardcoverClient: mockClient,
		result: &hardcover.ASINLookupResult{
			Book:      &models.HardcoverBook{ID: "901", EditionID: "902"},
			MatchKind: hardcover.ASINMatchEditionASIN,
		},
	}
	svc.hardcover = client
	ctx := hardcover.WithReadingFormat(context.Background(), models.ReadingFormatEbook)
	expectASINEditionRead(t, mockClient, "901", "902")

	got, err := svc.findBookInHardcover(ctx, book)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "901", got.ID, "stale association must not be reused; a fresh lookup should run instead")
	association, exists := svc.state.GetAssociation(book.ID)
	require.True(t, exists)
	assert.Equal(t, "ASIN-NEW", association.SourceASIN)
	assert.Equal(t, "901", association.HardcoverBookID)
	assert.Equal(t, string(hardcover.ASINMatchEditionASIN), association.Provenance)
	mockClient.AssertExpectations(t)
}

func TestProcessBookIncrementalSkipPreservesAssociationOnIdentifierOnlyChange(t *testing.T) {
	svc, client := createTestService()
	svc.config.Sync.Incremental = true
	svc.config.Sync.ProcessUnreadBooks = true
	book := associationTestBook("association-incremental", "ASIN-NEW", "")
	book.Progress.CurrentTime = book.Media.Duration / 2
	svc.state.UpdateBook(book.ID, 0.5, "IN_PROGRESS")
	svc.state.SetHasProgressSeconds(book.ID)
	require.NoError(t, svc.state.SetAssociation(state.Association{
		ABSItemID: book.ID, SourceASIN: "ASIN-OLD", HardcoverBookID: "901",
		HardcoverEditionID: "902", ReadingFormat: models.ReadingFormatAudiobook,
	}))

	err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

	require.NoError(t, err)
	assert.Equal(t, OutcomeAlreadyCurrent, recordedOutcome(svc, book.ID).Outcome)
	association, exists := svc.state.GetAssociation(book.ID)
	require.True(t, exists)
	assert.Equal(t, "ASIN-OLD", association.SourceASIN)
	client.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
}

func TestDryRunDoesNotStageVerifiedAssociation(t *testing.T) {
	svc, _ := createTestService()
	svc.config.Sync.DryRun = true
	book := associationTestBook("association-dry-run", "ASIN-123", "")
	svc.recordVerifiedASINAssociation(book, &hardcover.ASINLookupResult{
		Book:               &models.HardcoverBook{ID: "901", EditionID: "902"},
		MatchKind:          hardcover.ASINMatchAudibleMapping,
		RegionalExternalID: "ASIN-123:us",
	})

	_, exists := svc.state.GetAssociation(book.ID)
	assert.False(t, exists)
	assert.False(t, svc.state.IsDirty())
}

func TestDryRunDoesNotForgetConfirmedMissingEditionAssociation(t *testing.T) {
	svc, mockClient := createTestService()
	svc.config.Sync.DryRun = true
	book := associationTestBook("association-dry-run-missing", "ASIN-123", "")
	association := state.Association{
		ABSItemID: book.ID, SourceASIN: "ASIN-123", HardcoverBookID: "901",
		HardcoverEditionID: "902", ReadingFormat: models.ReadingFormatAudiobook,
	}
	require.NoError(t, svc.state.SetAssociation(association))
	wasDirty := svc.state.IsDirty()
	client := &associationLookupClient{
		MockHardcoverClient: mockClient,
		freshEditionErr:     fmt.Errorf("%w: 902", models.ErrEditionNotFound),
	}
	svc.hardcover = client

	removed := svc.forgetConfirmedMissingEdition(context.Background(), book.ID, "902")

	assert.False(t, removed)
	assert.Equal(t, 0, client.freshEditionLookup)
	got, exists := svc.state.GetAssociation(book.ID)
	require.True(t, exists)
	assert.Equal(t, association, got)
	assert.Equal(t, wasDirty, svc.state.IsDirty())
	mockClient.AssertExpectations(t)
}

func TestForgetConfirmedMissingEditionWithAlternateClient(t *testing.T) {
	svc, mockClient := createTestService()
	bookID := "association-alternate-client"
	association := state.Association{
		ABSItemID: bookID, SourceASIN: "ASIN-123", HardcoverBookID: "901",
		HardcoverEditionID: "902", ReadingFormat: models.ReadingFormatAudiobook,
	}
	require.NoError(t, svc.state.SetAssociation(association))
	client := &associationLookupClient{
		MockHardcoverClient: mockClient,
		freshEditionErr:     fmt.Errorf("%w: 902", models.ErrEditionNotFound),
	}
	svc.hardcover = client

	removed := svc.forgetConfirmedMissingEdition(context.Background(), bookID, association.HardcoverEditionID)

	assert.True(t, removed)
	assert.Equal(t, 1, client.freshEditionLookup)
	_, exists := svc.state.GetAssociation(bookID)
	assert.False(t, exists)
	mockClient.AssertExpectations(t)
}

func TestProcessBookOnlyInvalidatesAssociationAfterFreshEditionAbsence(t *testing.T) {
	t.Run("fresh lookup confirms missing edition", func(t *testing.T) {
		svc, mockClient := createTestService()
		svc.config.Sync.SyncOwned = false
		book := associationTestBook("association-deleted", "ASIN-123", "")
		book.Progress.CurrentTime = book.Media.Duration / 2
		require.NoError(t, svc.state.SetAssociation(state.Association{
			ABSItemID: book.ID, SourceASIN: "ASIN-123", HardcoverBookID: "901",
			HardcoverEditionID: "902", ReadingFormat: models.ReadingFormatAudiobook,
		}))
		svc.state.UpdateBook(book.ID+":902", 0.25, "IN_PROGRESS")
		mockClient.On("GetEdition", mock.Anything, "902").Return(nil, fmt.Errorf("%w: 902", models.ErrEditionNotFound)).Once()
		client := &associationLookupClient{
			MockHardcoverClient: mockClient,
			freshEditionErr:     fmt.Errorf("%w: 902", models.ErrEditionNotFound),
		}
		svc.hardcover = client

		err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

		require.ErrorIs(t, err, models.ErrEditionNotFound)
		assert.Equal(t, 1, client.freshEditionLookup)
		_, exists := svc.state.GetAssociation(book.ID)
		assert.False(t, exists)
		_, exists = svc.state.GetBookState(book.ID + ":902")
		assert.False(t, exists)
		assert.Equal(t, OutcomeFailed, recordedOutcome(svc, book.ID).Outcome)
		mockClient.AssertExpectations(t)
	})

	t.Run("fresh lookup still finds edition", func(t *testing.T) {
		svc, mockClient := createTestService()
		svc.config.Sync.SyncOwned = false
		book := associationTestBook("association-still-there", "ASIN-123", "")
		book.Progress.CurrentTime = book.Media.Duration / 2
		association := state.Association{
			ABSItemID: book.ID, SourceASIN: "ASIN-123", HardcoverBookID: "901",
			HardcoverEditionID: "902", ReadingFormat: models.ReadingFormatAudiobook,
		}
		require.NoError(t, svc.state.SetAssociation(association))
		mockClient.On("GetEdition", mock.Anything, "902").Return(nil, fmt.Errorf("%w: 902", models.ErrEditionNotFound)).Once()
		client := &associationLookupClient{
			MockHardcoverClient: mockClient,
			freshEdition:        &models.Edition{ID: "902", BookID: "901"},
		}
		svc.hardcover = client

		err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

		require.ErrorIs(t, err, models.ErrEditionNotFound)
		assert.Equal(t, 1, client.freshEditionLookup)
		got, exists := svc.state.GetAssociation(book.ID)
		require.True(t, exists)
		assert.Equal(t, association.HardcoverEditionID, got.HardcoverEditionID)
		mockClient.AssertExpectations(t)
	})

	t.Run("unrelated user book lookup failure does not trigger fresh edition check", func(t *testing.T) {
		svc, mockClient := createTestService()
		svc.config.Sync.SyncOwned = false
		book := associationTestBook("association-userbook-error", "ASIN-123", "")
		book.Progress.CurrentTime = book.Media.Duration / 2
		association := state.Association{
			ABSItemID: book.ID, SourceASIN: "ASIN-123", HardcoverBookID: "901",
			HardcoverEditionID: "902", ReadingFormat: models.ReadingFormatAudiobook,
		}
		require.NoError(t, svc.state.SetAssociation(association))
		mockClient.On("GetEdition", mock.Anything, "902").Return(&models.Edition{ID: "902", BookID: "901"}, nil).Once()
		mockClient.On("GetUserBookID", mock.Anything, 902).Return(0, errors.New("user book lookup unavailable")).Once()
		client := &associationLookupClient{MockHardcoverClient: mockClient}
		svc.hardcover = client

		err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

		require.Error(t, err)
		assert.Equal(t, 0, client.freshEditionLookup)
		_, exists := svc.state.GetAssociation(book.ID)
		assert.True(t, exists)
		mockClient.AssertExpectations(t)
	})
}

func TestProcessBookChecksSavedAssociationAfterCreateUserBookFailure(t *testing.T) {
	tests := []struct {
		name            string
		freshEditionErr error
		wantAssociation bool
		wantCheckpoint  bool
	}{
		{
			name:            "fresh lookup confirms edition is absent",
			freshEditionErr: fmt.Errorf("%w: 902", models.ErrEditionNotFound),
		},
		{
			name:            "fresh lookup transport error preserves retry state",
			freshEditionErr: errors.New("network timeout"),
			wantAssociation: true,
			wantCheckpoint:  true,
		},
		{
			name:            "fresh lookup permission error preserves retry state",
			freshEditionErr: errors.New("permission denied"),
			wantAssociation: true,
			wantCheckpoint:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockClient := createTestService()
			svc.config.Sync.SyncOwned = false
			book := associationTestBook("association-create-failure", "ASIN-123", "")
			book.Progress.CurrentTime = book.Media.Duration / 2
			association := state.Association{
				ABSItemID: book.ID, SourceASIN: "ASIN-123", HardcoverBookID: "901",
				HardcoverEditionID: "902", ReadingFormat: models.ReadingFormatAudiobook,
			}
			require.NoError(t, svc.state.SetAssociation(association))
			svc.state.UpdateBook(book.ID+":902", 0.25, "IN_PROGRESS")
			svc.findExistingUserBookForBookFunc = func(context.Context, int64) (int64, error) {
				return 0, nil
			}
			mockClient.On("GetEdition", mock.Anything, "902").Return(&models.Edition{
				ID: "902", BookID: "901",
			}, nil).Once()
			mockClient.On("GetUserBookID", mock.Anything, 902).Return(0, nil).Twice()
			mockClient.On("CreateUserBook", mock.Anything, "902", "IN_PROGRESS").Return("", errors.New("create failed")).Once()
			client := &associationLookupClient{
				MockHardcoverClient: mockClient,
				freshEditionErr:     tt.freshEditionErr,
			}
			svc.hardcover = client

			err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

			require.Error(t, err)
			assert.Equal(t, 1, client.freshEditionLookup)
			_, associationExists := svc.state.GetAssociation(book.ID)
			assert.Equal(t, tt.wantAssociation, associationExists)
			_, checkpointExists := svc.state.GetBookState(book.ID + ":902")
			assert.Equal(t, tt.wantCheckpoint, checkpointExists)
			assert.Equal(t, OutcomeFailed, recordedOutcome(svc, book.ID).Outcome)
			mockClient.AssertExpectations(t)
		})
	}
}

func TestProcessBookChecksSavedAssociationAfterReadInsertFailure(t *testing.T) {
	tests := []struct {
		name            string
		finished        bool
		freshEdition    *models.Edition
		freshEditionErr error
		wantAssociation bool
		wantCheckpoint  bool
	}{
		{
			name:            "fresh lookup confirms edition is absent",
			freshEditionErr: fmt.Errorf("%w: 902", models.ErrEditionNotFound),
		},
		{
			name:            "fresh lookup transport error preserves retry state",
			freshEditionErr: errors.New("network timeout"),
			wantAssociation: true,
			wantCheckpoint:  true,
		},
		{
			name:            "finished read insert also confirms saved edition",
			finished:        true,
			freshEditionErr: fmt.Errorf("%w: 902", models.ErrEditionNotFound),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockClient := createTestService()
			svc.config.Sync.SyncOwned = false
			book := associationTestBook("association-read-insert-failure", "ASIN-123", "")
			book.Progress.CurrentTime = book.Media.Duration / 2
			if tt.finished {
				book.Progress.IsFinished = true
				book.Progress.FinishedAt = book.Progress.StartedAt + 60_000
			}
			association := state.Association{
				ABSItemID: book.ID, SourceASIN: "ASIN-123", HardcoverBookID: "901",
				HardcoverEditionID: "902", ReadingFormat: models.ReadingFormatAudiobook,
			}
			require.NoError(t, svc.state.SetAssociation(association))
			svc.state.UpdateBook(book.ID+":902", 0.25, "IN_PROGRESS")
			svc.findExistingUserBookForBookFunc = func(context.Context, int64) (int64, error) {
				return 9021, nil
			}
			mockClient.On("GetEdition", mock.Anything, "902").Return(&models.Edition{
				ID: "902", BookID: "901",
			}, nil).Once()
			mockClient.On("GetUserBook", mock.Anything, "9021").Return(&models.HardcoverBook{
				ID: "901", EditionID: "902",
			}, nil).Maybe()
			mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
				UserBookID: 9021,
			}).Return([]hardcover.UserBookRead{}, nil).Once()
			mockClient.On("InsertUserBookRead", mock.Anything, mock.MatchedBy(func(input hardcover.InsertUserBookReadInput) bool {
				if input.UserBookID != 9021 {
					return false
				}
				if tt.finished {
					return input.DatesRead.EditionID == nil
				}
				return input.DatesRead.EditionID != nil && *input.DatesRead.EditionID == 902
			})).Return(0, errors.New("read mutation failed")).Once()
			client := &associationLookupClient{
				MockHardcoverClient: mockClient,
				freshEdition:        tt.freshEdition,
				freshEditionErr:     tt.freshEditionErr,
			}
			svc.hardcover = client

			err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

			require.Error(t, err)
			assert.Equal(t, 1, client.freshEditionLookup)
			_, associationExists := svc.state.GetAssociation(book.ID)
			assert.Equal(t, tt.wantAssociation, associationExists)
			_, checkpointExists := svc.state.GetBookState(book.ID + ":902")
			assert.Equal(t, tt.wantCheckpoint, checkpointExists)
			assert.Equal(t, OutcomeFailed, recordedOutcome(svc, book.ID).Outcome)
			mockClient.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
			mockClient.AssertExpectations(t)
		})
	}
}

func TestProcessBookStopsWhenSavedEditionCorrectionFails(t *testing.T) {
	svc, mockClient := createTestService()
	svc.config.Sync.SyncOwned = false
	book := associationTestBook("association-edition-correction-failure", "ASIN-123", "")
	book.Progress.CurrentTime = book.Media.Duration / 2
	association := state.Association{
		ABSItemID: book.ID, SourceASIN: "ASIN-123", HardcoverBookID: "901",
		HardcoverEditionID: "902", ReadingFormat: models.ReadingFormatAudiobook,
	}
	require.NoError(t, svc.state.SetAssociation(association))
	svc.state.UpdateBook(book.ID+":902", 0.25, "IN_PROGRESS")
	svc.findExistingUserBookForBookFunc = func(context.Context, int64) (int64, error) {
		return 9021, nil
	}
	mockClient.On("GetEdition", mock.Anything, "902").Return(&models.Edition{
		ID: "902", BookID: "901",
	}, nil).Once()
	mockClient.On("GetUserBook", mock.Anything, "9021").Return(&models.HardcoverBook{
		ID: "901", EditionID: "903",
	}, nil).Once()
	mockClient.On("UpdateUserBookEdition", mock.Anything, 9021, 902).Return(errors.New("permission denied")).Once()
	client := &associationLookupClient{
		MockHardcoverClient: mockClient,
		freshEdition:        &models.Edition{ID: "902", BookID: "901"},
	}
	svc.hardcover = client

	err := svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

	require.Error(t, err)
	assert.Equal(t, 1, client.freshEditionLookup)
	_, associationExists := svc.state.GetAssociation(book.ID)
	assert.True(t, associationExists)
	_, checkpointExists := svc.state.GetBookState(book.ID + ":902")
	assert.True(t, checkpointExists)
	assert.Equal(t, OutcomeFailed, recordedOutcome(svc, book.ID).Outcome)
	mockClient.AssertNotCalled(t, "GetUserBookReads", mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "InsertUserBookRead", mock.Anything, mock.Anything)
	mockClient.AssertExpectations(t)
}

func TestSyncRefusesStateAlreadyLockedByAnotherProcess(t *testing.T) {
	svc, client := createTestService()
	svc.statePath = t.TempDir() + "/sync_state.json"
	lock, err := state.AcquireFileLock(svc.statePath)
	require.NoError(t, err)
	defer func() { require.NoError(t, lock.Close()) }()

	err = svc.Sync(context.Background())

	assert.ErrorIs(t, err, state.ErrStateFileLocked)
	client.AssertNotCalled(t, "ClearUserBookCache")
}

func TestFindBookInHardcoverEditionASINOnlyAudiobookIsNotMatched(t *testing.T) {
	svc, mockClient := createTestService()
	book := associationTestBook("asin-only-audiobook", "B0AUDIO001", "")
	// No regional Audible mapping is a reviewable import opportunity. A direct
	// editions.asin/title candidate must not supply an unverified target book.
	lookupClient := &associationLookupClient{MockHardcoverClient: mockClient}
	svc.hardcover = lookupClient

	got, err := svc.findBookInHardcover(hardcover.WithReadingFormat(context.Background(), models.ReadingFormatAudiobook), book)

	require.Nil(t, got)
	require.ErrorIs(t, err, errAudibleImportAvailable)
	assert.Equal(t, 1, lookupClient.searchCount)
	mockClient.AssertNotCalled(t, "SearchBooks", mock.Anything, mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
	_, saved := svc.state.GetAssociation(book.ID)
	assert.False(t, saved)
}

func TestFindBookInHardcoverMatchesAudiobookISBNWithoutValidASIN(t *testing.T) {
	tests := []struct {
		name string
		asin string
	}{
		{name: "missing ASIN"},
		{name: "malformed ASIN", asin: "not-a-valid-ASIN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockClient := createTestService()
			svc.config.Sync.SyncOwned = false
			book := associationTestBook("audiobook-isbn-"+tt.name, tt.asin, "978-0-306-40615-7")
			expectUserBook(mockClient)
			searchContext := mock.MatchedBy(func(ctx context.Context) bool {
				format, _ := models.ReadingFormatFromContext(ctx)
				return format == models.ReadingFormatAudiobook
			})
			mockClient.On("SearchBookByISBN13", searchContext, "9780306406157").
				Return(hardcoverHit(), nil).Once()

			got, err := svc.findBookInHardcover(hardcover.WithReadingFormat(context.Background(), models.ReadingFormatAudiobook), book)

			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, "901", got.ID)
			assert.Equal(t, "902", got.EditionID)
			_, saved := svc.state.GetAssociation(book.ID)
			assert.False(t, saved, "automatic audiobook ISBN matches must remain ephemeral")
			mockClient.AssertNotCalled(t, "SearchBookByASIN", mock.Anything, mock.Anything)
			mockClient.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
			mockClient.AssertExpectations(t)
		})
	}
}

func TestFindBookInHardcoverValidAudiobookASINBlocksISBNFallback(t *testing.T) {
	lookupErr := errors.New("temporary ASIN lookup failure")
	tests := []struct {
		name      string
		searchErr error
	}{
		{name: "ASIN miss"},
		{name: "ASIN error", searchErr: lookupErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockClient := createTestService()
			book := associationTestBook("audiobook-valid-asin-"+tt.name, "b0audio001", "978-0-306-40615-7")
			lookupClient := &associationLookupClient{MockHardcoverClient: mockClient, searchErr: tt.searchErr}
			svc.hardcover = lookupClient

			_, err := svc.findBookInHardcover(hardcover.WithReadingFormat(context.Background(), models.ReadingFormatAudiobook), book)

			require.Error(t, err)
			assert.Equal(t, 1, lookupClient.searchCount)
			if tt.searchErr != nil {
				assert.ErrorIs(t, err, errHardcoverLookupFailed)
				assert.ErrorIs(t, err, lookupErr)
				assert.NotErrorIs(t, err, errAudibleImportAvailable)
			} else {
				assert.ErrorIs(t, err, errAudibleImportAvailable)
			}
			mockClient.AssertNotCalled(t, "SearchBooks", mock.Anything, mock.Anything, mock.Anything)
			mockClient.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
			mockClient.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
		})
	}
}

func TestFindBookInHardcoverEbookEditionASINWinsOverConflictingISBN(t *testing.T) {
	svc, mockClient := createTestService()
	svc.config.Sync.SyncOwned = false
	book := associationTestBook("ebook-asin-before-isbn", "ASIN-123", "9780306406157")
	book.MediaType = "ebook"
	svc.hardcover = &associationLookupClient{
		MockHardcoverClient: mockClient,
		result: &hardcover.ASINLookupResult{
			Book:      &models.HardcoverBook{ID: "901", EditionID: "902"},
			MatchKind: hardcover.ASINMatchEditionASIN,
		},
	}
	expectASINEditionRead(t, mockClient, "901", "902")

	got, err := svc.findBookInHardcover(hardcover.WithReadingFormat(context.Background(), models.ReadingFormatEbook), book)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "902", got.EditionID)
	mockClient.AssertNotCalled(t, "SearchBookByISBN13", mock.Anything, mock.Anything)
	mockClient.AssertNotCalled(t, "SearchBookByISBN10", mock.Anything, mock.Anything)
}
