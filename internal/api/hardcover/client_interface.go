package hardcover

import (
	"context"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// HardcoverClientInterface defines the interface for interacting with the Hardcover API
type HardcoverClientInterface interface {
	// GetAuthHeader returns the authentication header value
	GetAuthHeader() string

	// SearchPublishers searches for publishers by name in the Hardcover database
	SearchPublishers(ctx context.Context, name string, limit int) ([]models.Publisher, error)

	// SearchAuthors searches for authors by name in the Hardcover database
	SearchAuthors(ctx context.Context, name string, limit int) ([]models.Author, error)

	// SearchNarrators searches for narrators by name in the Hardcover database
	SearchNarrators(ctx context.Context, name string, limit int) ([]models.Author, error)

	// GetEdition retrieves an edition by ID
	GetEdition(ctx context.Context, editionID string) (*models.Edition, error)

	// CheckBookOwnership checks if a book is in the user's "Owned" list
	CheckBookOwnership(ctx context.Context, editionID int) (bool, error)

	// MarkEditionAsOwned adds a book to the user's "Owned" list
	MarkEditionAsOwned(ctx context.Context, editionID int) error

	// GetUserBookID retrieves the user book ID for a given edition ID
	GetUserBookID(ctx context.Context, editionID int) (int, error)

	// SearchBookByISBN13 searches for a book by ISBN-13
	SearchBookByISBN13(ctx context.Context, isbn13 string) (*models.HardcoverBook, error)

	// SearchBookByASIN searches for a book by ASIN
	SearchBookByASIN(ctx context.Context, asin string) (*models.HardcoverBook, error)

	// SearchBookByISBN10 searches for a book by ISBN-10
	SearchBookByISBN10(ctx context.Context, isbn10 string) (*models.HardcoverBook, error)

	// SearchBooks searches for books by title and author
	SearchBooks(ctx context.Context, title, author string) ([]models.HardcoverBook, error)

	// GetEditionByASIN gets an edition by its ASIN
	GetEditionByASIN(ctx context.Context, asin string) (*models.Edition, error)

	// GetEditionByISBN13 gets an edition by its ISBN-13
	GetEditionByISBN13(ctx context.Context, isbn13 string) (*models.Edition, error)

	// GetGoogleUploadCredentials gets upload credentials for Google Cloud Storage
	GetGoogleUploadCredentials(ctx context.Context, filename string, editionID int) (*edition.GoogleUploadInfo, error)

	// UpdateReadingProgress updates the reading progress for a book
	UpdateReadingProgress(
		ctx context.Context,
		bookID string,
		progress float64,
		status string,
		markAsOwned bool,
	) error

	// GetUserBook gets user book information by ID
	GetUserBook(ctx context.Context, userBookID string) (*models.HardcoverBook, error)

	// SaveToFile saves client state to a file (for mismatch package)
	SaveToFile(filepath string) error

	// AddWithMetadata adds a mismatch with metadata (for mismatch package)
	AddWithMetadata(key string, value interface{}, metadata map[string]interface{}) error

	// GetUserBookReads gets the reading progress for a user book
	GetUserBookReads(ctx context.Context, input GetUserBookReadsInput) ([]UserBookRead, error)

	// InsertUserBookRead creates a new reading progress entry
	InsertUserBookRead(ctx context.Context, input InsertUserBookReadInput) (int, error)

	// UpdateUserBookRead updates an existing reading progress entry
	UpdateUserBookRead(ctx context.Context, input UpdateUserBookReadInput) (bool, error)

	// CheckExistingUserBookRead checks if a reading progress entry exists
	CheckExistingUserBookRead(ctx context.Context, input CheckExistingUserBookReadInput) (*CheckExistingUserBookReadResult, error)

	// UpdateUserBookStatus updates the status of a user book
	UpdateUserBookStatus(ctx context.Context, input UpdateUserBookStatusInput) error

	// CreateUserBook creates a new user book entry
	CreateUserBook(ctx context.Context, editionID, status string) (string, error)
}
