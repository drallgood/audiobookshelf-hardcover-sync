package testutils

// EditionInput represents the input for creating or updating an edition
type EditionInput struct {
	BookID      int
	Title       string
	ImageURL    string
	ASIN        string
	AuthorIDs   []int
	NarratorIDs []int
	PublisherID int
	ReleaseDate string
	AudioLength int
}
