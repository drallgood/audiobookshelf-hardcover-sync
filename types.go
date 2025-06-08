package main

import (
	"encoding/json"
	"time"
)

// BookMismatch represents a book that may need manual verification
type BookMismatch struct {
	Title           string
	Subtitle        string
	Author          string
	Narrator        string
	Publisher       string
	PublishedYear   string
	ReleaseDate     string  // Maps to PublishedDate from API
	Duration        float64 // Duration in hours for easier reading
	DurationSeconds int     // Duration in seconds for JSON processing
	ISBN            string
	ASIN            string
	BookID          string
	EditionID       string
	Reason          string
	Timestamp       time.Time
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

// ID lookup and search functionality types

// PersonSearchResult represents a person (author/narrator) search result
type PersonSearchResult struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	BooksCount  int    `json:"books_count"`
	Bio         string `json:"bio"`
	IsCanonical bool   `json:"is_canonical"`
	CanonicalID *int   `json:"canonical_id"`
}

// PublisherSearchResult represents a publisher search result
type PublisherSearchResult struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	EditionsCount int    `json:"editions_count"`
	IsCanonical   bool   `json:"is_canonical"`
	CanonicalID   *int   `json:"canonical_id"`
}

// PersonSearchResponse represents the GraphQL response for person searches
type PersonSearchResponse struct {
	Data struct {
		Authors []PersonSearchResult `json:"authors"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// PublisherSearchResponse represents the GraphQL response for publisher searches
type PublisherSearchResponse struct {
	Data struct {
		Publishers []PublisherSearchResult `json:"publishers"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// SearchAPIResponse represents the response from Hardcover's search API
type SearchAPIResponse struct {
	Data struct {
		Search struct {
			Error     *string       `json:"error"`
			IDs       []json.Number `json:"ids"` // Use json.Number to handle both strings and integers
			Page      int           `json:"page"`
			PerPage   int           `json:"per_page"`
			Query     string        `json:"query"`
			QueryType string        `json:"query_type"`
			Results   interface{}   `json:"results"`
		} `json:"search"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}
