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
	log := logger.Get()
	log = log.With(map[string]interface{}{
		"component": "audiobookshelf_client",
	})

	return &Client{
		baseURL: baseURL,
		token:   token,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: log,
	}
}

// GetLibraries fetches all libraries from Audiobookshelf
func (c *Client) GetLibraries(ctx context.Context) ([]AudiobookshelfLibrary, error) {
	const endpoint = "/libraries"
	log := c.logger.With(map[string]interface{}{
		"endpoint": endpoint,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+apiPath+endpoint, nil)
	if err != nil {
		log.Error("Failed to create request", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		log.Error("Request failed", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error("Failed to read response body", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Error("Unexpected status code", map[string]interface{}{
			"status": resp.StatusCode,
			"body":   string(body),
		})
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result struct {
		Libraries []AudiobookshelfLibrary `json:"libraries"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		log.Error("Failed to decode response", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	log.Info("Successfully fetched libraries", map[string]interface{}{
		"count": len(result.Libraries),
	})
	return result.Libraries, nil
}

// GetLibraryItems returns all library items from a specific Audiobookshelf library
func (c *Client) GetLibraryItems(ctx context.Context, libraryID string) ([]models.AudiobookshelfBook, error) {
	if libraryID == "" {
		return nil, fmt.Errorf("library ID is required")
	}
	endpoint := fmt.Sprintf("/libraries/%s/items?include=progress&minified=0", libraryID)
	log := c.logger.With(map[string]interface{}{
		"endpoint": endpoint,
	})

	// Build request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+apiPath+endpoint, nil)
	if err != nil {
		log.Error("Failed to create request", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	// Execute request
	log.Debug("Fetching library items", nil)
	resp, err := c.client.Do(req)
	if err != nil {
		log.Error("Failed to fetch library items", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to fetch library items: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Error("Unexpected status code", map[string]interface{}{
			"status":   resp.StatusCode,
			"response": string(body),
		})
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error("Failed to read response body", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Save the raw response to a file for inspection
	logDir := "logs"

	// Ensure the logs directory exists
	log.Info("Ensuring logs directory exists", map[string]interface{}{
		"path": logDir,
	})
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Error("Failed to create logs directory, falling back to current directory", map[string]interface{}{
			"error": err.Error(),
			"path":  logDir,
		})
		// Use current directory if logs directory fails
		logDir = "."
	}

	logFile := filepath.Join(logDir, "audiobookshelf_response.json")

	// Save the response to file
	log.Info("Saving API response to file", map[string]interface{}{
		"path": logFile,
	})
	if err := os.WriteFile(logFile, body, 0644); err != nil {
		log.Error("Failed to save API response to file", map[string]interface{}{
			"error": err.Error(),
			"path":  logFile,
		})
		// Try writing to /tmp as a last resort
		tmpFile := filepath.Join(os.TempDir(), "audiobookshelf_response.json")
		if writeErr := os.WriteFile(tmpFile, body, 0644); writeErr != nil {
			log.Error("Failed to save API response to temp file", map[string]interface{}{
				"error": writeErr.Error(),
				"path":  tmpFile,
			})
		} else {
			log.Info("Saved raw API response to temp file", map[string]interface{}{
				"path": tmpFile,
			})
		}
	} else {
		log.Info("Successfully saved raw API response to file", map[string]interface{}{
			"path": logFile,
		})
	}

	// Print the raw response directly to stderr to ensure it's captured
	os.Stderr.WriteString("\n=== START OF RAW API RESPONSE ===\n")
	os.Stderr.Write(body)
	os.Stderr.WriteString("\n=== END OF RAW API RESPONSE ===\n\n")

	// Save to a file in the current directory using absolute path
	absPath := "/tmp/audiobookshelf_raw_response.json"
	if err := os.WriteFile(absPath, body, 0644); err != nil {
		log.Error("Failed to save raw response to file", map[string]interface{}{
			"error": err.Error(),
			"path":  absPath,
		})
	} else {
		log.Info("Saved raw response to file", map[string]interface{}{
			"path": absPath,
		})
	}

	// Also save to current working directory
	cwd, _ := os.Getwd()
	relPath := filepath.Join(cwd, "audiobookshelf_raw_response.json")
	if err := os.WriteFile(relPath, body, 0644); err != nil {
		log.Error("Failed to save raw response to current directory", map[string]interface{}{
			"error": err.Error(),
			"path":  relPath,
		})
	} else {
		log.Info("Saved raw response to current directory", map[string]interface{}{
			"path": relPath,
		})
	}

	// Try to read back the file to verify it was written correctly
	if content, err := os.ReadFile(absPath); err != nil {
		log.Error("Failed to read back saved response file", map[string]interface{}{
			"error": err.Error(),
			"path":  absPath,
		})
	} else if !bytes.Equal(content, body) {
		log.Error("Saved file content does not match original response", map[string]interface{}{
			"path": absPath,
		})
	}

	// Log a sample of the response for debugging
	sampleSize := 500
	if len(body) < sampleSize {
		sampleSize = len(body)
	}
	log.Debug("Raw API response sample from Audiobookshelf", map[string]interface{}{
		"response_sample": string(body[:sampleSize]),
		"total_bytes":     len(body),
	})

	// First, unmarshal into a generic map to inspect the structure
	var rawResponse map[string]interface{}
	if err := json.Unmarshal(body, &rawResponse); err != nil {
		log.Error("Failed to unmarshal response", map[string]interface{}{
			"error":           err.Error(),
			"response_sample": string(body[:sampleSize]),
		})
		return nil, fmt.Errorf("failed to decode response into raw map: %w", err)
	}

	// Log the top-level keys in the response
	keys := make([]string, 0, len(rawResponse))
	for k := range rawResponse {
		keys = append(keys, k)
	}
	log.Info("Top-level keys in API response", map[string]interface{}{
		"top_level_keys": keys,
	})

	// Check if we have a 'results' array
	resultsRaw, hasResults := rawResponse["results"]
	if !hasResults {
		log.Error("No 'results' key in API response", map[string]interface{}{
			"response": rawResponse,
		})
		return nil, fmt.Errorf("no 'results' key in API response")
	}

	// Log the type of the results value
	log.Info("Type of results in API response", map[string]interface{}{
		"results_type": fmt.Sprintf("%T", resultsRaw),
	})

	// Try to unmarshal the results into our model
	resultsJSON, err := json.Marshal(resultsRaw)
	if err != nil {
		log.Error("Failed to marshal results", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to marshal results: %w", err)
	}

	var books []models.AudiobookshelfBook
	if err := json.Unmarshal(resultsJSON, &books); err != nil {
		log.Error("Failed to process library item", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to unmarshal results into books: %w", err)
	}

	// Log the first book's raw data for debugging
	if len(books) > 0 {
		firstBook := books[0]
		log.Info("First book details from parsed response", map[string]interface{}{
			"first_book_id":     firstBook.ID,
			"first_book_title":  firstBook.Media.Metadata.Title,
			"first_book_author": firstBook.Media.Metadata.AuthorName,
		})

		// Log the raw JSON of the first book
		if bookJSON, err := json.MarshalIndent(firstBook, "", "  "); err == nil {
			log.Debug("First book raw JSON", map[string]interface{}{
				"first_book_json": string(bookJSON),
			})
		}
	}

	result := struct {
		Results []models.AudiobookshelfBook `json:"results"`
	}{
		Results: books,
	}

	log.Info("Successfully fetched library items", map[string]interface{}{
		"count": len(result.Results),
	})

	// Log first book details for debugging
	if len(result.Results) > 0 {
		firstBook := result.Results[0]
		log.Debug("First book details", map[string]interface{}{
			"book_id": firstBook.ID,
			"title":   firstBook.Media.Metadata.Title,
			"author":  firstBook.Media.Metadata.AuthorName,
			"isbn":    firstBook.Media.Metadata.ISBN,
			"asin":    firstBook.Media.Metadata.ASIN,
		})
	}

	return result.Results, nil
}

// GetUserProgress fetches the current user's progress data from Audiobookshelf
func (c *Client) GetUserProgress(ctx context.Context) (*models.AudiobookshelfUserProgress, error) {
	const endpoint = "/me"
	log := c.logger.With(map[string]interface{}{
		"endpoint": endpoint,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+apiPath+endpoint, nil)
	if err != nil {
		log.Error("Failed to create request in GetUserProgress", map[string]interface{}{"error": err.Error()})
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		log.Error("Request failed in GetUserProgress", map[string]interface{}{"error": err.Error()})
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error("Failed to read response body in GetUserProgress", map[string]interface{}{"error": err.Error()})
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Error("Unexpected status code in GetUserProgress", map[string]interface{}{
			"status":   resp.StatusCode,
			"response": string(body),
		})
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var progress models.AudiobookshelfUserProgress
	if err := json.Unmarshal(body, &progress); err != nil {
		log.Error("Failed to decode response in GetUserProgress", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	log.Info("Successfully fetched user progress in GetUserProgress", map[string]interface{}{
		"media_progress_count":     len(progress.MediaProgress),
		"listening_sessions_count": len(progress.ListeningSessions),
	})

	return &progress, nil
}

// GetListeningSessions fetches recent listening sessions from Audiobookshelf
func (c *Client) GetListeningSessions(ctx context.Context, since time.Time) ([]models.AudiobookshelfBook, error) {
	const endpoint = "/me/listening-sessions"
	log := c.logger.With(map[string]interface{}{
		"endpoint": endpoint,
		"since":    since,
	})

	// Build URL with query parameters
	url := fmt.Sprintf("%s?since=%d", c.baseURL+apiPath+endpoint, since.Unix()*1000)

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Error("Failed to create request", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	// Execute request
	log.Debug("Fetching listening sessions", nil)
	resp, err := c.client.Do(req)
	if err != nil {
		log.Error("Failed to fetch listening sessions", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to fetch listening sessions: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Error("Unexpected status code", map[string]interface{}{
			"status":   resp.StatusCode,
			"response": string(body),
		})
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Parse response
	var sessions []models.AudiobookshelfBook
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error("Failed to read response body", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if err := json.Unmarshal(body, &sessions); err != nil {
		log.Error("Failed to decode response", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	log.Info("Successfully fetched listening sessions", map[string]interface{}{
		"count": len(sessions),
	})

	return sessions, nil
}
