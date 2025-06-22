package audiobookshelf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
)

const (
	apiPath = "/api"
)

// AudiobookshelfLibrary represents a library in Audiobookshelf
type AudiobookshelfLibrary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Client is a client for the Audiobookshelf API
type Client struct {
	baseURL string
	token   string
	client  *http.Client
	logger  *logger.Logger
}

// NewClient creates a new Audiobookshelf client
func NewClient(baseURL, token string) *Client {
	log := logger.Get().With().
		Str("component", "audiobookshelf_client").
		Logger()

	return &Client{
		baseURL: baseURL,
		token:   token,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: &logger.Logger{Logger: log},
	}
}

// GetLibraries fetches all libraries from Audiobookshelf
func (c *Client) GetLibraries(ctx context.Context) ([]AudiobookshelfLibrary, error) {
	const endpoint = "/libraries"
	log := c.logger.With().Str("endpoint", endpoint).Logger()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+apiPath+endpoint, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create request")
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		log.Error().Err(err).Msg("Request failed")
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read response body")
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Error().
			Int("status", resp.StatusCode).
			Str("response", string(body)).
			Msg("Unexpected status code")
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result struct {
		Libraries []AudiobookshelfLibrary `json:"libraries"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		log.Error().Err(err).Msg("Failed to decode response")
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	log.Info().Int("count", len(result.Libraries)).Msg("Successfully fetched libraries")
	return result.Libraries, nil
}

// GetLibraryItems returns all library items from a specific Audiobookshelf library
func (c *Client) GetLibraryItems(ctx context.Context, libraryID string) ([]models.AudiobookshelfBook, error) {
	if libraryID == "" {
		return nil, fmt.Errorf("library ID is required")
	}
	endpoint := fmt.Sprintf("/libraries/%s/items?include=progress&minified=0", libraryID)
	log := c.logger.With().Str("endpoint", endpoint).Logger()

	// Build request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+apiPath+endpoint, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create request")
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	// Execute request
	log.Debug().Msg("Fetching library items")
	resp, err := c.client.Do(req)
	if err != nil {
		log.Error().Err(err).Msg("Failed to fetch library items")
		return nil, fmt.Errorf("failed to fetch library items: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Error().
			Int("status", resp.StatusCode).
			Str("response", string(body)).
			Msg("Unexpected status code")
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read response body")
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Save the raw response to a file for inspection
	logDir := "logs"
	logFile := filepath.Join(logDir, "audiobookshelf_response.json")
	
	// Ensure the logs directory exists
	log.Info().Str("path", logDir).Msg("Ensuring logs directory exists")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Error().Err(err).Str("path", logDir).Msg("Failed to create logs directory")
		// Try current directory if logs directory fails
		logDir = "."
		logFile = "audiobookshelf_response.json"
	}

	// Save the response to file
	log.Info().Str("path", logFile).Msg("Saving API response to file")
	if err := os.WriteFile(logFile, body, 0644); err != nil {
		log.Error().Err(err).Str("path", logFile).Msg("Failed to save API response to file")
		// Try writing to /tmp as a last resort
		tmpFile := filepath.Join(os.TempDir(), "audiobookshelf_response.json")
		if writeErr := os.WriteFile(tmpFile, body, 0644); writeErr != nil {
			log.Error().Err(writeErr).Str("path", tmpFile).Msg("Failed to save API response to temp file")
		} else {
			log.Info().Str("path", tmpFile).Msg("Saved raw API response to temp file")
		}
	} else {
		log.Info().Str("path", logFile).Msg("Successfully saved raw API response to file")
	}

	// Print the raw response directly to stderr to ensure it's captured
	os.Stderr.WriteString("\n=== START OF RAW API RESPONSE ===\n")
	os.Stderr.Write(body)
	os.Stderr.WriteString("\n=== END OF RAW API RESPONSE ===\n\n")

	// Save to a file in the current directory using absolute path
	absPath := "/tmp/audiobookshelf_raw_response.json"
	if err := os.WriteFile(absPath, body, 0644); err != nil {
		log.Error().Err(err).Str("path", absPath).Msg("Failed to save raw response to file")
	} else {
		log.Info().Str("path", absPath).Msg("Saved raw response to file")
	}

	// Also save to current working directory
	cwd, _ := os.Getwd()
	relPath := filepath.Join(cwd, "audiobookshelf_raw_response.json")
	if err := os.WriteFile(relPath, body, 0644); err != nil {
		log.Error().Err(err).Str("path", relPath).Msg("Failed to save raw response to current directory")
	} else {
		log.Info().Str("path", relPath).Msg("Saved raw response to current directory")
	}

	// Try to read back the file to verify it was written correctly
	if content, err := os.ReadFile(absPath); err != nil {
		log.Error().Err(err).Str("path", absPath).Msg("Failed to read back saved response file")
	} else if !bytes.Equal(content, body) {
		log.Error().Str("path", absPath).Msg("Saved file content does not match original response")
	}

	// Log a sample of the response for debugging
	sampleSize := 500
	if len(body) < sampleSize {
		sampleSize = len(body)
	}
	log.Debug().
		Str("response_sample", string(body[:sampleSize])).
		Int("total_bytes", len(body)).
		Msg("Raw API response sample from Audiobookshelf")

	// First, unmarshal into a generic map to inspect the structure
	var rawResponse map[string]interface{}
	if err := json.Unmarshal(body, &rawResponse); err != nil {
		log.Error().
			Err(err).
			Str("response", string(body)).
			Msg("Failed to decode response into raw map")
		return nil, fmt.Errorf("failed to decode response into raw map: %w", err)
	}

	// Log the top-level keys in the response
	keys := make([]string, 0, len(rawResponse))
	for k := range rawResponse {
		keys = append(keys, k)
	}
	log.Info().
		Strs("top_level_keys", keys).
		Msg("Top-level keys in API response")

	// Check if we have a 'results' array
	resultsRaw, hasResults := rawResponse["results"]
	if !hasResults {
		log.Error().
			Interface("response", rawResponse).
			Msg("No 'results' key in API response")
		return nil, fmt.Errorf("no 'results' key in API response")
	}

	// Log the type of the results value
	log.Info().
		Str("results_type", fmt.Sprintf("%T", resultsRaw)).
		Msg("Type of results in API response")

	// Try to unmarshal the results into our model
	resultsJSON, err := json.Marshal(resultsRaw)
	if err != nil {
		log.Error().
			Err(err).
			Interface("results", resultsRaw).
			Msg("Failed to marshal results back to JSON")
		return nil, fmt.Errorf("failed to marshal results: %w", err)
	}

	var books []models.AudiobookshelfBook
	if err := json.Unmarshal(resultsJSON, &books); err != nil {
		log.Error().
			Err(err).
			Str("results_json", string(resultsJSON)).
			Msg("Failed to unmarshal results into books")
		return nil, fmt.Errorf("failed to unmarshal results into books: %w", err)
	}

	// Log the first book's raw data for debugging
	if len(books) > 0 {
		firstBook := books[0]
		log.Info().
			Str("first_book_id", firstBook.ID).
			Str("first_book_title", firstBook.Media.Metadata.Title).
			Str("first_book_author", firstBook.Media.Metadata.AuthorName).
			Msg("First book details from parsed response")

		// Log the raw JSON of the first book
		if bookJSON, err := json.MarshalIndent(firstBook, "", "  "); err == nil {
			log.Debug().
				Str("first_book_json", string(bookJSON)).
				Msg("First book raw JSON")
		}
	}

	result := struct {
		Results []models.AudiobookshelfBook `json:"results"`
	}{
		Results: books,
	}

	log.Info().
		Int("count", len(result.Results)).
		Msg("Successfully fetched library items")

	// Log first book details for debugging
	if len(result.Results) > 0 {
		firstBook := result.Results[0]
		log.Debug().
			Str("book_id", firstBook.ID).
			Str("title", firstBook.Media.Metadata.Title).
			Str("author", firstBook.Media.Metadata.AuthorName).
			Str("isbn", firstBook.Media.Metadata.ISBN).
			Str("asin", firstBook.Media.Metadata.ASIN).
			Msg("First book details")
	}

	return result.Results, nil
}

// GetUserProgress fetches the current user's progress data from Audiobookshelf
func (c *Client) GetUserProgress(ctx context.Context) (*models.AudiobookshelfUserProgress, error) {
	const endpoint = "/me"
	log := c.logger.With().Str("endpoint", endpoint).Logger()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+apiPath+endpoint, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create request")
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		log.Error().Err(err).Msg("Request failed")
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read response body")
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Error().
			Int("status", resp.StatusCode).
			Str("response", string(body)).
			Msg("Unexpected status code")
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var progress models.AudiobookshelfUserProgress
	if err := json.Unmarshal(body, &progress); err != nil {
		log.Error().Err(err).Msg("Failed to decode response")
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	log.Info().
		Int("media_progress_count", len(progress.MediaProgress)).
		Int("listening_sessions_count", len(progress.ListeningSessions)).
		Msg("Successfully fetched user progress")

	return &progress, nil
}

// GetListeningSessions fetches recent listening sessions from Audiobookshelf
func (c *Client) GetListeningSessions(ctx context.Context, since time.Time) ([]models.AudiobookshelfBook, error) {
	const endpoint = "/me/listening-sessions"
	log := c.logger.With().
		Str("endpoint", endpoint).
		Time("since", since).
		Logger()

	// Build URL with query parameters
	url := fmt.Sprintf("%s?since=%d", c.baseURL+apiPath+endpoint, since.Unix()*1000)

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create request")
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	// Execute request
	log.Debug().Msg("Fetching listening sessions")
	resp, err := c.client.Do(req)
	if err != nil {
		log.Error().Err(err).Msg("Failed to fetch listening sessions")
		return nil, fmt.Errorf("failed to fetch listening sessions: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Error().
			Int("status", resp.StatusCode).
			Str("response", string(body)).
			Msg("Unexpected status code")
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse response
	var sessions []models.AudiobookshelfBook
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		log.Error().Err(err).Msg("Failed to decode response")
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	log.Info().
		Int("count", len(sessions)).
		Msg("Successfully fetched listening sessions")

	return sessions, nil
}
