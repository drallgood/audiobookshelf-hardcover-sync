package main

import "time"

// BookMismatch represents a book that may need manual verification
type BookMismatch struct {
	Title         string
	Subtitle      string
	Author        string
	Narrator      string
	Publisher     string
	PublishedYear string
	ReleaseDate   string  // Maps to PublishedDate from API
	Duration      float64 // Duration in hours for easier reading
	ISBN          string
	ASIN          string
	BookID        string
	EditionID     string
	Reason        string
	Timestamp     time.Time
}

// AudiobookShelf API response structures
type Library struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type MediaMetadata struct {
	Title         string   `json:"title"`
	Subtitle      string   `json:"subtitle,omitempty"`
	AuthorName    string   `json:"authorName"`
	NarratorName  string   `json:"narratorName,omitempty"`
	Publisher     string   `json:"publisher,omitempty"`
	PublishedYear string   `json:"publishedYear,omitempty"`
	PublishedDate string   `json:"publishedDate,omitempty"`
	Description   string   `json:"description,omitempty"`
	Language      string   `json:"language,omitempty"`
	Genres        []string `json:"genres,omitempty"`
	ISBN          string   `json:"isbn,omitempty"`
	ISBN13        string   `json:"isbn_13,omitempty"`
	ASIN          string   `json:"asin,omitempty"`
	Duration      float64  `json:"duration,omitempty"` // Total duration in seconds
}

type Media struct {
	ID       string        `json:"id"`
	Metadata MediaMetadata `json:"metadata"`
	Duration float64       `json:"duration,omitempty"` // Backup duration location
}

type UserProgress struct {
	Progress      float64 `json:"progress"`
	CurrentTime   float64 `json:"currentTime"`
	IsFinished    bool    `json:"isFinished"`
	TimeRemaining float64 `json:"timeRemaining"`
	TotalDuration float64 `json:"totalDuration,omitempty"` // Total book duration
}

type Item struct {
	ID           string        `json:"id"`
	MediaType    string        `json:"mediaType"`
	Media        Media         `json:"media"`
	Progress     float64       `json:"progress"`
	UserProgress *UserProgress `json:"userProgress,omitempty"`
	IsFinished   bool          `json:"isFinished"`
}

type Audiobook struct {
	ID            string        `json:"id"`
	Title         string        `json:"title"`
	Author        string        `json:"author"`
	ISBN          string        `json:"isbn,omitempty"`
	ISBN10        string        `json:"isbn10,omitempty"`
	ASIN          string        `json:"asin,omitempty"`
	Progress      float64       `json:"progress"`
	CurrentTime   float64       `json:"currentTime,omitempty"`   // Current position in seconds
	TotalDuration float64       `json:"totalDuration,omitempty"` // Total duration in seconds
	Metadata      MediaMetadata `json:"metadata,omitempty"`      // Full metadata for enhanced mismatch collection
}
