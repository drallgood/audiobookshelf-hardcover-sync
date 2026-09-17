package sync

import (
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/stretchr/testify/require"
)

func TestServiceMismatchRecordsUseOwnedCollector(t *testing.T) {
	svc, _ := createTestService()
	svc.addMismatch(mismatch.BookMismatch{BookID: "run-local"})

	runRecords := svc.mismatchCollector.GetAll()
	require.Len(t, runRecords, 1)
	require.Equal(t, "run-local", runRecords[0].BookID)
}
