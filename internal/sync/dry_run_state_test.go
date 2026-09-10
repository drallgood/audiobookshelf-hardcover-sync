package sync

import (
	"context"
	"strconv"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestHandleFinishedBookDryRunDoesNotAdvanceState(t *testing.T) {
	svc, mockClient := createTestService()
	svc.config.Sync.DryRun = true
	svc.hardcover = &dryRunHardcoverClient{HardcoverClientInterface: mockClient}

	book := createTestFinishedBook("dry-run-finished", "Dry Run Finished", "Test Author", "DRYRUN-FINISHED", "")
	modelBook := convertTestBookToModel(book)
	userBookID := int64(123)
	stateKey := modelBook.ID + ":456"

	mockClient.On("GetUserBook", mock.Anything, strconv.FormatInt(userBookID, 10)).Return(&models.HardcoverBook{
		ID:           "book-123",
		UserBookID:   strconv.FormatInt(userBookID, 10),
		BookStatusID: 2,
	}, nil).Once()
	mockClient.On("GetUserBookReads", mock.Anything, hardcover.GetUserBookReadsInput{
		UserBookID: userBookID,
	}).Return([]hardcover.UserBookRead{}, nil).Twice()

	require.NoError(t, svc.HandleFinishedBook(context.Background(), modelBook, "456", userBookID))

	_, exists := svc.state.GetBookState(stateKey)
	assert.False(t, exists, "dry-run must not mark a skipped finished mutation as applied")
	assert.True(t, svc.state.NeedsSync(stateKey, 1.0, "FINISHED", 0.01), "a later real incremental run must still process the book")
	mockClient.AssertExpectations(t)
}

func TestProcessWantToReadDryRunDoesNotAdvanceState(t *testing.T) {
	svc, mockClient := createTestService()
	svc.config.Sync.DryRun = true
	svc.config.Sync.ProcessUnreadBooks = true
	svc.config.Sync.SyncOwned = false
	svc.hardcover = &dryRunHardcoverClient{HardcoverClientInterface: mockClient}

	book := toAudiobookshelfBook(createTestBook("dry-run-want-to-read", "Dry Run Want To Read", "Test Author", "DRYRUN-WANT", ""))
	stateKey := book.ID + ":456"
	mockClient.On("SearchBookByASIN", mock.Anything, "DRYRUN-WANT").Return(&models.HardcoverBook{
		ID:        "123",
		EditionID: "456",
	}, nil).Once()
	mockClient.On("GetEdition", mock.Anything, "456").Return(&models.Edition{
		ID:     "456",
		BookID: "123",
	}, nil).Times(3)
	mockClient.On("GetUserBookID", mock.Anything, 456).Return(789, nil).Times(3)

	require.NoError(t, svc.processBook(context.Background(), *book, &models.AudiobookshelfUserProgress{}))

	_, exists := svc.state.GetBookState(stateKey)
	assert.False(t, exists, "dry-run must not mark skipped WANT_TO_READ mutation as applied")
	assert.True(t, svc.state.NeedsSync(stateKey, 0.0, "WANT_TO_READ", 0.01), "a later real incremental run must still process the book")
	mockClient.AssertNotCalled(t, "UpdateUserBookStatus", mock.Anything, mock.Anything)
	mockClient.AssertExpectations(t)
}
