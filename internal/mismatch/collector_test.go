package mismatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/mock"
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

func TestCollectorSaveToFileSerializesSharedDirectory(t *testing.T) {
	outputDir := t.TempDir()
	first := NewCollector()
	second := NewCollector()
	first.Add(BookMismatch{BookID: "1", Title: "First", Author: "First Author"})
	second.Add(BookMismatch{BookID: "2", Title: "Second", Author: "Second Author"})

	client := &MockHardcoverClient{}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{})
	client.On("SearchAuthors", mock.Anything, "First Author", 5).
		Run(func(mock.Arguments) {
			close(firstEntered)
			<-releaseFirst
		}).Return([]models.Author{}, nil).Once()
	client.On("SearchAuthors", mock.Anything, "Second Author", 5).
		Run(func(mock.Arguments) {
			close(secondEntered)
		}).Return([]models.Author{}, nil).Once()

	var wg sync.WaitGroup
	var firstErr, secondErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		firstErr = first.SaveToFile(context.Background(), client, outputDir, nil)
	}()
	select {
	case <-firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first export did not reach the enrichment lookup")
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		secondErr = second.SaveToFile(context.Background(), client, outputDir, nil)
	}()
	select {
	case <-secondEntered:
		t.Fatal("second export entered before the first export completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	wg.Wait()
	require.NoError(t, firstErr)
	require.NoError(t, secondErr)
	client.AssertExpectations(t)

	files, err := os.ReadDir(outputDir)
	require.NoError(t, err)
	require.Len(t, files, 1)
	data, err := os.ReadFile(filepath.Join(outputDir, files[0].Name()))
	require.NoError(t, err)
	var export struct {
		Title string `json:"title"`
	}
	require.NoError(t, json.Unmarshal(data, &export))
	require.Equal(t, "Second", export.Title)
}
