package models

import (
	"bytes"
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
	SeriesName        string                 `json:"seriesName"`
	Series            []AudiobookshelfSeries `json:"series"`
	Genres            []string               `json:"genres"`
	PublishedYear     string                 `json:"publishedYear"`
	Publisher         string                 `json:"publisher"`
	Description       string                 `json:"description"`
	ISBN              string                 `json:"isbn"`
	ASIN              string                 `json:"asin"`
	Language          string                 `json:"language"`
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
	} `json:"media"`
	// content describes the media files, which Audiobookshelf reports in the
	// media payload rather than in mediaType. It is populated by UnmarshalJSON.
	content mediaContent
	// Progress tracks the user's progress through the book
	Progress struct {
		CurrentTime float64 `json:"currentTime"`
		IsFinished  bool    `json:"isFinished"`
		StartedAt   int64   `json:"startedAt"`
		FinishedAt  int64   `json:"finishedAt"`
	} `json:"progress,omitempty"`
}

// mediaContent records which media files an Audiobookshelf item carries.
// Expanded library items list audioFiles and an ebookFile object; minified
// ones report numAudioFiles and ebookFormat instead.
type mediaContent struct {
	hasAudio bool
	hasEbook bool
}

// UnmarshalJSON decodes the item and records its media content, which the
// exported fields do not capture.
func (b *AudiobookshelfBook) UnmarshalJSON(data []byte) error {
	type plain AudiobookshelfBook
	if err := json.Unmarshal(data, (*plain)(b)); err != nil {
		return err
	}
	var raw struct {
		Media struct {
			AudioFiles    []json.RawMessage `json:"audioFiles"`
			NumAudioFiles int               `json:"numAudioFiles"`
			EbookFile     json.RawMessage   `json:"ebookFile"`
			EbookFormat   string            `json:"ebookFormat"`
		} `json:"media"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	ebookFile := bytes.TrimSpace(raw.Media.EbookFile)
	b.content = mediaContent{
		hasAudio: b.Media.Duration > 0 || raw.Media.NumAudioFiles > 0 || len(raw.Media.AudioFiles) > 0,
		hasEbook: (len(ebookFile) > 0 && !bytes.Equal(ebookFile, []byte("null"))) || strings.TrimSpace(raw.Media.EbookFormat) != "",
	}
	return nil
}

// IsEbook reports whether the item is an ebook-only library item. Audiobookshelf
// reports "book" as the media type for audiobooks and ebooks alike, so the media
// content decides: an item with audio is an audiobook even if it also carries
// an ebook file. Items not decoded from JSON fall back to the media type alone.
func (b *AudiobookshelfBook) IsEbook() bool {
	if strings.EqualFold(strings.TrimSpace(b.MediaType), "ebook") {
		return true
	}
	return b.content.hasEbook && !b.content.hasAudio && b.Media.Duration <= 0
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
