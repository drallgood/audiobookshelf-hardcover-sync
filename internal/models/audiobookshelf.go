package models

// AudiobookshelfBook represents a book from the Audiobookshelf API
type AudiobookshelfBook struct {
	ID        string `json:"id"`
	LibraryID string `json:"libraryId"`
	Path      string `json:"path"`
	MediaType string `json:"mediaType"`
	Media     struct {
		ID       string `json:"id"`
		Metadata struct {
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
		} `json:"metadata"`
		CoverPath string  `json:"coverPath"`
		Duration  float64 `json:"duration"`
	} `json:"media"`
	// Add progress fields if needed
	Progress struct {
		CurrentTime float64 `json:"currentTime"`
		IsFinished  bool    `json:"isFinished"`
		StartedAt   int64   `json:"startedAt"`
		FinishedAt  int64   `json:"finishedAt"`
	} `json:"progress,omitempty"`
}

// AudiobookshelfLibraryResponse represents the response from the Audiobookshelf API
// when fetching library items
type AudiobookshelfLibraryResponse struct {
	Results []AudiobookshelfBook `json:"results"`
	Total   int                  `json:"total"`
	Limit   int                  `json:"limit"`
	Page    int                  `json:"page"`
}
