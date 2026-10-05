package sync

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	statepkg "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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
