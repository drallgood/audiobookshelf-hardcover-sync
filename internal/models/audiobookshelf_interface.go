package models

// AudiobookshelfBookInterface defines the interface for accessing Audiobookshelf book data
type AudiobookshelfBookInterface interface {
	// GetID returns the book's unique identifier
	GetID() string
	// GetLibraryID returns the ID of the library this book belongs to
	GetLibraryID() string
	// GetPath returns the file system path of the book
	GetPath() string
	// GetMediaType returns the type of media (e.g., "book", "podcast")
	GetMediaType() string
	// GetMediaID returns the ID of the media item
	GetMediaID() string
	// GetMediaMetadata returns the metadata for the media
	GetMediaMetadata() AudiobookshelfMetadata
	// GetProgress returns the progress information for the book
	GetProgress() *AudiobookshelfProgress
}

// AudiobookshelfMetadata defines the interface for accessing book metadata
type AudiobookshelfMetadata interface {
	// GetTitle returns the book's title
	GetTitle() string
	// GetAuthorName returns the primary author's name
	GetAuthorName() string
	// GetASIN returns the book's ASIN (Amazon Standard Identification Number)
	GetASIN() string
	// GetISBN returns the book's ISBN (International Standard Book Number)
	GetISBN() string
}
