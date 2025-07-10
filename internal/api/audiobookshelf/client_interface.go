package audiobookshelf

import (
	"context"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// AudiobookshelfClientInterface defines the interface for the Audiobookshelf API client
// This allows for mocking in tests
type AudiobookshelfClientInterface interface {
	GetLibraries(ctx context.Context) ([]AudiobookshelfLibrary, error)
	GetLibraryItems(ctx context.Context, libraryID string) ([]models.AudiobookshelfBook, error)
	GetUserProgress(ctx context.Context) (*models.AudiobookshelfUserProgress, error)
	GetListeningSessions(ctx context.Context, since time.Time) ([]models.AudiobookshelfBook, error)
}

// Ensure that the Client implements AudiobookshelfClientInterface
var _ AudiobookshelfClientInterface = (*Client)(nil)
