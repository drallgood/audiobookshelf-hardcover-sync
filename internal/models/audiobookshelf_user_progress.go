package models

// AudiobookshelfUserProgress represents user progress data from the Audiobookshelf /api/me endpoint
type AudiobookshelfUserProgress struct {
	ID             string `json:"id"`
	Username       string `json:"username"`
	MediaProgress  []struct {
		ID             string  `json:"id"`
		LibraryItemID  string  `json:"libraryItemId"`
		UserID         string  `json:"userId"`
		IsFinished     bool    `json:"isFinished"`
		Progress       float64 `json:"progress"`
		CurrentTime    float64 `json:"currentTime"`
		Duration       float64 `json:"duration"`
		StartedAt      int64   `json:"startedAt"`
		FinishedAt     int64   `json:"finishedAt"`
		LastUpdate     int64   `json:"lastUpdate"`
		TimeListening  float64 `json:"timeListening"`
	} `json:"mediaProgress"`
	ListeningSessions []struct {
		ID             string  `json:"id"`
		UserID         string  `json:"userId"`
		LibraryItemID  string  `json:"libraryItemId"`
		MediaType      string  `json:"mediaType"`
		MediaMetadata  struct {
			Title    string `json:"title"`
			Author   string `json:"author"`
		} `json:"mediaMetadata"`
		Duration       float64 `json:"duration"`
		CurrentTime    float64 `json:"currentTime"`
		Progress       float64 `json:"progress"`
		IsFinished     bool    `json:"isFinished"`
		StartedAt      int64   `json:"startedAt"`
		UpdatedAt      int64   `json:"updatedAt"`
	} `json:"listeningSessions"`
}
