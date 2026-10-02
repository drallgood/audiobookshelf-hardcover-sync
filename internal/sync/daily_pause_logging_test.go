package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/testutils"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type dailyQuotaPausedHardcoverClient struct {
	*MockHardcoverClient
	paused bool
}

type bookSearchIntentCase struct {
	name                  string
	asin                  string
	isbn                  string
	title                 string
	author                string
	ebook                 bool
	directTitleAuthor     bool
	wantSuppressedIntents int
}

func (c *dailyQuotaPausedHardcoverClient) DailyQuotaPaused() bool {
	return c.paused
}

func TestDebugRequestIntentObservesDailyQuotaPause(t *testing.T) {
	testutils.SetGlobalLogLevel(t, zerolog.DebugLevel)

	tests := []struct {
		name      string
		client    HardcoverSyncClient
		wantEvent bool
	}{
		{
			name:      "unpaused client emits intent",
			client:    &dailyQuotaPausedHardcoverClient{MockHardcoverClient: &MockHardcoverClient{}},
			wantEvent: true,
		},
		{
			name:      "paused client suppresses intent",
			client:    &dailyQuotaPausedHardcoverClient{MockHardcoverClient: &MockHardcoverClient{}, paused: true},
			wantEvent: false,
		},
		{
			name:      "client without pause capability emits intent",
			client:    &MockHardcoverClient{},
			wantEvent: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			log := &logger.Logger{Logger: zerolog.New(&output).Level(zerolog.DebugLevel)}
			service := &Service{hardcover: test.client}

			service.debugRequestIntent(log, "test intent event", map[string]interface{}{"request_marker": "caller-provided-marker"})

			if test.wantEvent {
				require.Contains(t, output.String(), `"message":"test intent event"`)
				require.Contains(t, output.String(), `"request_marker":"caller-provided-marker"`)
			} else {
				require.Empty(t, output.String())
			}
		})
	}
}

func TestBookSearchIntentsObserveDailyQuotaPauseAtLookupBoundary(t *testing.T) {
	testutils.SetGlobalLogLevel(t, zerolog.DebugLevel)

	tests := []bookSearchIntentCase{
		{
			name:                  "ASIN lookup",
			asin:                  "daily-pause-asin",
			wantSuppressedIntents: 1,
		},
		{
			name:                  "ebook ISBN lookup",
			isbn:                  "9791032305690",
			ebook:                 true,
			wantSuppressedIntents: 1,
		},
		{
			name:                  "direct title and author lookup",
			title:                 "Intent Test Book",
			author:                "Test Author",
			directTitleAuthor:     true,
			wantSuppressedIntents: 1,
		},
		{
			name:                  "title and author fallback through book matching",
			title:                 "Intent Test Book",
			author:                "Test Author",
			wantSuppressedIntents: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			unpausedRecords := runBookSearchIntentFlow(t, test, false)
			pausedRecords := runBookSearchIntentFlow(t, test, true)

			require.Equal(t, test.wantSuppressedIntents, unpausedRecords-pausedRecords,
				"the same lookup should emit one record for each request intent suppressed by the daily pause")
		})
	}
}

func runBookSearchIntentFlow(t *testing.T, test bookSearchIntentCase, paused bool) int {
	t.Helper()

	svc, mockClient := createTestService()
	svc.hardcover = &dailyQuotaPausedHardcoverClient{MockHardcoverClient: mockClient, paused: paused}
	var output bytes.Buffer
	svc.log = &logger.Logger{Logger: zerolog.New(&output).Level(zerolog.DebugLevel)}

	book := *toAudiobookshelfBook(createTestBook("daily-pause-search", test.title, test.author, test.asin, test.isbn))
	if test.ebook {
		book.MediaType = "ebook"
	}
	ctx := context.Background()
	if test.ebook {
		ctx = models.WithReadingFormat(ctx, models.ReadingFormatEbook)
	}

	switch {
	case test.asin != "":
		mockClient.On("SearchBookByASIN", mock.Anything, test.asin).
			Return((*models.HardcoverBook)(nil), nil).Once()
	case test.isbn != "":
		mockClient.On("SearchBookByISBN13", mock.Anything, test.isbn).
			Return((*models.HardcoverBook)(nil), nil).Once()
	}
	if test.title != "" && test.author != "" {
		mockClient.On("SearchBooks", mock.Anything, test.title+" "+test.author, "").
			Return([]models.HardcoverBook{}, nil).Once()
	}

	var err error
	if test.directTitleAuthor {
		_, err = svc.findBookInHardcoverByTitleAuthor(ctx, book)
	} else {
		_, err = svc.findBookInHardcover(ctx, book)
	}
	require.Error(t, err)
	mockClient.AssertExpectations(t)

	if output.Len() == 0 {
		return 0
	}
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	for _, line := range lines {
		var event map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(line, &event), "lookup should emit structured JSON log records")
	}
	return len(lines)
}
