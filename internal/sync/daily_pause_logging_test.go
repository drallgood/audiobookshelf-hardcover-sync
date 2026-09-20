package sync

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/testutils"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/mock"
)

type dailyPauseMockClient struct {
	*MockHardcoverClient
	paused bool
}

func (c *dailyPauseMockClient) DailyQuotaPaused() bool { return c.paused }

func TestHardcoverSearchIntentLogsOnlyOutsideDailyPause(t *testing.T) {
	testutils.SetGlobalLogLevel(t, zerolog.DebugLevel)

	for _, tc := range []struct {
		name    string
		message string
		setup   func(*MockHardcoverClient, *models.AudiobookshelfBook)
		run     func(*Service, models.AudiobookshelfBook)
	}{
		{
			name:    "ASIN",
			message: "Searching for book by ASIN: B000TEST",
			setup: func(client *MockHardcoverClient, book *models.AudiobookshelfBook) {
				book.Media.Metadata.ASIN = "B000TEST"
				client.On("SearchBookByASIN", mock.Anything, "B000TEST").Return((*models.HardcoverBook)(nil), nil)
			},
			run: func(service *Service, book models.AudiobookshelfBook) {
				_, _ = service.findBookInHardcover(context.Background(), book)
			},
		},
		{
			name:    "ISBN",
			message: "Searching for book by ISBN: 9781101926840",
			setup: func(client *MockHardcoverClient, book *models.AudiobookshelfBook) {
				book.Media.Metadata.ISBN = "9781101926840"
				client.On("SearchBookByISBN13", mock.Anything, "9781101926840").Return((*models.HardcoverBook)(nil), nil)
				client.On("SearchBookByISBN10", mock.Anything, "9781101926840").Return((*models.HardcoverBook)(nil), nil)
			},
			run: func(service *Service, book models.AudiobookshelfBook) {
				_, _ = service.findBookInHardcover(context.Background(), book)
			},
		},
		{
			name:    "title and author",
			message: "Searching for book by title and author",
			setup: func(client *MockHardcoverClient, book *models.AudiobookshelfBook) {
				book.Media.Metadata.Title = "Test Book"
				book.Media.Metadata.AuthorName = "Test Author"
				client.On("SearchBooks", mock.Anything, "Test Book Test Author", "").Return([]models.HardcoverBook{}, nil)
			},
			run: func(service *Service, book models.AudiobookshelfBook) {
				_, _ = service.findBookInHardcoverByTitleAuthor(context.Background(), book)
			},
		},
	} {
		for _, paused := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/normal", true: "/daily_pause"}[paused], func(t *testing.T) {
				var logs bytes.Buffer
				log := &logger.Logger{Logger: zerolog.New(&logs).Level(zerolog.DebugLevel)}
				mockClient := new(MockHardcoverClient)
				client := &dailyPauseMockClient{MockHardcoverClient: mockClient, paused: paused}
				book := models.AudiobookshelfBook{}
				tc.setup(mockClient, &book)
				service := &Service{
					hardcover:       client,
					log:             log,
					config:          config.DefaultConfig(),
					asinCache:       make(map[string]*models.HardcoverBook),
					persistentCache: NewPersistentASINCache(t.TempDir()),
				}
				tc.run(service, book)
				mockClient.AssertExpectations(t)
				if got := strings.Contains(logs.String(), tc.message); got != !paused {
					t.Errorf("request-intent log present = %t, daily pause = %t; logs: %s", got, paused, logs.String())
				}
			})
		}
	}
}
