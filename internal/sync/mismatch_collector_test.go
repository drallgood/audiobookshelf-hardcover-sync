package sync

import (
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
	"github.com/stretchr/testify/require"
)

func TestServiceMismatchRecordsUseRunCollector(t *testing.T) {
	mismatch.Clear()
	t.Cleanup(mismatch.Clear)
	mismatch.Add(mismatch.BookMismatch{BookID: "legacy"})

	svc, _ := createTestService()
	svc.mismatchCollector = mismatch.NewCollector()
	svc.addMismatch(mismatch.BookMismatch{BookID: "run-local"})

	runRecords := svc.mismatchCollector.GetAll()
	require.Len(t, runRecords, 1)
	require.Equal(t, "run-local", runRecords[0].BookID)

	globalRecords := mismatch.GetAll()
	require.Len(t, globalRecords, 1)
	require.Equal(t, "legacy", globalRecords[0].BookID)
}
