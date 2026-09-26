package sync

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
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
	client.On("GetEdition", mock.Anything, editionID).Return(&models.Edition{
		ID: editionID, BookID: bookID,
	}, nil).Once()
	client.On("GetUserBookID", mock.Anything, editionInt).Return(9021, nil).Once()
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

func TestFindBookInHardcoverPersistsOnlyVerifiedAudiobookMapping(t *testing.T) {
	tests := []struct {
		name       string
		format     string
		matchKind  hardcover.ASINMatchKind
		wantSaved  bool
		wantRegion string
	}{
		{name: "verified audiobook mapping", format: models.ReadingFormatAudiobook, matchKind: hardcover.ASINMatchAudibleMapping, wantSaved: true, wantRegion: "ASIN-123:uk"},
		{name: "temporary edition ASIN fallback", format: models.ReadingFormatAudiobook, matchKind: hardcover.ASINMatchEditionASIN},
		{name: "ebook edition ASIN", format: models.ReadingFormatEbook, matchKind: hardcover.ASINMatchEditionASIN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mockClient := createTestService()
			svc.config.Sync.SyncOwned = false
			book := associationTestBook("association-"+tt.name, "ASIN-123", "")
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
				assert.Equal(t, "ASIN-123", association.SourceASIN)
				assert.Equal(t, string(hardcover.ASINMatchAudibleMapping), association.Provenance)
			} else {
				assert.False(t, svc.state.IsDirty())
			}
			mockClient.AssertExpectations(t)
		})
	}
}

func TestProcessBookDoesNotRepeatTemporaryASINFallbackLookup(t *testing.T) {
	svc, mockClient := createTestService()
	svc.config.Sync.SyncOwned = false
	svc.config.Sync.SyncWantToRead = false
	book := associationTestBook("association-temporary-process", "ASIN-123", "")
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
	mockClient.On("GetEdition", mock.Anything, "902").Return(&models.Edition{ID: "902", BookID: "901"}, nil).Once()
	mockClient.On("GetUserBookID", mock.Anything, editionInt).Return(9021, nil).Once()

	err = svc.processBook(context.Background(), book, &models.AudiobookshelfUserProgress{})

	require.NoError(t, err)
	assert.Equal(t, 1, client.searchCount, "one item should make one external ASIN lookup even when the result is not persistable")
	_, saved := svc.state.GetAssociation(book.ID)
	assert.False(t, saved, "edition-ASIN fallback remains temporary")
	mockClient.AssertExpectations(t)
}

func TestFindBookInHardcoverIdentifierChangeInvalidatesAndRematches(t *testing.T) {
	svc, mockClient := createTestService()
	svc.config.Sync.SyncOwned = false
	book := associationTestBook("association-changed", "ASIN-NEW", "")
	require.NoError(t, svc.state.SetAssociation(state.Association{
		ABSItemID: book.ID, SourceASIN: "ASIN-OLD", HardcoverBookID: "old-book",
		HardcoverEditionID: "old-edition", ReadingFormat: models.ReadingFormatAudiobook,
	}))
	client := &associationLookupClient{
		MockHardcoverClient: mockClient,
		result: &hardcover.ASINLookupResult{
			Book:               &models.HardcoverBook{ID: "901", EditionID: "902"},
			MatchKind:          hardcover.ASINMatchAudibleMapping,
			RegionalExternalID: "ASIN-NEW:us",
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
	assert.Equal(t, "ASIN-NEW", association.SourceASIN)
	assert.Equal(t, "901", association.HardcoverBookID)
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
