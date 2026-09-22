package models

import (
	"encoding/json"
	"strings"
)

// AudiobookshelfMetadata represents the metadata for an Audiobookshelf book
type AudiobookshelfMetadataStruct struct {
	Title             string                 `json:"title"`
	TitleIgnorePrefix string                 `json:"titleIgnorePrefix"`
	Subtitle          string                 `json:"subtitle"`
	AuthorName        string                 `json:"authorName"`
	AuthorNameLF      string                 `json:"authorNameLF"`
	NarratorName      string                 `json:"narratorName"`
	Authors           []AudiobookshelfPerson `json:"authors"`
	Narrators         []string               `json:"narrators"`
	SeriesName        string                 `json:"seriesName"`
	Series            []AudiobookshelfSeries `json:"series"`
	Genres            []string               `json:"genres"`
	PublishedYear     string                 `json:"publishedYear"`
	// PublishedDate is a free-form full publication date, tried before
	// PublishedYear when deriving a release date.
	PublishedDate string `json:"publishedDate"`
	Publisher     string `json:"publisher"`
	Description   string `json:"description"`
	ISBN          string `json:"isbn"`
	ASIN          string `json:"asin"`
	Language      string `json:"language"`
	// Abridged is true when Audiobookshelf marks the audiobook as abridged.
	Abridged bool `json:"abridged"`
}

// AudiobookshelfPerson is a named author entry in expanded Audiobookshelf
// metadata.
type AudiobookshelfPerson struct {
	Name string `json:"name"`
}

// AudiobookshelfSeries describes a book's membership and position in a series.
type AudiobookshelfSeries struct {
	Name     string `json:"name"`
	Sequence string `json:"sequence"`
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
		ID        string                       `json:"id"`
		Metadata  AudiobookshelfMetadataStruct `json:"metadata"`
		CoverPath string                       `json:"coverPath"`
		Duration  float64                      `json:"duration"`
		// NumTracks counts the audio files not marked excluded, and is present in
		// both minified and expanded items. EbookFile is set (non-nil) for an ebook
		// and EbookFormat names its format. IsEbook reads all three.
		NumTracks   int              `json:"numTracks"`
		EbookFile   *json.RawMessage `json:"ebookFile"`
		EbookFormat string           `json:"ebookFormat"`
	} `json:"media"`
	// Progress tracks the user's progress through the book
	Progress struct {
		CurrentTime float64 `json:"currentTime"`
		IsFinished  bool    `json:"isFinished"`
		StartedAt   int64   `json:"startedAt"`
		FinishedAt  int64   `json:"finishedAt"`
	} `json:"progress,omitempty"`
}

// IsEbook reports whether the item is an ebook-only library item. Audiobookshelf
// reports "book" as the media type for audiobooks and ebooks alike, so the media
// content decides: an item with audio is an audiobook even if it also carries an
// ebook file. Audio follows Audiobookshelf's own hasAudioTracks rule (numTracks
// counts the non-excluded audio files, or there is a duration). An ebook is an
// ebookFile object (any object, as in Audiobookshelf's own truthiness check) or
// an ebookFormat. The legacy "ebook" media type is still honored.
func (b *AudiobookshelfBook) IsEbook() bool {
	if strings.EqualFold(strings.TrimSpace(b.MediaType), "ebook") {
		return true
	}
	hasAudio := b.Media.Duration > 0 || b.Media.NumTracks > 0
	hasEbook := b.Media.EbookFile != nil || strings.TrimSpace(b.Media.EbookFormat) != ""
	return hasEbook && !hasAudio
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
