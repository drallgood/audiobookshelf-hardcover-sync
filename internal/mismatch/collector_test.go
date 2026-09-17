package mismatch

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCollectorsRemainIsolatedWhenWrittenConcurrently(t *testing.T) {
	first := NewCollector()
	second := NewCollector()
	const recordsPerCollector = 32

	var wg sync.WaitGroup
	for i := 0; i < recordsPerCollector; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			first.Add(BookMismatch{BookID: fmt.Sprintf("first-%d", i)})
		}(i)
		go func(i int) {
			defer wg.Done()
			second.Add(BookMismatch{BookID: fmt.Sprintf("second-%d", i)})
		}(i)
	}
	wg.Wait()

	firstRecords := first.GetAll()
	secondRecords := second.GetAll()
	require.Len(t, firstRecords, recordsPerCollector)
	require.Len(t, secondRecords, recordsPerCollector)
	for _, record := range firstRecords {
		require.Contains(t, record.BookID, "first-")
	}
	for _, record := range secondRecords {
		require.Contains(t, record.BookID, "second-")
	}
}
