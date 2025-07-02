package sync

import (
	"reflect"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

// toHardcoverBook converts a test Hardcover book to a production model
func toHardcoverBook(testBook *TestHardcoverBook) *models.HardcoverBook {
	if testBook == nil {
		return nil
	}

	// Convert authors
	authors := make([]models.Author, len(testBook.Authors))
	for i, author := range testBook.Authors {
		authors[i] = models.Author{
			ID:   author.ID,
			Name: author.Name,
		}
	}

	return &models.HardcoverBook{
		ID:            testBook.ID,
		Title:         testBook.Title,
		Subtitle:      testBook.Subtitle,
		Description:   testBook.Description,
		Authors:       authors,
		CoverImageURL: testBook.CoverImageURL,
		ASIN:          testBook.ASIN,
		ISBN:          testBook.ISBN,
		EditionID:     testBook.EditionID,
	}
}

// toAudiobookshelfBook converts a test Audiobookshelf book to a production model
func toAudiobookshelfBook(testBook *TestAudiobookshelfBook) *models.AudiobookshelfBook {
	if testBook == nil {
		return nil
	}

	// Create metadata
	metadata := models.AudiobookshelfMetadataStruct{
		Title:      testBook.Media.Metadata.GetTitle(),
		AuthorName: testBook.Media.Metadata.GetAuthorName(),
		ASIN:       testBook.Media.Metadata.GetASIN(),
		ISBN:       testBook.Media.Metadata.GetISBN(),
	}

	// Create media with embedded metadata
	var media struct {
		ID        string                          `json:"id"`
		Metadata  models.AudiobookshelfMetadataStruct `json:"metadata"`
		CoverPath string                          `json:"coverPath"`
		Duration  float64                         `json:"duration"`
	}
	media.ID = testBook.Media.ID
	media.Metadata = metadata
	media.CoverPath = testBook.Media.CoverPath
	media.Duration = testBook.Media.Duration

	// Create progress
	progress := models.AudiobookshelfProgress{
		CurrentTime: testBook.Progress.CurrentTime,
		IsFinished:  testBook.Progress.IsFinished,
		StartedAt:   testBook.Progress.StartedAt,
		FinishedAt:  testBook.Progress.FinishedAt,
	}

	// Create the book with embedded media
	book := &models.AudiobookshelfBook{
		ID:        testBook.ID,
		LibraryID: testBook.LibraryID,
		Path:      testBook.Path,
		MediaType: testBook.MediaType,
	}

	// Use reflection to set the unexported media field
	reflect.ValueOf(book).Elem().FieldByName("Media").Set(reflect.ValueOf(media))
	reflect.ValueOf(book).Elem().FieldByName("Progress").Set(reflect.ValueOf(progress))

	return book
}

// TestAudiobookshelfBook is a test implementation of an Audiobookshelf book
type TestAudiobookshelfBook struct {
	ID        string
	LibraryID string
	Path      string
	MediaType string
	Media     struct {
		ID        string
		Metadata  TestAudiobookshelfMetadata
		CoverPath string
		Duration  float64
	}
	Progress struct {
		CurrentTime float64
		IsFinished  bool
		StartedAt   int64
		FinishedAt  int64
	}
}

// GetID returns the book ID
func (b *TestAudiobookshelfBook) GetID() string {
	return b.ID
}

// GetLibraryID returns the library ID
func (b *TestAudiobookshelfBook) GetLibraryID() string {
	return b.LibraryID
}

// GetPath returns the book path
func (b *TestAudiobookshelfBook) GetPath() string {
	return b.Path
}

// GetMediaType returns the media type
func (b *TestAudiobookshelfBook) GetMediaType() string {
	return b.MediaType
}

// GetMediaID returns the media ID
func (b *TestAudiobookshelfBook) GetMediaID() string {
	return b.Media.ID
}

// GetMediaMetadata returns the media metadata
func (b *TestAudiobookshelfBook) GetMediaMetadata() models.AudiobookshelfMetadata {
	return &b.Media.Metadata
}

// GetProgress returns the progress information
func (b *TestAudiobookshelfBook) GetProgress() *models.AudiobookshelfProgress {
	if b == nil {
		return nil
	}
	return &models.AudiobookshelfProgress{
		CurrentTime: b.Progress.CurrentTime,
		IsFinished:  b.Progress.IsFinished,
		StartedAt:   b.Progress.StartedAt,
		FinishedAt:  b.Progress.FinishedAt,
	}
}

// TestAudiobookshelfMetadata represents metadata for a test book
type TestAudiobookshelfMetadata struct {
	Title             string
	TitleIgnorePrefix string
	Subtitle          string
	AuthorName        string
	AuthorNameLF      string
	NarratorName      string
	SeriesName        string
	Genres            []string
	PublishedYear     string
	Publisher         string
	Description       string
	ISBN              string
	ASIN              string
	Language          string
}

// GetTitle returns the book title
func (m *TestAudiobookshelfMetadata) GetTitle() string {
	return m.Title
}

// GetAuthorName returns the author name
func (m *TestAudiobookshelfMetadata) GetAuthorName() string {
	return m.AuthorName
}

// GetASIN returns the ASIN
func (m *TestAudiobookshelfMetadata) GetASIN() string {
	return m.ASIN
}

// GetISBN returns the ISBN
func (m *TestAudiobookshelfMetadata) GetISBN() string {
	return m.ISBN
}

// TestAudiobookshelfProgress represents progress information for a test book
type TestAudiobookshelfProgress struct {
	CurrentTime float64
	IsFinished  bool
	StartedAt   int64
	FinishedAt  int64
}

// TestHardcoverBook is a test implementation of a Hardcover book
type TestHardcoverBook struct {
	ID            string
	UserBookID    string
	EditionID     string
	Title         string
	Subtitle      string
	Authors       []models.Author
	Narrators     []models.Author
	CoverImageURL string
	Description   string
	PageCount     int
	ReleaseDate   string
	Publisher     string
	ISBN          string
	ASIN          string
	BookStatusID  int
	CanonicalID   *int
	EditionASIN   string
	EditionISBN10 string
	EditionISBN13 string
}

// GetID returns the book ID
func (b TestHardcoverBook) GetID() string {
	return b.ID
}

// GetTitle returns the book title
func (b TestHardcoverBook) GetTitle() string {
	return b.Title
}

// GetEditionID returns the edition ID
func (b TestHardcoverBook) GetEditionID() string {
	return b.EditionID
}

// GetEditionASIN returns the edition ASIN
func (b TestHardcoverBook) GetEditionASIN() string {
	return b.EditionASIN
}

// GetEditionISBN10 returns the edition ISBN-10
func (b TestHardcoverBook) GetEditionISBN10() string {
	return b.EditionISBN10
}

// GetEditionISBN13 returns the edition ISBN-13
func (b TestHardcoverBook) GetEditionISBN13() string {
	return b.EditionISBN13
}

// createTestBook creates a new test book with the given parameters
func createTestBook(id, title, author, asin, isbn string) *TestAudiobookshelfBook {
	return &TestAudiobookshelfBook{
		ID:        id,
		LibraryID: "test-library",
		Path:      "/path/to/book",
		MediaType: "book",
		Media: struct {
			ID        string
			Metadata  TestAudiobookshelfMetadata
			CoverPath string
			Duration  float64
		}{
			ID:        "media-" + id,
			CoverPath: "/path/to/cover.jpg",
			Duration:  3600.0,
			Metadata: TestAudiobookshelfMetadata{
				Title:             title,
				AuthorName:        author,
				ASIN:              asin,
				ISBN:              isbn,
				Genres:            []string{"Fiction"},
				PublishedYear:     "2023",
				Publisher:         "Test Publisher",
				Description:       "Test description",
				Language:          "en",
				NarratorName:      "Test Narrator",
				SeriesName:        "Test Series",
			},
		},
		Progress: struct {
			CurrentTime float64
			IsFinished  bool
			StartedAt   int64
			FinishedAt  int64
		}{
			CurrentTime: 0,
			IsFinished:  false,
			StartedAt:   time.Now().Unix() - 3600, // 1 hour ago
			FinishedAt:  0,
		},
	}
}

// createTestFinishedBook creates a new test book that is marked as finished
func createTestFinishedBook(id, title, author, asin, isbn string) *TestAudiobookshelfBook {
	book := createTestBook(id, title, author, asin, isbn)
	book.Progress.IsFinished = true
	book.Progress.FinishedAt = time.Now().Unix()
	book.Progress.CurrentTime = book.Media.Duration // Set current time to duration (100% complete)
	return book
}



// TestLibrary implements a simple library interface for testing
type TestLibrary struct {
	ID   string
	Name string
}

// GetID returns the library ID
func (l *TestLibrary) GetID() string {
	return l.ID
}

// GetName returns the library name
func (l *TestLibrary) GetName() string {
	return l.Name
}
