package audnex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// Client represents an Audnex API client
type Client struct {
	httpClient *http.Client
	baseURL    string
	logger     *logger.Logger
}

// Book represents a book from the Audnex API
type Book struct {
	ASIN         string    `json:"asin"`
	Title        string    `json:"title"`
	Subtitle     string    `json:"subtitle,omitempty"`
	Authors      []string  `json:"authors,omitempty"`
	Narrators    []string  `json:"narrators,omitempty"`
	PublisherName string   `json:"publisherName,omitempty"`
	Summary      string    `json:"summary,omitempty"`
	ReleaseDate  string    `json:"releaseDate,omitempty"`
	Image        string    `json:"image,omitempty"`
	ISBN         string    `json:"isbn,omitempty"`
	Language     string    `json:"language,omitempty"`
	RuntimeLengthMin int   `json:"runtimeLengthMin,omitempty"`
	FormatType   string    `json:"formatType,omitempty"`
}

// NewClient creates a new Audnex API client
func NewClient(logger *logger.Logger) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://api.audnex.us",
		logger:  logger,
	}
}

// GetBookByASIN retrieves book details by ASIN with retry mechanism
func (c *Client) GetBookByASIN(ctx context.Context, asin, region string) (*Book, error) {
	if asin == "" {
		return nil, fmt.Errorf("ASIN is required")
	}

	url := fmt.Sprintf("%s/books/%s", c.baseURL, asin)
	if region != "" {
		url = fmt.Sprintf("%s?region=%s", url, region)
	}

	c.logger.Debug("Making request to Audnex API", map[string]interface{}{
		"method": "GetBookByASIN",
		"asin":   asin,
		"region": region,
		"url":    url,
	})

	// Retry configuration
	const maxRetries = 3
	const initialBackoff = 500 * time.Millisecond
	var lastErr error

	// Retry loop
	for attempt := 0; attempt < maxRetries; attempt++ {
		// If this is a retry, log it and wait with exponential backoff
		if attempt > 0 {
			backoff := initialBackoff * time.Duration(1<<uint(attempt-1))
			c.logger.Debug("Retrying Audnex API request", map[string]interface{}{
				"attempt":   attempt + 1,
				"max":       maxRetries,
				"asin":      asin,
				"backoff_ms": backoff.Milliseconds(),
				"error":     lastErr.Error(),
			})

			// Check if context is cancelled before sleeping
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				// Continue with retry
			}
		}

		// Create a new request for each attempt to ensure fresh connection
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("failed to make request: %w", err)
			continue // Retry on network errors
		}

		// Always close response body
		defer resp.Body.Close()

		// Only retry on 5xx server errors
		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			c.logger.Warn("Received server error from Audnex API", map[string]interface{}{
				"method":      "GetBookByASIN",
				"asin":        asin,
				"region":      region,
				"status_code": resp.StatusCode,
				"attempt":     attempt + 1,
			})
			lastErr = fmt.Errorf("received server error response: %d", resp.StatusCode)
			continue // Retry on server errors
		}

		// Don't retry on client errors (4xx)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			c.logger.Error("Received client error response", map[string]interface{}{
				"method":      "GetBookByASIN",
				"asin":        asin,
				"region":      region,
				"status_code": resp.StatusCode,
			})
			return nil, fmt.Errorf("received client error response: %d", resp.StatusCode)
		}

		// Success case
		if resp.StatusCode == http.StatusOK {
			var book Book
			if err := json.NewDecoder(resp.Body).Decode(&book); err != nil {
				return nil, fmt.Errorf("failed to decode response: %w", err)
			}

			// Log success after retries if this wasn't the first attempt
			if attempt > 0 {
				c.logger.Info("Successfully retrieved book after retries", map[string]interface{}{
					"asin":     asin,
					"attempts": attempt + 1,
				})
			}

			return &book, nil
		}

		// Unexpected status code
		c.logger.Error("Received unexpected response", map[string]interface{}{
			"method":      "GetBookByASIN",
			"asin":        asin,
			"region":      region,
			"status_code": resp.StatusCode,
		})
		return nil, fmt.Errorf("received unexpected response: %d", resp.StatusCode)
	}

	// If we get here, we've exhausted all retries
	c.logger.Error("Exhausted all retries for Audnex API request", map[string]interface{}{
		"method":     "GetBookByASIN",
		"asin":       asin,
		"max_retries": maxRetries,
		"error":      lastErr.Error(),
	})
	return nil, fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
}
