package audnex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// Client represents an Audnex API client
type Client struct {
	httpClient *http.Client
	baseURL    string
	logger     *logger.Logger
}

var (
	// ErrNotFound identifies an Audnex response indicating that a book is absent
	// from the requested region.
	ErrNotFound = errors.New("Audnex book not found")
	// ErrRateLimited identifies an Audnex rate-limit response.
	ErrRateLimited = errors.New("Audnex rate limited")
	// ErrTransient identifies a request that failed temporarily after retries, or
	// a 400 or 403 response, which is retryable rather than evidence of absence.
	ErrTransient = errors.New("Audnex request temporarily unavailable")
)

// APIError describes a typed Audnex API failure. Use errors.Is with
// ErrNotFound, ErrRateLimited, or ErrTransient to classify it.
type APIError struct {
	Kind       error
	StatusCode int
	Err        error
}

func (e *APIError) Error() string {
	if e.Err != nil {
		if e.StatusCode != 0 {
			return fmt.Sprintf("%s (HTTP %d): %v", e.Kind, e.StatusCode, e.Err)
		}
		return fmt.Sprintf("%s: %v", e.Kind, e.Err)
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s (HTTP %d)", e.Kind, e.StatusCode)
	}
	return e.Kind.Error()
}

// Unwrap exposes both the classification sentinel and the underlying cause.
func (e *APIError) Unwrap() []error {
	if e.Err == nil {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.Err}
}

const regionDiscoveryTimeout = 30 * time.Second

var audnexRegions = [...]string{"us", "ca", "uk", "au", "de", "fr", "es", "in", "it", "jp"}

// Regions returns a copy of the supported Audnex regions in sweep order:
// us, ca, uk, au, de, fr, es, in, it, jp.
func Regions() []string {
	regions := make([]string, len(audnexRegions))
	copy(regions, audnexRegions[:])
	return regions
}

// IsRegion reports whether region is one of the ten supported Audnex regions.
func IsRegion(region string) bool {
	for _, candidate := range audnexRegions {
		if region == candidate {
			return true
		}
	}
	return false
}

// Author represents an author from the Audnex API
type Author struct {
	Name string `json:"name,omitempty"`
	// Add other author fields as they become known
}

// Book represents a book from the Audnex API
type Book struct {
	ASIN             string      `json:"asin"`
	Title            string      `json:"title"`
	Subtitle         string      `json:"subtitle,omitempty"`
	Authors          interface{} `json:"authors,omitempty"`   // Accept any type to handle both array and object
	Narrators        interface{} `json:"narrators,omitempty"` // Accept any type to handle both array and object
	PublisherName    string      `json:"publisherName,omitempty"`
	Summary          string      `json:"summary,omitempty"`
	ReleaseDate      string      `json:"releaseDate,omitempty"`
	Image            string      `json:"image,omitempty"`
	ISBN             string      `json:"isbn,omitempty"`
	Language         string      `json:"language,omitempty"`
	RuntimeLengthMin int         `json:"runtimeLengthMin,omitempty"`
	FormatType       string      `json:"formatType,omitempty"`
}

// GetAuthorsAsStrings returns a slice of author names regardless of the format they were provided in
func (b *Book) GetAuthorsAsStrings() []string {
	var authors []string

	switch v := b.Authors.(type) {
	case []interface{}:
		// Handle array of objects or strings
		for _, author := range v {
			switch a := author.(type) {
			case string:
				authors = append(authors, a)
			case map[string]interface{}:
				if name, ok := a["name"].(string); ok {
					authors = append(authors, name)
				}
			}
		}
	case map[string]interface{}:
		// Handle single object
		if name, ok := v["name"].(string); ok {
			authors = append(authors, name)
		}
	case string:
		// Handle single string
		authors = append(authors, v)
	}

	return authors
}

// GetNarratorsAsStrings returns a slice of narrator names regardless of the format they were provided in
func (b *Book) GetNarratorsAsStrings() []string {
	var narrators []string

	switch v := b.Narrators.(type) {
	case []interface{}:
		// Handle array of objects or strings
		for _, narrator := range v {
			switch n := narrator.(type) {
			case string:
				narrators = append(narrators, n)
			case map[string]interface{}:
				if name, ok := n["name"].(string); ok {
					narrators = append(narrators, name)
				}
			}
		}
	case map[string]interface{}:
		// Handle single object
		if name, ok := v["name"].(string); ok {
			narrators = append(narrators, name)
		}
	case string:
		// Handle single string
		narrators = append(narrators, v)
	}

	return narrators
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

// NewClientForTesting creates a client with a custom base URL for testing
func NewClientForTesting(baseURL string, log *logger.Logger) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: baseURL,
		logger:  log,
	}
}

// CanonicalASIN returns a normalized ASIN when raw contains exactly ten ASCII
// letters or digits. Lowercase letters are uppercased so lookups and response
// comparisons use the same identifier without inferring a marketplace.
func CanonicalASIN(raw string) (string, bool) {
	asin := strings.TrimSpace(raw)
	if len(asin) != 10 {
		return "", false
	}
	for _, char := range asin {
		if char < '0' || char > '9' {
			if char < 'A' || char > 'Z' {
				if char < 'a' || char > 'z' {
					return "", false
				}
			}
		}
	}
	return strings.ToUpper(asin), true
}

// GetBookByASIN retrieves book details by ASIN with retry mechanism
func (c *Client) GetBookByASIN(ctx context.Context, asin, region string) (*Book, error) {
	if strings.TrimSpace(asin) == "" {
		return nil, fmt.Errorf("ASIN is required")
	}
	canonicalASIN, valid := CanonicalASIN(asin)
	if !valid {
		return nil, fmt.Errorf("ASIN must contain exactly 10 ASCII letters or digits")
	}
	asin = canonicalASIN

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
	var lastStatusCode int

	// Retry loop
	for attempt := 0; attempt < maxRetries; attempt++ {
		// If this is a retry, log it and wait with exponential backoff
		if attempt > 0 {
			backoff := initialBackoff * time.Duration(1<<uint(attempt-1))
			c.logger.Debug("Retrying Audnex API request", map[string]interface{}{
				"attempt":    attempt + 1,
				"max":        maxRetries,
				"asin":       asin,
				"backoff_ms": backoff.Milliseconds(),
				"error":      lastErr.Error(),
			})

			// Check if context is cancelled before sleeping
			select {
			case <-ctx.Done():
				return nil, classifyContextError(ctx.Err())
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
			lastStatusCode = 0
			lastErr = fmt.Errorf("failed to make request: %w", err)
			continue // Retry on network errors
		}

		// Retry server errors and request timeouts. GET is safe to retry, and
		// Audnex 408 responses indicate a temporary failure rather than a bad
		// request for this region.
		if resp.StatusCode == http.StatusRequestTimeout || (resp.StatusCode >= 500 && resp.StatusCode < 600) {
			lastStatusCode = resp.StatusCode
			_ = resp.Body.Close()
			c.logger.Warn("Received retryable response from Audnex API", map[string]interface{}{
				"method":      "GetBookByASIN",
				"asin":        asin,
				"region":      region,
				"status_code": resp.StatusCode,
				"attempt":     attempt + 1,
			})
			lastErr = fmt.Errorf("received retryable response: %d", resp.StatusCode)
			continue
		}

		// Don't retry on client errors (4xx)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				c.logger.Debug("Audnex book was not found in region", map[string]interface{}{
					"method":      "GetBookByASIN",
					"asin":        asin,
					"region":      region,
					"status_code": resp.StatusCode,
				})
				return nil, &APIError{Kind: ErrNotFound, StatusCode: resp.StatusCode}
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				c.logger.Warn("Audnex rate limit reached", map[string]interface{}{
					"method":      "GetBookByASIN",
					"asin":        asin,
					"region":      region,
					"status_code": resp.StatusCode,
				})
				return nil, &APIError{Kind: ErrRateLimited, StatusCode: resp.StatusCode}
			}
			if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden {
				// Audnex can answer a region with 400 or 403 for a temporary or
				// regional reason, so callers must not read it as an absent ASIN.
				c.logger.Warn("Audnex rejected the request for region", map[string]interface{}{
					"method":      "GetBookByASIN",
					"asin":        asin,
					"region":      region,
					"status_code": resp.StatusCode,
				})
				return nil, &APIError{Kind: ErrTransient, StatusCode: resp.StatusCode}
			}
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
				_ = resp.Body.Close()
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return nil, &APIError{Kind: ErrTransient, Err: err}
				}
				return nil, fmt.Errorf("failed to decode response: %w", err)
			}
			_ = resp.Body.Close()

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
		_ = resp.Body.Close()
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
		"method":      "GetBookByASIN",
		"asin":        asin,
		"max_retries": maxRetries,
		"error":       lastErr.Error(),
	})
	return nil, &APIError{
		Kind:       ErrTransient,
		StatusCode: lastStatusCode,
		Err:        fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr),
	}
}

// DiscoverBookByASIN searches the preferred Audnex region first, then each
// remaining supported region once. A successful result is returned only when
// Audnex reports the requested ASIN exactly. A completed sweep with no match
// returns a nil book, empty region, and nil error; rate limits and transient
// failures stop the sweep and return a typed error. The 30-second overall
// deadline includes each region's request retries and can be shortened by ctx.
func (c *Client) DiscoverBookByASIN(ctx context.Context, asin, preferredRegion string) (*Book, string, error) {
	if strings.TrimSpace(asin) == "" {
		return nil, "", fmt.Errorf("ASIN is required")
	}
	canonicalASIN, valid := CanonicalASIN(asin)
	if !valid {
		return nil, "", fmt.Errorf("ASIN must contain exactly 10 ASCII letters or digits")
	}
	asin = canonicalASIN

	preferredRegion = strings.ToLower(strings.TrimSpace(preferredRegion))
	if !isAudnexRegion(preferredRegion) {
		preferredRegion = "us"
	}

	discoveryCtx, cancel := context.WithTimeout(ctx, regionDiscoveryTimeout)
	defer cancel()

	regions := make([]string, 0, len(audnexRegions))
	regions = append(regions, preferredRegion)
	for _, region := range audnexRegions {
		if region != preferredRegion {
			regions = append(regions, region)
		}
	}

	for _, region := range regions {
		book, err := c.GetBookByASIN(discoveryCtx, asin, region)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		if book != nil {
			returnedASIN, valid := CanonicalASIN(book.ASIN)
			if valid && returnedASIN == asin {
				return book, region, nil
			}
		}
	}

	if err := discoveryCtx.Err(); err != nil {
		return nil, "", classifyContextError(err)
	}
	return nil, "", nil
}

func isAudnexRegion(region string) bool {
	return IsRegion(region)
}

func classifyContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &APIError{Kind: ErrTransient, Err: err}
	}
	return err
}
