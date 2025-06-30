package models

// AudiobookshelfMetadata represents the metadata for an Audiobookshelf book
type AudiobookshelfMetadataStruct struct {
	Title             string   `json:"title"`
	TitleIgnorePrefix string   `json:"titleIgnorePrefix"`
	Subtitle          string   `json:"subtitle"`
	AuthorName        string   `json:"authorName"`
	AuthorNameLF      string   `json:"authorNameLF"`
	NarratorName      string   `json:"narratorName"`
	SeriesName        string   `json:"seriesName"`
	Genres            []string `json:"genres"`
	PublishedYear     string   `json:"publishedYear"`
	Publisher         string   `json:"publisher"`
	Description       string   `json:"description"`
	ISBN              string   `json:"isbn"`
	ASIN              string   `json:"asin"`
	Language          string   `json:"language"`
}

// GetTitle returns the book's title
func (m *AudiobookshelfMetadataStruct) GetTitle() string {
	return m.Title
}

// GetAuthorName returns the primary author's name
func (m *AudiobookshelfMetadataStruct) GetAuthorName() string {
	return m.AuthorName
}

// GetASIN returns the book's ASIN (Amazon Standard Identification Number)
func (m *AudiobookshelfMetadataStruct) GetASIN() string {
	return m.ASIN
}

// GetISBN returns the book's ISBN (International Standard Book Number)
func (m *AudiobookshelfMetadataStruct) GetISBN() string {
	return m.ISBN
}

// AudiobookshelfBook represents a book from the Audiobookshelf API
type AudiobookshelfBook struct {
	ID        string `json:"id"`
	LibraryID string `json:"libraryId"`
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
	Media     struct {
		ID       string                      `json:"id"`
		Metadata AudiobookshelfMetadataStruct `json:"metadata"`
		CoverPath string                     `json:"coverPath"`
		Duration  float64                    `json:"duration"`
	} `json:"media"`
	// Progress tracks the user's progress through the book
	Progress struct {
		CurrentTime float64 `json:"currentTime"`
		IsFinished  bool    `json:"isFinished"`
		StartedAt   int64   `json:"startedAt"`
		FinishedAt  int64   `json:"finishedAt"`
	} `json:"progress,omitempty"`
}

// GetID returns the book's unique identifier
func (b *AudiobookshelfBook) GetID() string {
	return b.ID
}

// GetLibraryID returns the ID of the library this book belongs to
func (b *AudiobookshelfBook) GetLibraryID() string {
	return b.LibraryID
}

// GetPath returns the file system path of the book
func (b *AudiobookshelfBook) GetPath() string {
	return b.Path
}

// GetMediaType returns the type of media (e.g., "book", "podcast")
func (b *AudiobookshelfBook) GetMediaType() string {
	return b.MediaType
}

// GetMediaID returns the ID of the media item
func (b *AudiobookshelfBook) GetMediaID() string {
	return b.Media.ID
}

// GetMediaMetadata returns the metadata for the media
func (b *AudiobookshelfBook) GetMediaMetadata() AudiobookshelfMetadata {
	return &b.Media.Metadata
}

// GetProgress returns the progress information for the book
func (b *AudiobookshelfBook) GetProgress() *AudiobookshelfProgress {
	return &AudiobookshelfProgress{
		CurrentTime: b.Progress.CurrentTime,
		IsFinished:  b.Progress.IsFinished,
		StartedAt:   b.Progress.StartedAt,
		FinishedAt:  b.Progress.FinishedAt,
	}
}

// AudiobookshelfProgress represents the progress of reading a book
type AudiobookshelfProgress struct {
	CurrentTime float64 `json:"currentTime"`
	IsFinished  bool    `json:"isFinished"`
	StartedAt   int64   `json:"startedAt"`
	FinishedAt  int64   `json:"finishedAt"`
}

// AudiobookshelfLibraryResponse represents the response from the Audiobookshelf API
// when fetching library items
type AudiobookshelfLibraryResponse struct {
	Results []AudiobookshelfBook `json:"results"`
	Total   int                  `json:"total"`
	Limit   int                  `json:"limit"`
	Page    int                  `json:"page"`
}

// Ensure AudiobookshelfBook implements AudiobookshelfBookInterface
var _ AudiobookshelfBookInterface = (*AudiobookshelfBook)(nil)

// Ensure the metadata struct implements AudiobookshelfMetadata
var _ AudiobookshelfMetadata = (*AudiobookshelfMetadataStruct)(nil)
