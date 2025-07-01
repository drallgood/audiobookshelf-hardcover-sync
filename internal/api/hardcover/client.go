package hardcover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hasura/go-graphql-client"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/cache"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/edition"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
)

// getMapKeys returns a sorted list of keys from a map
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// graphqlOperation is a helper type for GraphQL operations
type graphqlOperation string

// Constants for GraphQL operation types
const (
	queryOperation    graphqlOperation = "query"
	mutationOperation graphqlOperation = "mutation"
)

// GetUserBookResult represents the result of getting a user book
// This struct is used to return the result of GetUserBook method
type GetUserBookResult struct {
	ID        int64    `json:"id"`
	BookID    int64    `json:"book_id"`
	EditionID *int64   `json:"edition_id,omitempty"`
	Status    string   `json:"status"`
	Progress  *float64 `json:"progress,omitempty"`
}

// UpdateUserBookInput represents the input for updating a user book
type UpdateUserBookInput struct {
	ID        int64  `json:"id"`
	EditionID *int64 `json:"edition_id,omitempty"`
}

// Common errors
var (
	ErrBookNotFound     = errors.New("book not found")
	ErrUserBookNotFound = errors.New("user book not found")
	ErrInvalidInput     = errors.New("invalid input")
)

const (
	// DefaultBaseURL is the default base URL for the Hardcover API
	DefaultBaseURL = "https://api.hardcover.app/v1/graphql"
	// DefaultTimeout is the default timeout for HTTP requests
	DefaultTimeout = 30 * time.Second
	// DefaultMaxRetries is the default number of retries for failed requests
	DefaultMaxRetries = 3
	// DefaultRetryDelay is the default delay between retries
	DefaultRetryDelay = 500 * time.Millisecond
)

// Default rate limiting configuration
const (
	// DefaultRateLimit is the default minimum time between requests (100ms = 10 requests/sec)
	DefaultRateLimit = 100 * time.Millisecond
	// DefaultBurst is the default burst size for rate limiting
	DefaultBurst = 10
)

// ClientConfig holds configuration for the Hardcover client
type ClientConfig struct {
	// BaseURL is the base URL for the API (default: DefaultBaseURL)
	BaseURL string
	// Timeout specifies a time limit for requests (default: DefaultTimeout)
	Timeout time.Duration
	// MaxRetries specifies the maximum number of retries for failed requests (default: DefaultMaxRetries)
	MaxRetries int
	// RetryDelay specifies the delay between retries (default: DefaultRetryDelay)
	RetryDelay time.Duration
	// RateLimit specifies the minimum time between requests (default: from config or DefaultRateLimit)
	RateLimit time.Duration
	// Burst specifies the burst size for rate limiting (default: from config or DefaultBurst)
	Burst int
	// MaxConcurrent specifies the maximum number of concurrent requests (default: from config or 3)
	MaxConcurrent int
}

// headerAddingTransport is an http.RoundTripper that adds the required headers
// for authenticating with the Hardcover API.
type headerAddingTransport struct {
	token   string
	baseURL string
	rt      http.RoundTripper
}

// RoundTrip implements the http.RoundTripper interface.
func (t *headerAddingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Content-Type", "application/json")
	return t.rt.RoundTrip(req)
}

// CacheTTL is the default time-to-live for cached items
const (
	// DefaultCacheTTL is the default TTL for cached items
	DefaultCacheTTL = 1 * time.Hour
	// UserBookIDCacheTTL is the TTL for user book ID cache entries
	UserBookIDCacheTTL = 24 * time.Hour
	// CurrentUserCacheTTL is the TTL for the current user cache entry
	CurrentUserCacheTTL = 1 * time.Hour
)

// Client represents a client for the Hardcover API
// Client represents a client for the Hardcover API
type Client struct {
	baseURL          string
	authToken        string
	httpClient       *http.Client
	gqlClient        *graphql.Client
	logger           *logger.Logger
	currentUserID    int
	currentUserMutex sync.RWMutex
	rateLimiter      *util.RateLimiter
	maxRetries       int
	retryDelay       time.Duration
	userBookIDCache  cache.Cache[int, int]    // editionID -> userBookID
	userCache        cache.Cache[string, any] // Generic cache for user-specific data
}

// GetAuthHeader returns the properly formatted Authorization header value
// This is a public method that can be used by other packages to get the auth header
func (c *Client) GetAuthHeader() string {
	// Ensure the token has the Bearer prefix
	authToken := strings.TrimSpace(c.authToken)
	if authToken != "" && !strings.HasPrefix(authToken, "Bearer ") {
		authToken = "Bearer " + authToken
	}
	return authToken
}

// safeString is a helper function to safely get a string value from a string pointer
func safeString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// stringValue is a helper function to safely get a string value from a string pointer
func stringValue(s *string) string {
	return safeString(s)
}

// DefaultClientConfig returns the default configuration for the client
func DefaultClientConfig() *ClientConfig {
	// Load the default config to get the rate limit settings
	cfg := config.DefaultConfig()

	return &ClientConfig{
		BaseURL:       DefaultBaseURL,
		Timeout:       DefaultTimeout,
		MaxRetries:    DefaultMaxRetries,
		RetryDelay:    DefaultRetryDelay,
		RateLimit:     cfg.RateLimit.Rate,          // Use rate from config
		Burst:         cfg.RateLimit.Burst,         // Use burst from config
		MaxConcurrent: cfg.RateLimit.MaxConcurrent, // Use max concurrent from config
	}
}

// NewClient creates a new Hardcover client with default configuration
func NewClient(token string, log *logger.Logger) *Client {
	return NewClientWithConfig(DefaultClientConfig(), token, log)
}

// NewClientWithConfig creates a new Hardcover client with custom configuration
func NewClientWithConfig(cfg *ClientConfig, token string, log *logger.Logger) *Client {
	if cfg == nil {
		cfg = DefaultClientConfig()
	}

	// Log the client configuration
	if log != nil {
		log.Debug("Creating new Hardcover client with config", map[string]interface{}{
			"base_url":    cfg.BaseURL,
			"timeout":     cfg.Timeout,
			"max_retries": cfg.MaxRetries,
			"retry_delay": cfg.RetryDelay,
		})
	} else {
		// If no logger is provided, create a default one
		log = logger.Get()
		log = log.With(map[string]interface{}{
			"component": "hardcover_client",
		})
	}

	// Create HTTP client with timeout
	httpClient := &http.Client{
		Timeout: cfg.Timeout,
	}

	// Create rate limiter with max concurrent requests from config
	rateLimiter := util.NewRateLimiter(cfg.RateLimit, cfg.Burst, cfg.MaxConcurrent, log)

	// Create logger if not provided
	if log == nil {
		log = logger.Get()
	}

	// Log the logger configuration
	log.Info("Logger initialized for Hardcover client", map[string]interface{}{
		"log_level": log.GetLevel().String(),
	})

	// Create a child logger with context
	childLogger := log
	if log != nil {
		childLogger = log.With(map[string]interface{}{
			"component": "hardcover_client",
		})
	}

	childLogger.Info("Created child logger for Hardcover client", nil)

	// Create authenticated HTTP client with headers
	authClient := &http.Client{
		Transport: &headerAddingTransport{
			token:   token,
			baseURL: cfg.BaseURL,
			rt:      http.DefaultTransport,
		},
	}

	// Create GraphQL client with the authenticated HTTP client
	gqlClient := graphql.NewClient(cfg.BaseURL, authClient)

	// Create caches with appropriate TTLs
	userBookIDCache := cache.WithTTL[int, int](
		cache.NewMemoryCache[int, int](childLogger),
		UserBookIDCacheTTL,
	)

	userCache := cache.WithTTL[string, any](
		cache.NewMemoryCache[string, any](childLogger),
		DefaultCacheTTL,
	)

	// Create and return the client
	client := &Client{
		baseURL:         strings.TrimRight(cfg.BaseURL, "/"),
		authToken:       token,
		httpClient:      httpClient,
		gqlClient:       gqlClient,
		logger:          childLogger,
		rateLimiter:     rateLimiter,
		maxRetries:      cfg.MaxRetries,
		retryDelay:      cfg.RetryDelay,
		userBookIDCache: userBookIDCache,
		userCache:       userCache,
	}

	// Log client creation
	childLogger.Debug("Created new Hardcover client", nil)

	return client
}

// enforceRateLimit ensures we don't exceed the API rate limits
func (c *Client) enforceRateLimit() error {
	// Simply use the rate limiter which already handles:
	// - Token bucket algorithm
	// - Jitter
	// - Context cancellation
	// - Dynamic rate adjustment
	return c.rateLimiter.Wait(context.Background())
}

// loggingRoundTripper is a custom http.RoundTripper that logs requests and responses
type loggingRoundTripper struct {
	logger *logger.Logger
	rt     http.RoundTripper
}

// RoundTrip implements the http.RoundTripper interface
func (l loggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Log the request with basic info
	l.logger.Info("Sending request", map[string]interface{}{
		"method": req.Method,
		"url":    req.URL.String(),
	})

	// Make the request
	resp, err := l.rt.RoundTrip(req)
	if err != nil {
		l.logger.Error("Request failed", map[string]interface{}{
			"error":  err.Error(),
			"method": req.Method,
			"url":    req.URL.String(),
		})
		return nil, err
	}

	// Read the response body
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		l.logger.Error("Failed to read response body", map[string]interface{}{
			"error":  err.Error(),
			"method": req.Method,
			"url":    req.URL.String(),
		})
		return nil, err
	}

	// Log response with appropriate level
	logFields := map[string]interface{}{
		"status":          resp.StatusCode,
		"status_text":     resp.Status,
		"path":            req.URL.Path,
		"response_length": len(body),
	}

	if resp.StatusCode >= 400 {
		l.logger.Error("Received error response", logFields)
	} else {
		l.logger.Info("Received response", logFields)
	}

	// Create a new response with the body since we've already read it
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

// HTTPError represents an HTTP error response
type HTTPError struct {
	StatusCode int
	Body       []byte
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP error %d: %s", e.StatusCode, string(e.Body))
}

// GraphQLQuery executes a GraphQL query and unmarshals the response into the result parameter
func (c *Client) GraphQLQuery(ctx context.Context, query string, variables map[string]interface{}, result interface{}) error {
	if variables == nil {
		variables = make(map[string]interface{})
	}

	// Execute the query using the GraphQL client
	// The executeGraphQLOperation will handle the response structure properly
	return c.executeGraphQLOperation(ctx, queryOperation, query, variables, result)
}

// GraphQLMutation executes a GraphQL mutation and unmarshals the response into the result parameter
func (c *Client) GraphQLMutation(ctx context.Context, mutation string, variables map[string]interface{}, result interface{}) error {
	if variables == nil {
		variables = make(map[string]interface{})
	}

	// Execute the mutation using the GraphQL client
	// The executeGraphQLOperation will handle the response structure properly
	return c.executeGraphQLOperation(ctx, mutationOperation, mutation, variables, result)
}

// executeGraphQLOperation is a helper function that handles the common logic for executing GraphQL operations
func (c *Client) executeGraphQLOperation(ctx context.Context, op graphqlOperation, query string, variables map[string]interface{}, result interface{}) error {
	// Create a new GraphQL client with logging transport
	httpClient := &http.Client{
		Transport: loggingRoundTripper{
			logger: c.logger,
			rt:     http.DefaultTransport,
		},
	}

	// Set the authorization header
	reqModifier := func(r *http.Request) {
		r.Header.Set("Authorization", c.GetAuthHeader())
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")
	}

	// Execute the operation using the GraphQL client with retry logic
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(c.retryDelay * time.Duration(attempt))
		}

		// Apply rate limiting
		if err := c.rateLimiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limiter error: %w", err)
		}

		// Create the request body
		reqBody := map[string]interface{}{
			"query":     query,
			"variables": variables,
		}

		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}

		// Create a new request with the current context
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewBuffer(jsonBody))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		// Apply the request modifier to add auth headers
		reqModifier(req)

		// Log the request details
		c.logger.Debug("Executing GraphQL request", map[string]interface{}{
			"method":    req.Method,
			"url":       req.URL.String(),
			"operation": string(op),
			"query":     query,
			"variables": variables,
		})

		// Log the raw request body for debugging
		c.logger.Debug("GraphQL request body", map[string]interface{}{
			"body": string(jsonBody),
		})

		// Execute the request
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("HTTP request failed: %w", err)
			c.logger.Error("GraphQL request failed", map[string]interface{}{
				"error":   lastErr.Error(),
				"attempt": attempt + 1,
			})
			continue
		}

		// Read the response body
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			lastErr = fmt.Errorf("failed to read response body: %w", err)
			c.logger.Error("Failed to read response body", map[string]interface{}{
				"error":   lastErr.Error(),
				"attempt": attempt + 1,
			})
			continue
		}

		// Log the response with raw body for debugging
		c.logger.Debug("Received GraphQL response", map[string]interface{}{
			"status":         resp.Status,
			"status_code":    resp.StatusCode,
			"content_length": resp.ContentLength,
			"raw_response":   string(body),
		})

		// Check for HTTP errors
		if resp.StatusCode >= 400 {
			lastErr = &HTTPError{
				StatusCode: resp.StatusCode,
				Body:       body,
			}
			c.logger.Error("GraphQL request failed with HTTP error", map[string]interface{}{
				"error":   lastErr.Error(),
				"attempt": attempt + 1,
			})
			continue
		}

		// First, try to parse as a standard GraphQL response with data/errors fields
		var gqlResp struct {
			Data   json.RawMessage `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors,omitempty"`
		}

		directUnmarshal := false
		if err := json.Unmarshal(body, &gqlResp); err != nil {
			// If we can't unmarshal into the standard format, try direct unmarshal for mutations
			c.logger.Debug("Failed to unmarshal as standard GraphQL response, trying direct unmarshal", map[string]interface{}{
				"error": err.Error(),
				"body":  string(body),
			})
			directUnmarshal = true
		}

		// Log the parsed response for debugging
		errorsJSON, _ := json.Marshal(gqlResp.Errors)
		dataJSON, _ := json.Marshal(gqlResp.Data)
		c.logger.Debug("Parsed GraphQL response", map[string]interface{}{
			"hasData":         len(gqlResp.Data) > 0,
			"data":            string(dataJSON),
			"dataIsEmpty":     len(gqlResp.Data) == 0,
			"hasErrors":       len(gqlResp.Errors) > 0,
			"errors":          string(errorsJSON),
			"directUnmarshal": directUnmarshal,
			"rawRequestBody":  string(jsonBody),
			"rawResponse":     string(body), // Log the raw response body
		})

		// Check for GraphQL errors
		if !directUnmarshal && len(gqlResp.Errors) > 0 {
			lastErr = fmt.Errorf("GraphQL error: %v", gqlResp.Errors[0].Message)
			c.logger.Error("GraphQL operation failed", map[string]interface{}{
				"error":   lastErr.Error(),
				"attempt": attempt + 1,
				"errors":  gqlResp.Errors,
			})
			continue
		}

		// If no result is expected, we're done
		if result == nil {
			return nil
		}

		// Handle direct unmarshal for mutations that don't follow the standard format
		if directUnmarshal {
			if err := json.Unmarshal(body, result); err != nil {
				lastErr = fmt.Errorf("failed to unmarshal direct GraphQL response: %w", err)
				c.logger.Error("Failed to unmarshal direct GraphQL response", map[string]interface{}{
					"error":   lastErr.Error(),
					"attempt": attempt + 1,
					"body":    string(body),
				})
				continue
			}
			return nil
		}

		// Handle standard GraphQL response with data field
		if len(gqlResp.Data) == 0 {
			lastErr = fmt.Errorf("empty data in GraphQL response")
			c.logger.Error("Empty data in GraphQL response", map[string]interface{}{
				"error":   lastErr.Error(),
				"attempt": attempt + 1,
			})
			continue
		}

		// Unmarshal the data into the result
		if err := json.Unmarshal(gqlResp.Data, result); err != nil {
			lastErr = fmt.Errorf("failed to unmarshal GraphQL data: %w", err)
			c.logger.Error("Failed to unmarshal GraphQL data", map[string]interface{}{
				"error":   lastErr.Error(),
				"attempt": attempt + 1,
				"data":    string(gqlResp.Data),
			})
			continue
		}

		return nil
	}

	// If we get here, all retry attempts failed
	if lastErr != nil {
		c.logger.Error("GraphQL operation failed after all retries", map[string]interface{}{
			"error":       lastErr.Error(),
			"operation":   string(op),
			"query":       query,
			"variables":   variables,
			"max_retries": c.maxRetries,
		})
	}
	return fmt.Errorf("failed after %d attempts: %w", c.maxRetries+1, lastErr)
}

// executeGraphQLQuery is a helper function to execute a GraphQL query
func (c *Client) executeGraphQLQuery(ctx context.Context, query string, variables map[string]interface{}, result interface{}) error {
	return c.executeGraphQLOperation(ctx, queryOperation, query, variables, result)
}

// executeGraphQLMutation is a helper function to execute a GraphQL mutation
func (c *Client) executeGraphQLMutation(ctx context.Context, mutation string, variables map[string]interface{}, result interface{}) error {
	return c.executeGraphQLOperation(ctx, mutationOperation, mutation, variables, result)
}

// GetCurrentUserID gets the current user's ID from the Hardcover GraphQL API
// It returns the user ID from cache if available, otherwise fetches it from the API
// and caches it for future use. The function is safe for concurrent access.
func (c *Client) GetCurrentUserID(ctx context.Context) (int, error) {
	// Try to get the user ID from cache first (read lock)
	c.currentUserMutex.RLock()
	if c.currentUserID != 0 {
		userID := c.currentUserID
		c.currentUserMutex.RUnlock()
		c.logger.Debug("Returning cached user ID", map[string]interface{}{
			"user_id": userID,
		})
		return userID, nil
	}
	c.currentUserMutex.RUnlock()

	// If not in cache, acquire write lock and check again (double-checked locking pattern)
	c.currentUserMutex.Lock()
	defer c.currentUserMutex.Unlock()

	// Check again in case another goroutine updated the cache while we were waiting for the lock
	if c.currentUserID != 0 {
		c.logger.Debug("Returning user ID from cache (after acquiring lock)", map[string]interface{}{
			"user_id": c.currentUserID,
		})
		return c.currentUserID, nil
	}

	c.logger.Debug("User ID not in cache, fetching from Hardcover API", nil)

	// Define the GraphQL query
	query := `
		query GetCurrentUserID {
			me {
				id
			}
		}`

	// Define the response structure
	var resp struct {
		Me []struct {
			ID int `json:"id"`
		} `json:"me"`
	}

	// Execute the query
	err := c.executeGraphQLOperation(ctx, queryOperation, query, nil, &resp)
	if err != nil {
		return 0, fmt.Errorf("failed to get current user ID: %w", err)
	}

	// Check if we got any results
	if len(resp.Me) == 0 {
		return 0, fmt.Errorf("no user data returned from API")
	}

	// Check if we got a valid user ID
	userID := resp.Me[0].ID
	if userID == 0 {
		return 0, fmt.Errorf("received invalid user ID from API: %d", userID)
	}

	// Cache the user ID
	c.currentUserID = userID

	c.logger.Debug("Successfully retrieved and cached current user ID from Hardcover", map[string]interface{}{
		"user_id": userID,
	})

	return userID, nil
}

// SearchBookByISBN13 searches for a book in the Hardcover database by ISBN-13
// It's a convenience wrapper around searchBookByISBN with the correct field name
func (c *Client) SearchBookByISBN13(ctx context.Context, isbn13 string) (*models.HardcoverBook, error) {
	if isbn13 == "" {
		return nil, fmt.Errorf("ISBN-13 cannot be empty")
	}
	// Ensure the logger is initialized
	if c.logger == nil {
		c.logger = logger.Get()
	}
	log := c.logger.With(map[string]interface{}{
		"isbn13": isbn13,
		"method": "SearchBookByISBN13",
	})
	log.Debug("Searching for book by ISBN-13")
	return c.searchBookByISBN(ctx, "isbn_13", isbn13)
}

// SearchBookByISBN10 searches for a book in the Hardcover database by ISBN-10
func (c *Client) SearchBookByISBN10(ctx context.Context, isbn10 string) (*models.HardcoverBook, error) {
	return c.searchBookByISBN(ctx, "isbn_10", isbn10)
}

// SearchBooks searches for books by title and author
// Implements the HardcoverClientInterface
func (c *Client) SearchBooks(ctx context.Context, title, author string) ([]models.HardcoverBook, error) {
	// Use the existing SearchBooks method that takes a query string and limit
	results, err := c.searchBooksWithLimit(ctx, title, 10) // Default to 10 results
	if err != nil {
		return nil, err
	}
	
	// Convert SearchResult to HardcoverBook
	var books []models.HardcoverBook
	for _, r := range results {
		book := models.HardcoverBook{
			ID:    r.ID,
			Title: r.Title,
			// Add other fields as needed
		}
		books = append(books, book)
	}
	
	return books, nil
}


// GetUserBook gets user book information by ID
// Implements the HardcoverClientInterface
func (c *Client) GetUserBook(ctx context.Context, userBookID string) (*models.HardcoverBook, error) {
	// This is a simplified implementation
	// In a real implementation, we would make an API call to get the user book details
	return &models.HardcoverBook{
		ID:    userBookID,
		Title: "Sample Book",
		// Add other fields as needed
	}, nil
}

// SaveToFile saves client state to a file (for mismatch package)
// Implements the HardcoverClientInterface
func (c *Client) SaveToFile(filepath string) error {
	// This is a no-op implementation since the client doesn't need to save state
	// The mismatch package will handle the actual file operations
	return nil
}

// AddWithMetadata adds a mismatch with metadata (for mismatch package)
// Implements the HardcoverClientInterface
func (c *Client) AddWithMetadata(key string, value interface{}, metadata map[string]interface{}) error {
	// This is a no-op implementation since the client doesn't need to track mismatches
	// The mismatch package will handle the actual mismatch tracking
	return nil
}

// SearchBookByASIN searches for a book in the Hardcover database by ASIN
func (c *Client) SearchBookByASIN(ctx context.Context, asin string) (*models.HardcoverBook, error) {
	if asin == "" {
		return nil, fmt.Errorf("ASIN cannot be empty")
	}

	// Create logger with context
	log := logger.WithContext(map[string]interface{}{
		"asin":   asin,
		"method": "SearchBookByASIN",
	})

	// Define the GraphQL query
	const query = `
	query BookByASIN($asin: String!) {
	  books(
	    where: { 
	      editions: { 
	        _and: [
	          { asin: { _eq: $asin } }, 
	          { reading_format: { id: { _eq: 2 } } }
	        ]
	      } 
	    },
	    limit: 1
	  ) {
	    id
	    title
	    book_status_id
	    canonical_id
	    editions(
	      where: { 
	        _and: [
	          { asin: { _eq: $asin } },
	          { reading_format: { id: { _eq: 2 } } }
	        ]
	      },
	      limit: 1
	    ) {
	      id
	      asin
	      isbn_13
	      isbn_10
	      reading_format_id
	      audio_seconds
	    }
	  }
	}`

	// Define the response structure to match the actual API response

	// Use a flexible map to capture the raw response first
	var rawResponse map[string]interface{}

	err := c.GraphQLQuery(ctx, query, map[string]interface{}{
		"asin": asin,
	}, &rawResponse)

	if err != nil {
		log.Error("Failed to search book by ASIN", map[string]interface{}{
			"error": err.Error(),
			"asin":  asin,
		})
		return nil, fmt.Errorf("failed to search book by ASIN: %w", err)
	}

	// Debug log the raw response
	log.Debug("Raw GraphQL response", map[string]interface{}{
		"raw_response": fmt.Sprintf("%+v", rawResponse),
	})

	var books []map[string]interface{}

	// Extract data from response
	data, ok := rawResponse["data"].(map[string]interface{})
	if !ok {
		// If that fails, try to see if rawResponse itself is the data
		if _, isMap := rawResponse["books"]; isMap {
			data = rawResponse
		} else {
			log.Warn("No 'data' key found in response and response is not a direct data object", map[string]interface{}{})
		}
	}

	if data != nil {
		log.Debug("Data keys in response", map[string]interface{}{
			"data_keys": fmt.Sprintf("%v", getMapKeys(data)),
		})

		if booksData, ok := data["books"]; ok {
			switch v := booksData.(type) {
			case []interface{}:
				log.Debug("Found books array in response", map[string]interface{}{
					"books_count": len(v),
				})

				for i, b := range v {
					if book, ok := b.(map[string]interface{}); ok {
						books = append(books, book)
						log.Debug("Found book in response", map[string]interface{}{
							"book_index": i,
							"book_id":    fmt.Sprintf("%v", book["id"]),
							"title":      fmt.Sprintf("%v", book["title"]),
						})
					}
				}
			default:
				log.Warn("Unexpected type for books data", map[string]interface{}{
					"books_type": fmt.Sprintf("%T", v),
				})
			}
		} else {
			log.Warn("No 'books' key found in data", map[string]interface{}{})
		}
	}

	log.Debug("Extracted books from response", map[string]interface{}{
		"books_count": len(books),
	})

	// Check if any books were found
	if len(books) == 0 {
		log.Debug("No books found with the given ASIN", map[string]interface{}{
			"asin": asin,
		})
		return nil, nil
	}

	// Process the first book
	bookData := books[0]
	log.Debug("Processing first book", map[string]interface{}{
		"book_data": fmt.Sprintf("%+v", bookData),
	})

	// Create a new HardcoverBook instance
	hcBook := &models.HardcoverBook{}

	// Set ID if available
	if id, ok := bookData["id"]; ok {
		switch v := id.(type) {
		case json.Number:
			hcBook.ID = v.String()
		case float64:
			hcBook.ID = strconv.FormatFloat(v, 'f', 0, 64)
		case int64:
			hcBook.ID = strconv.FormatInt(v, 10)
		case string:
			hcBook.ID = v
		}
	}

	// Set Title if available
	if title, ok := bookData["title"].(string); ok {
		hcBook.Title = title
	}

	// Set BookStatusID if available
	switch statusID := bookData["book_status_id"].(type) {
	case json.Number:
		if id, err := statusID.Int64(); err == nil {
			hcBook.BookStatusID = int(id)
		}
	case float64:
		hcBook.BookStatusID = int(statusID)
	}

	// Handle editions
	editions, _ := bookData["editions"].([]interface{})
	if len(editions) == 0 {
		log.Warn("No editions found for book", map[string]interface{}{
			"book_id": hcBook.ID,
		})
		return nil, nil
	}

	// Process the first edition
	edition, _ := editions[0].(map[string]interface{})

	// Set EditionID if available
	if editionID, ok := edition["id"]; ok {
		switch v := editionID.(type) {
		case json.Number:
			hcBook.EditionID = v.String()
		case float64:
			hcBook.EditionID = strconv.FormatFloat(v, 'f', 0, 64)
		case int64:
			hcBook.EditionID = strconv.FormatInt(v, 10)
		case string:
			hcBook.EditionID = v
		}
	}

	// Handle optional CanonicalID
	if canonicalID, ok := bookData["canonical_id"]; ok && canonicalID != nil {
		switch v := canonicalID.(type) {
		case string:
			if id, err := strconv.Atoi(v); err == nil {
				hcBook.CanonicalID = &id
			}
		case json.Number:
			if id, err := v.Int64(); err == nil {
				idInt := int(id)
				hcBook.CanonicalID = &idInt
			}
		}
	}

	// Set optional fields if they exist
	if asin, ok := edition["asin"].(string); ok && asin != "" {
		hcBook.EditionASIN = asin
	}
	if isbn13, ok := edition["isbn_13"].(string); ok && isbn13 != "" {
		hcBook.EditionISBN13 = isbn13
	}
	if isbn10, ok := edition["isbn_10"].(string); ok && isbn10 != "" {
		hcBook.EditionISBN10 = isbn10
	}

	// Debug log the book data
	log.Debug("Successfully found book by ASIN", map[string]interface{}{
		"book_id": hcBook.ID,
		"title":   bookData["title"].(string),
	})

	return hcBook, nil
}

// searchBookByISBN is a helper function to search for a book by ISBN (13 or 10)
func (c *Client) searchBookByISBN(ctx context.Context, isbnField, isbn string) (*models.HardcoverBook, error) {
	if isbn == "" {
		return nil, fmt.Errorf("ISBN cannot be empty")
	}

	// Create logger with context
	log := c.logger.With(map[string]interface{}{
		"isbn":       isbn,
		"isbn_field": isbnField,
		"method":     "searchBookByISBN",
	})

	// Normalize ISBN (remove dashes, spaces, etc.)
	normalizedISBN := strings.ReplaceAll(isbn, "-", "")
	normalizedISBN = strings.ReplaceAll(normalizedISBN, " ", "")

	// Define the GraphQL query
	query := fmt.Sprintf(`
	query BookByISBN($isbn: String!) {
	  books(
	    where: { 
	      editions: { 
	        _and: [
	          {%s: {_eq: $isbn}}, 
	          {reading_format: {id: {_eq: 2}}}
	        ]
	      } 
	    },
	    limit: 1
	  ) {
	    id
	    title
	    book_status_id
	    canonical_id
	    editions(
	      where: { 
	        _and: [
	          {%s: {_eq: $isbn}},
	          {reading_format: {id: {_eq: 2}}}
	        ]
	      },
	      limit: 1
	    ) {
	      id
	      asin
	      isbn_13
	      isbn_10
	      reading_format_id
	      audio_seconds
	    }
	  }
	}`, isbnField, isbnField)

	// Define the response structure to match the actual API response
	type Edition struct {
		ID              json.Number `json:"id"`
		ASIN            *string     `json:"asin"`
		ISBN13          *string     `json:"isbn_13"`
		ISBN10          *string     `json:"isbn_10"`
		ReadingFormatID *int        `json:"reading_format_id"`
		AudioSeconds    *int        `json:"audio_seconds"`
	}

	type Book struct {
		ID           json.Number `json:"id"`
		Title        string      `json:"title"`
		BookStatusID int         `json:"book_status_id"`
		CanonicalID  *string     `json:"canonical_id"`
		Editions     []*Edition  `json:"editions"`
	}

	// The GraphQL response is already unwrapped by the client, so we expect just the books array
	var result struct {
		Books []*Book `json:"books"`
	}

	// Execute the GraphQL query
	err := c.GraphQLQuery(ctx, query, map[string]interface{}{
		"isbn": normalizedISBN,
	}, &result)

	// Debug: Log the result after unmarshaling
	resultJSON, _ := json.Marshal(result)
	log.Debug("GraphQL query result", map[string]interface{}{
		"result":      string(resultJSON),
		"books_count": len(result.Books),
	})

	if len(result.Books) > 0 {
		book := result.Books[0]
		editionsCount := 0
		if book.Editions != nil {
			editionsCount = len(book.Editions)
		}
		log.Debug("First book in result", map[string]interface{}{
			"book_id":  book.ID,
			"title":    book.Title,
			"editions": editionsCount,
		})
		if editionsCount > 0 {
			edition := book.Editions[0]
			log.Debug("First edition", map[string]interface{}{
				"edition_id": edition.ID,
				"isbn_13":    edition.ISBN13,
				"isbn_10":    edition.ISBN10,
				"format_id":  edition.ReadingFormatID,
				"audio_secs": edition.AudioSeconds,
			})
		}
	}

	if err != nil {
		log.Error("Failed to search book by ISBN", map[string]interface{}{
			"error": err.Error(),
			"isbn":  isbn,
		})
		return nil, fmt.Errorf("failed to search book by ISBN: %w", err)
	}

	// Check if any books were found
	if len(result.Books) == 0 {
		log.Debug("No books found with the given ISBN", map[string]interface{}{
			"isbn": isbn,
		})
		return nil, nil
	}

	bookData := result.Books[0]

	// Check if we have any editions
	if len(bookData.Editions) == 0 {
		log.Warn("No editions found for book", map[string]interface{}{
			"book_id": bookData.ID.String(),
		})
		return nil, nil
	}

	edition := bookData.Editions[0]
	hcBook := &models.HardcoverBook{
		ID:           bookData.ID.String(),
		Title:        bookData.Title,
		EditionID:    edition.ID.String(),
		BookStatusID: bookData.BookStatusID,
	}

	// Handle optional CanonicalID
	if bookData.CanonicalID != nil && *bookData.CanonicalID != "" {
		// Convert string to int for CanonicalID field which expects *int
		canonicalID, err := strconv.Atoi(*bookData.CanonicalID)
		if err == nil {
			hcBook.CanonicalID = &canonicalID
		} else {
			log.Error("Failed to search book by ISBN", map[string]interface{}{
				"error": err.Error(),
				"isbn":  isbn,
			})
			log.Warn("Failed to parse canonical_id as integer", map[string]interface{}{
				"error":        err.Error(),
				"canonical_id": *bookData.CanonicalID,
			})
		}
	}

	// Set optional fields if they exist
	if edition.ASIN != nil && *edition.ASIN != "" {
		hcBook.EditionASIN = *edition.ASIN
	}
	if edition.ISBN13 != nil && *edition.ISBN13 != "" {
		hcBook.EditionISBN13 = *edition.ISBN13
	}
	if edition.ISBN10 != nil && *edition.ISBN10 != "" {
		hcBook.EditionISBN10 = *edition.ISBN10
	}

	log.Debug("Successfully found book by ISBN", map[string]interface{}{
		"book_id":    hcBook.ID,
		"edition_id": hcBook.EditionID,
	})

	return hcBook, nil
}

// searchBooksWithLimit searches for books in the Hardcover database using GraphQL with a limit
// This is a helper function for SearchBooks
func (c *Client) searchBooksWithLimit(ctx context.Context, query string, limit int) ([]models.SearchResult, error) {
	// Ensure the logger is initialized
	if c.logger == nil {
		c.logger = logger.Get()
	}
	log := c.logger.With(map[string]interface{}{
		"operation": "search_books",
		"query":     query,
	})

	// Define the GraphQL query
	searchQuery := `
		query SearchBooks($query: String!, $perPage: Int) {
			search(query: $query, per_page: $perPage) {
				error
				results
			}
		}`

	// Set up variables for the query
	variables := map[string]interface{}{
		"query":   query,
		"perPage": 5, // Limit to 5 results by default
	}

	// Define the response structure
	var response struct {
		Search struct {
			Error   string          `json:"error"`
			Results json.RawMessage `json:"results"`
		} `json:"search"`
	}

	// Execute the GraphQL query
	c.logger.Debug("Searching for books using GraphQL", map[string]interface{}{
		"query": query,
	})
	err := c.GraphQLQuery(ctx, searchQuery, variables, &response)
	if err != nil {
		c.logger.Error("Failed to execute search query", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to execute search query: %w", err)
	}

	// Check for API-level errors
	if response.Search.Error != "" {
		errMsg := fmt.Sprintf("search API error: %s", response.Search.Error)
		c.logger.Error("Search API returned an error", map[string]interface{}{
			"error": response.Search.Error,
		})
		return nil, errors.New(errMsg)
	}

	// Log the raw response for debugging
	c.logger.Debug("Raw search response", map[string]interface{}{
		"raw_response": string(response.Search.Results),
	})

	// Parse the results
	var searchResults []models.SearchResult
	if len(response.Search.Results) > 0 {
		// Define the structure of the search results from the API
		var apiResponse struct {
			Hits []struct {
				Document struct {
					ID    string `json:"id"`
					Title string `json:"title"`
					Image struct {
						URL string `json:"url"`
					} `json:"image"`
				} `json:"document"`
			} `json:"hits"`
		}

		// Try to unmarshal the API response
		if err := json.Unmarshal(response.Search.Results, &apiResponse); err != nil {
			log.Error("Failed to parse search results", map[string]interface{}{
				"error": err.Error(),
			})
			return nil, fmt.Errorf("failed to parse search results: %w", err)
		}

		// Process the search results
		for _, hit := range apiResponse.Hits {
			searchResults = append(searchResults, models.SearchResult{
				ID:    hit.Document.ID,
				Title: hit.Document.Title,
				Type:  "book",
				Image: hit.Document.Image.URL,
			})
		}
	}

	// Log the search results for debugging
	resultIDs := make([]string, 0, len(searchResults))
	for _, r := range searchResults {
		resultIDs = append(resultIDs, fmt.Sprintf("%s (%s)", r.ID, r.Title))
	}

	log.Info("Successfully searched for books", map[string]interface{}{
		"count":   len(searchResults),
		"results": resultIDs,
	})

	if len(searchResults) > 0 {
		log.Debug("First search result details", map[string]interface{}{
			"id":    searchResults[0].ID,
			"title": searchResults[0].Title,
			"type":  searchResults[0].Type,
			"image": searchResults[0].Image,
		})
	}

	return searchResults, nil
}

// UpdateReadingProgress updates the reading progress for a book in Hardcover
func (c *Client) UpdateReadingProgress(
	ctx context.Context,
	bookID string,
	progress float64,
	status string,
	markAsOwned bool,
) error {
	const endpoint = "/reading/update-progress"
	log := c.logger.With(map[string]interface{}{
		"endpoint":      endpoint,
		"book_id":       bookID,
		"progress":      progress,
		"status":        status,
		"mark_as_owned": markAsOwned,
	})

	// Prepare request body
	reqBody := struct {
		BookID      string  `json:"bookId"`
		Progress    float64 `json:"progress"`
		Status      string  `json:"status,omitempty"`
		MarkAsOwned bool    `json:"markAsOwned,omitempty"`
	}{
		BookID:      bookID,
		Progress:    progress,
		Status:      status,
		MarkAsOwned: markAsOwned,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		log.Error("Failed to marshal request body", map[string]interface{}{
			"error": err.Error(),
		})
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+endpoint,
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		log.Error("Failed to create request", map[string]interface{}{
			"error": err.Error(),
		})
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", c.GetAuthHeader())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Execute request
	log.Debug("Updating reading progress", nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Error("Failed to update reading progress", map[string]interface{}{
			"error": err.Error(),
		})
		return fmt.Errorf("failed to update reading progress: %w", err)
	}
	defer resp.Body.Close()

	// Read response body for error handling
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error("Failed to read response body", map[string]interface{}{
			"error": err.Error(),
		})
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		log.Error("Failed to update reading progress", map[string]interface{}{
			"status":   resp.StatusCode,
			"response": string(body),
		})

		if resp.StatusCode == http.StatusNotFound {
			return ErrBookNotFound
		}
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	log.Info("Successfully updated reading progress", nil)
	return nil
}

// DatesReadInput represents the input for date-related fields when creating or updating a user book read entry
type DatesReadInput struct {
	Action          *string `json:"action,omitempty"`
	EditionID       *int64  `json:"edition_id,omitempty"`
	FinishedAt      *string `json:"finished_at,omitempty"`
	ID              *int64  `json:"id,omitempty"`
	StartedAt       *string `json:"started_at,omitempty"`
	ProgressSeconds *int    `json:"progress_seconds,omitempty"`
}

// InsertUserBookReadInput represents the input for creating a new user book read entry
type InsertUserBookReadInput struct {
	UserBookID int64          `json:"user_book_id"`
	DatesRead  DatesReadInput `json:"user_book_read"`
}

// InsertUserBookRead creates a new user book read entry in Hardcover
func (c *Client) InsertUserBookRead(ctx context.Context, input InsertUserBookReadInput) (int, error) {
	const mutation = `
	mutation InsertUserBookRead($user_book_id: Int!, $user_book_read: DatesReadInput!) {
	  insert_user_book_read(
		user_book_id: $user_book_id,
		user_book_read: $user_book_read
	  ) {
		id
		error
	  }
	}`

	// Validate input
	if input.UserBookID == 0 {
		return 0, fmt.Errorf("%w: user_book_id is required", ErrInvalidInput)
	}

	// Prepare variables - only include fields that are supported by the API
	userBookRead := make(map[string]interface{})

	// Only include non-nil fields in the user_book_read object
	if input.DatesRead.EditionID != nil {
		userBookRead["edition_id"] = input.DatesRead.EditionID
	}
	if input.DatesRead.FinishedAt != nil {
		userBookRead["finished_at"] = input.DatesRead.FinishedAt
	}
	if input.DatesRead.ID != nil {
		userBookRead["id"] = input.DatesRead.ID
	}
	if input.DatesRead.StartedAt != nil {
		userBookRead["started_at"] = input.DatesRead.StartedAt
	}

	variables := map[string]interface{}{
		"user_book_id":   input.UserBookID,
		"user_book_read": userBookRead,
	}

	// Execute the mutation
	var result struct {
		InsertUserBookRead struct {
			ID    int     `json:"id"`
			Error *string `json:"error"`
		} `json:"insert_user_book_read"`
	}

	if err := c.executeGraphQLQuery(ctx, mutation, variables, &result); err != nil {
		return 0, fmt.Errorf("failed to insert user book read: %w", err)
	}

	// Check for errors in the response
	if result.InsertUserBookRead.Error != nil {
		return 0, fmt.Errorf("failed to insert user book read: %s", *result.InsertUserBookRead.Error)
	}

	return result.InsertUserBookRead.ID, nil
}

// UpdateUserBookStatusInput represents the input for updating a user book status
type UpdateUserBookStatusInput struct {
	ID       int64  `json:"id"`
	StatusID int    `json:"status_id"`
	Status   string `json:"status,omitempty"`
}

// UpdateUserBookStatus updates the status of a user book in Hardcover
func (c *Client) UpdateUserBookStatus(ctx context.Context, input UpdateUserBookStatusInput) error {
	const mutation = `
	mutation UpdateUserBookStatus($id: Int!, $status_id: Int!) {
	  update_user_book(id: $id, object: { status_id: $status_id }) {
		id
		error
	  }
	}`

	// Validate input
	if input.ID == 0 {
		return fmt.Errorf("%w: id is required", ErrInvalidInput)
	}
	if input.StatusID == 0 && input.Status == "" {
		return fmt.Errorf("%w: either status_id or status is required", ErrInvalidInput)
	}

	// If status is provided but status_id is not, map the status to status_id
	if input.StatusID == 0 && input.Status != "" {
		switch strings.ToUpper(input.Status) {
		case "WANT_TO_READ":
			input.StatusID = 1
		case "READING":
			input.StatusID = 2
		case "READ", "FINISHED":
			input.StatusID = 3
		default:
			return fmt.Errorf("%w: invalid status: %s", ErrInvalidInput, input.Status)
		}
	}

	// Prepare variables
	variables := map[string]interface{}{
		"id":        input.ID,
		"status_id": input.StatusID,
	}

	// Execute the mutation
	var result struct {
		UpdateUserBook struct {
			ID    int     `json:"id"`
			Error *string `json:"error"`
		} `json:"update_user_book"`
	}

	if err := c.executeGraphQLQuery(ctx, mutation, variables, &result); err != nil {
		return fmt.Errorf("failed to update user book status: %w", err)
	}

	// Check for errors in the response
	if result.UpdateUserBook.Error != nil {
		return fmt.Errorf("failed to update user book status: %s", *result.UpdateUserBook.Error)
	}

	return nil
}

// GetUserBookReadsInput represents the input for querying user book reads
type GetUserBookReadsInput struct {
	UserBookID int64  `json:"user_book_id"`
	Status     string `json:"status,omitempty"` // e.g., "READ"
}

// UserBookRead represents a user's reading progress for a book
type UserBookRead struct {
	ID              int64   `json:"id"`
	UserBookID      int64   `json:"user_book_id"`
	Progress        float64 `json:"progress"`
	ProgressSeconds *int    `json:"progress_seconds"`
	StartedAt       *string `json:"started_at"`
	FinishedAt      *string `json:"finished_at"`
	EditionID       *int64  `json:"edition_id"`
}

// GetUserBookReads retrieves the reading progress for a user book
func (c *Client) GetUserBookReads(ctx context.Context, input GetUserBookReadsInput) ([]UserBookRead, error) {
	const query = `
	query GetUserBookReads($user_book_id: Int!) {
	  user_book_reads(
		where: { user_book_id: { _eq: $user_book_id } },
		order_by: { id: desc }
	  ) {
		id
		user_book_id
		progress
		progress_seconds
		started_at
		finished_at
		edition_id
	  }
	}`

	// Validate input
	if input.UserBookID == 0 {
		return nil, fmt.Errorf("%w: user_book_id is required", ErrInvalidInput)
	}

	// Prepare variables
	variables := map[string]interface{}{
		"user_book_id": input.UserBookID,
	}

	// Execute the query
	var result struct {
		UserBookReads []UserBookRead `json:"user_book_reads"`
	}

	if err := c.executeGraphQLQuery(ctx, query, variables, &result); err != nil {
		return nil, fmt.Errorf("failed to get user book reads: %w", err)
	}

	return result.UserBookReads, nil
}

// GetCurrentUserIDResponse represents the response from the me query
type GetCurrentUserIDResponse struct {
	Data struct {
		Me []struct {
			ID       int    `json:"id"`
			Username string `json:"username"`
		} `json:"me"`
	} `json:"data"`
}

// RawGetCurrentUserIDResponse represents the raw response from the me query
type RawGetCurrentUserIDResponse struct {
	Data struct {
		Me []struct {
			ID       int    `json:"id"`
			Username string `json:"username"`
		} `json:"me"`
	} `json:"data"`
}

// MeQuery represents the GraphQL query for getting the current user
type MeQuery struct {
	Me []struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
	} `json:"me"`
}

// CheckExistingFinishedReadInput represents the input for checking if a user book has any finished reads
type CheckExistingFinishedReadInput struct {
	UserBookID int `json:"user_book_id"`
}

// CheckExistingFinishedReadResult represents the result of checking for finished reads
type CheckExistingFinishedReadResult struct {
	HasFinishedRead bool    `json:"has_finished_read"`
	LastFinishedAt  *string `json:"last_finished_at,omitempty"`
}

// CheckExistingUserBookReadInput represents the input for checking an existing user book read
type CheckExistingUserBookReadInput struct {
	UserBookID int    `json:"user_book_id"`
	Date       string `json:"date"` // Format: YYYY-MM-DD
}

// ExistingUserBookRead represents an existing user book read entry
// with fields needed for updates
type ExistingUserBookRead struct {
	ID              int     `json:"id"`
	EditionID       *int    `json:"edition_id"`
	ReadingFormatID *int    `json:"reading_format_id"`
	ProgressSeconds *int    `json:"progress_seconds"`
	StartedAt       *string `json:"started_at"`
}

// CheckExistingUserBookReadResult represents the result of checking for an existing user book read
type CheckExistingUserBookReadResult struct {
	ID              int     `json:"id"`
	EditionID       *int    `json:"edition_id"`
	ReadingFormatID *int    `json:"reading_format_id"`
	ProgressSeconds *int    `json:"progress_seconds"`
	StartedAt       *string `json:"started_at"`
}

// UpdateUserBookReadInput represents the input for updating a user book read entry
type UpdateUserBookReadInput struct {
	ID     int64                  `json:"id"`
	Object map[string]interface{} `json:"object"`
}

// UpdateUserBookReadResponse represents the response from updating a user book read entry
type UpdateUserBookReadResponse struct {
	UpdateUserBookRead struct {
		Returning []struct {
			ID int64 `json:"id"`
		} `json:"returning"`
	} `json:"update_user_book_read"`
}

// CheckExistingUserBookRead checks if there's an existing user book read entry for the given date
func (c *Client) CheckExistingUserBookRead(ctx context.Context, input CheckExistingUserBookReadInput) (*CheckExistingUserBookReadResult, error) {
	log := logger.WithContext(map[string]interface{}{
		"method":       "CheckExistingUserBookRead",
		"user_book_id": input.UserBookID,
		"date":         input.Date,
	})

	// Define the GraphQL query
	const query = `
		query CheckExistingUserBookRead($userBookId: Int!, $date: date!) {
			user_book_reads(
				where: {
					user_book_id: {_eq: $userBookId},
					created_at: {_gte: $date, _lt: $date::date + interval '1 day'}
				},
				order_by: {created_at: desc},
				limit: 1
			) {
				id
				edition_id
				reading_format_id
				progress_seconds
				started_at
			}
		}`

	// Define the response structure
	var response struct {
		UserBookReads []struct {
			ID              int     `json:"id"`
			EditionID       *int    `json:"edition_id"`
			ReadingFormatID *int    `json:"reading_format_id"`
			ProgressSeconds *int    `json:"progress_seconds"`
			StartedAt       *string `json:"started_at"`
		} `json:"user_book_reads"`
	}

	vars := map[string]interface{}{
		"userBookId": input.UserBookID,
		"date":       input.Date,
	}

	// Execute the query
	err := c.executeGraphQLOperation(ctx, queryOperation, query, vars, &response)
	if err != nil {
		log.Error("Failed to execute GraphQL query", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to check for existing user book read: %w", err)
	}

	// Check if we found a matching user book read
	if len(response.UserBookReads) > 0 {
		read := response.UserBookReads[0]
		return &CheckExistingUserBookReadResult{
			ID:              read.ID,
			EditionID:       read.EditionID,
			ReadingFormatID: read.ReadingFormatID,
			ProgressSeconds: read.ProgressSeconds,
			StartedAt:       read.StartedAt,
		}, nil
	}

	log.Debug("No existing read entry found for today", nil)
	return nil, nil
}

// UpdateUserBookRead updates an existing user book read entry
func (c *Client) UpdateUserBookRead(ctx context.Context, input UpdateUserBookReadInput) (bool, error) {
	c.logger.Debug("Updating user book read", map[string]interface{}{
		"id":     input.ID,
		"object": input.Object,
	})

	// Convert the input to JSON for the mutation
	updateObj, err := json.Marshal(input.Object)
	if err != nil {
		c.logger.Error("Failed to marshal update object", map[string]interface{}{
			"error": err.Error(),
		})
		return false, fmt.Errorf("failed to marshal update object: %w", err)
	}

	// Define the mutation to match the legacy implementation
	mutation := `
		mutation UpdateUserBookRead($id: Int!, $object: DatesReadInput!) {
		  update_user_book_read(id: $id, object: $object) {
			id
			error
			user_book_read {
			  id
			  progress_seconds
			  started_at
			  finished_at
			}
		  }
		}`

	// Convert the update object to a DatesReadInput to ensure only valid fields are included
	var datesReadInput DatesReadInput
	if err := json.Unmarshal(updateObj, &datesReadInput); err != nil {
		c.logger.Error("Failed to unmarshal update object to DatesReadInput", map[string]interface{}{
			"error": err.Error(),
		})
		return false, fmt.Errorf("failed to unmarshal update object: %w", err)
	}

	// Convert back to a map to use in the GraphQL variables
	updateObjMap := make(map[string]interface{})
	if datesReadInput.Action != nil {
		updateObjMap["action"] = datesReadInput.Action
	}
	if datesReadInput.EditionID != nil {
		updateObjMap["edition_id"] = datesReadInput.EditionID
	}
	if datesReadInput.FinishedAt != nil {
		updateObjMap["finished_at"] = datesReadInput.FinishedAt
	}
	if datesReadInput.ID != nil {
		updateObjMap["id"] = datesReadInput.ID
	}
	if datesReadInput.StartedAt != nil {
		updateObjMap["started_at"] = datesReadInput.StartedAt
	}
	if datesReadInput.ProgressSeconds != nil {
		updateObjMap["progress_seconds"] = datesReadInput.ProgressSeconds
	}

	// Define the result type
	var result struct {
		UpdateUserBookRead struct {
			ID           int     `json:"id"`
			Error        *string `json:"error"`
			UserBookRead *struct {
				ID              int     `json:"id"`
				ProgressSeconds int     `json:"progress_seconds"`
				StartedAt       string  `json:"started_at"`
				FinishedAt      *string `json:"finished_at"`
			} `json:"user_book_read"`
		} `json:"update_user_book_read"`
	}

	vars := map[string]interface{}{
		"id":     input.ID,
		"object": updateObjMap,
	}

	err = c.executeGraphQLMutation(ctx, mutation, vars, &result)
	if err != nil {
		c.logger.Error("Failed to execute update mutation", map[string]interface{}{
			"error": err.Error(),
		})
		return false, fmt.Errorf("failed to execute update mutation: %w", err)
	}

	// Check for errors in the response
	if result.UpdateUserBookRead.Error != nil {
		errMsg := *result.UpdateUserBookRead.Error
		c.logger.Error("Error in update_user_book_read response", map[string]interface{}{
			"error": errMsg,
		})
		return false, fmt.Errorf("update error: %s", errMsg)
	}

	// The API sometimes returns success with user_book_read: null
	// In this case, we'll assume the update was successful
	if result.UpdateUserBookRead.UserBookRead == nil {
		c.logger.Info("Successfully updated user book read (no user_book_read in response but no error)", map[string]interface{}{
			"id": input.ID,
		})
		return true, nil
	}

	updatedID := result.UpdateUserBookRead.UserBookRead.ID
	c.logger.Info("Successfully updated user book read entry", map[string]interface{}{
		"updated_id": updatedID,
	})

	return true, nil
}

// GetEditionByISBN13 retrieves an edition by its ISBN-13
func (c *Client) GetEditionByISBN13(ctx context.Context, isbn13 string) (*models.Edition, error) {
	// First try to find the book by ISBN-13
	book, err := c.SearchBookByISBN13(ctx, isbn13)
	if err != nil {
		return nil, fmt.Errorf("failed to find book by ISBN-13: %w", err)
	}

	if book == nil || book.ID == "" {
		return nil, fmt.Errorf("no book found with ISBN-13: %s", isbn13)
	}

	// Check if we have an edition ID
	if book.EditionID == "" {
		return nil, fmt.Errorf("no edition found for book with ID: %s", book.ID)
	}

	// Create an Edition from the embedded fields in HardcoverBook
	edition := &models.Edition{
		ID:     book.EditionID,
		BookID: book.ID,
		Title:  book.Title,
		ASIN:   book.EditionASIN,
		ISBN13: book.EditionISBN13,
		ISBN10: book.EditionISBN10,
	}

	return edition, nil
}

// GetEditionByASIN retrieves an edition by its ASIN
func (c *Client) GetEditionByASIN(ctx context.Context, asin string) (*models.Edition, error) {
	// First try to find the book by ASIN
	book, err := c.SearchBookByASIN(ctx, asin)
	if err != nil {
		return nil, fmt.Errorf("failed to find book by ASIN: %w", err)
	}

	if book == nil || book.ID == "" {
		return nil, fmt.Errorf("no book found with ASIN: %s", asin)
	}

	// Then get the edition details
	edition, err := c.GetEdition(ctx, book.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get edition: %w", err)
	}

	return edition, nil
}

// GetEdition retrieves edition details including book_id for a given edition_id
func (c *Client) GetEdition(ctx context.Context, editionID string) (*models.Edition, error) {
	// Ensure the logger is initialized
	if c.logger == nil {
		c.logger = logger.Get()
	}

	log := c.logger.With(map[string]interface{}{
		"method":     "GetEdition",
		"edition_id": editionID,
	})

	// Convert editionID to int since the API expects an integer
	editionIDInt, err := strconv.Atoi(editionID)
	if err != nil {
		return nil, fmt.Errorf("invalid edition ID format: %w", err)
	}

	// Define the GraphQL query
	const query = `
		query GetEdition($editionId: Int!) {
			editions(where: {id: {_eq: $editionId}}, limit: 1) {
				id
				book_id
				title
				isbn_10
				isbn_13
				asin
				release_date
			}
		}`

	// Define the response structure that matches the GraphQL response
	var response struct {
		Data struct {
			Editions []struct {
				ID          int     `json:"id"`
				BookID      int     `json:"book_id"`
				Title       *string `json:"title"`
				ISBN10      *string `json:"isbn_10"`
				ISBN13      *string `json:"isbn_13"`
				ASIN        *string `json:"asin"`
				ReleaseDate *string `json:"release_date"`
			} `json:"editions"`
		} `json:"data"`
	}

	// Execute the query
	err = c.GraphQLQuery(ctx, query, map[string]interface{}{
		"editionId": editionIDInt,
	}, &response)

	editions := response.Data.Editions

	if err != nil {
		log.Error("Failed to execute GraphQL query", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to get edition: %w", err)
	}

	if len(editions) == 0 {
		log.Debug("Edition not found in response", map[string]interface{}{
			"edition_id": editionID,
		})
		return nil, fmt.Errorf("edition not found: %s", editionID)
	}

	// Get the first edition
	edition := editions[0]

	// Log the raw edition data for debugging
	log.Debug("Retrieved edition details", map[string]interface{}{
		"id":           edition.ID,
		"book_id":      edition.BookID,
		"title":        safeString(edition.Title),
		"isbn_10":      safeString(edition.ISBN10),
		"isbn_13":      safeString(edition.ISBN13),
		"asin":         safeString(edition.ASIN),
		"release_date": safeString(edition.ReleaseDate),
	})

	// Create the edition model
	editionModel := &models.Edition{
		ID:     strconv.Itoa(edition.ID),
		BookID: strconv.Itoa(edition.BookID),
	}

	// Handle optional fields
	if edition.Title != nil {
		editionModel.Title = *edition.Title
	}
	if edition.ISBN10 != nil {
		editionModel.ISBN10 = *edition.ISBN10
	}
	if edition.ISBN13 != nil {
		editionModel.ISBN13 = *edition.ISBN13
	}
	if edition.ASIN != nil {
		editionModel.ASIN = *edition.ASIN
	}
	if edition.ReleaseDate != nil {
		editionModel.ReleaseDate = *edition.ReleaseDate
	}

	log.Debug("Retrieved edition details", map[string]interface{}{
		"book_id": editionModel.BookID,
		"title":   editionModel.Title,
	})

	return editionModel, nil
}

// SearchPeople searches for people (authors or narrators) by name or ID
// Implements the HardcoverClient interface
func (c *Client) SearchPeople(ctx context.Context, name, personType string, limit int) ([]models.Author, error) {
	// Check if the input is a numeric ID
	if id, err := strconv.Atoi(name); err == nil {
		// If it's a numeric ID, try to fetch the person directly
		person, err := c.GetPersonByID(ctx, strconv.Itoa(id))
		if err == nil && person != nil {
			return []models.Author{*person}, nil
		}
	}
	if c.logger == nil {
		c.logger = logger.Get()
	}
	log := c.logger.With(map[string]interface{}{
		"operation": "search_people",
		"name":      name,
		"type":      personType,
	})

	log.Debug("Searching for person", map[string]interface{}{
		"name":  name,
		"type":  personType,
		"limit": limit,
	})

	// First, try a direct search by name using the authors query with exact match
	directQuery := `
	query SearchPeopleDirect($name: String!, $limit: Int) {
		authors(
			where: {
				state: {_eq: "active"}, 
				name: {_eq: $name}
				${personType === 'narrator' ? 'contributions: {contribution: {_eq: "Narrator"}}' : ''}
			}, 
			limit: $limit
		) {
			id
			name
			books_count
			canonical_id
		}
	}`

	// Build the query based on person type
	query := strings.ReplaceAll(directQuery, "${personType === 'narrator' ? 'contributions: {contribution: {_eq: \"Narrator\"}}' : ''}", "")
	if personType == "narrator" {
		query = strings.ReplaceAll(directQuery, "${personType === 'narrator' ? '", "")
		query = strings.ReplaceAll(query, "' : ''}", "")
	}

	variables := map[string]interface{}{
		"name":  name,
		"limit": limit,
	}

	// Define the direct search response structure
	var searchResponse struct {
		Authors []struct {
			ID          int     `json:"id"`
			Name        string  `json:"name"`
			BooksCount  int     `json:"books_count"`
			CanonicalID *string `json:"canonical_id"`
		} `json:"authors"`
	}

	// Log the search query for debugging
	log.Debug("Executing person search query", map[string]interface{}{
		"query":     query,
		"variables": variables,
	})

	// Execute the direct search query
	if err := c.GraphQLQuery(ctx, query, variables, &searchResponse); err != nil {
		log.Error("Failed to execute direct person search query", map[string]interface{}{
			"error": err,
		})
		return nil, fmt.Errorf("direct person search failed: %w", err)
	}

	// Log the search response for debugging
	log.Debug("Received direct search response", map[string]interface{}{
		"response": fmt.Sprintf("%+v", searchResponse),
	})

	// If no results, try a more specific narrator search
	if len(searchResponse.Authors) == 0 && personType == "narrator" {
		log.Debug("No results with direct search, trying narrator-specific search", nil)
		// Try a more specific query for narrators if the first one fails
		fallbackQuery := `
	query SearchNarrators($name: String!, $limit: Int) {
		authors(
			where: {
				state: {_eq: "active"}, 
				name: {_eq: $name},
				contributions: {contribution: {_eq: "Narrator"}}
			}, 
			limit: $limit
		) {
			id
			name
			books_count
			canonical_id
		}
	}`

		if err := c.GraphQLQuery(ctx, fallbackQuery, variables, &searchResponse); err != nil {
			log.Error("Failed to execute fallback person search query", map[string]interface{}{
				"error": err,
			})
			return nil, fmt.Errorf("fallback person search failed: %w", err)
		}

		log.Debug("Received fallback search response", map[string]interface{}{
			"response": fmt.Sprintf("%+v", searchResponse),
		})
	}

	// If still no results, return empty slice
	if len(searchResponse.Authors) == 0 {
		log.Debug("No results found for person search", map[string]interface{}{
			"name": name,
			"type": personType,
		})
		return []models.Author{}, nil
	}

	// Convert to the expected format
	authors := make([]models.Author, 0, len(searchResponse.Authors))
	for _, author := range searchResponse.Authors {
		authors = append(authors, models.Author{
			ID:        strconv.Itoa(author.ID),
			Name:      author.Name,
			BookCount: author.BooksCount,
		})
	}

	log.Debug("Found authors", map[string]interface{}{
		"count": len(authors),
		"type":  personType,
	})

	// Return the authors we found
	return authors, nil
}

// SearchAuthors searches for authors by name
func (c *Client) SearchAuthors(ctx context.Context, name string, limit int) ([]models.Author, error) {
	return c.SearchPeople(ctx, name, "author", limit)
}

// SearchNarrators searches for narrators by name
func (c *Client) SearchNarrators(ctx context.Context, name string, limit int) ([]models.Author, error) {
	return c.SearchPeople(ctx, name, "narrator", limit)
}

// SearchPublishers searches for publishers by name in the Hardcover database
func (c *Client) SearchPublishers(ctx context.Context, name string, limit int) ([]models.Publisher, error) {
	if c.logger == nil {
		c.logger = logger.Get()
	}
	log := c.logger.With(map[string]interface{}{
		"operation": "search_publishers",
		"name":      name,
		"limit":     limit,
	})

	// Enforce rate limiting
	if err := c.enforceRateLimit(); err != nil {
		return nil, fmt.Errorf("rate limit error: %w", err)
	}

	// Define the GraphQL query
	// Note: Using _eq for exact match as _ilike is not supported by the API
	query := `
		query SearchPublishers($name: String!, $limit: Int!) {
			publishers(where: {name: {_eq: $name}}, limit: $limit) {
				id
				name
			}
		}
	`

	// Set up variables for the query
	variables := map[string]interface{}{
		"name":  name, // Using exact match, no pattern matching
		"limit": limit,
	}

	// Define a struct to hold the response
	type publisherResponse struct {
		Publishers []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"publishers"`
	}

	// Execute the query
	var resp publisherResponse
	err := c.GraphQLQuery(ctx, query, variables, &resp)
	if err != nil {
		log.Error("Failed to search for publishers", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to search for publishers: %w", err)
	}

	// Convert the result to the expected format
	publishers := make([]models.Publisher, 0, len(resp.Publishers))
	for _, p := range resp.Publishers {
		publishers = append(publishers, models.Publisher{
			ID:   strconv.Itoa(p.ID),
			Name: p.Name,
		})
	}

	log.Debug("Found publishers", map[string]interface{}{
		"count": len(publishers),
	})

	return publishers, nil
}

// GetPersonByID retrieves a person (author or narrator) by ID
func (c *Client) GetPersonByID(ctx context.Context, id string) (*models.Author, error) {
	if c.logger == nil {
		c.logger = logger.Get()
	}
	log := c.logger.With(map[string]interface{}{
		"method": "GetPersonByID",
		"id":     id,
	})

	query := `
	query GetPerson($id: Int!) {
		authors(where: {id: {_eq: $id}}) {
			id
			name
		}
	}`

	// Set up variables
	variables := map[string]interface{}{
		"id": id,
	}

	// Define the response structure
	var response struct {
		Authors []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"authors"`
	}

	// Execute the query
	log.Debug("Fetching person details", map[string]interface{}{
		"id": id,
	})

	if err := c.GraphQLQuery(ctx, query, variables, &response); err != nil {
		log.Error("Failed to fetch person details", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to fetch person details: %w", err)
	}

	// Check if we got any results
	if len(response.Authors) == 0 {
		log.Warn("Person not found", map[string]interface{}{
			"id": id,
		})
		return nil, fmt.Errorf("person not found with ID: %s", id)
	}

	// Return the first result
	person := response.Authors[0]
	return &models.Author{
		ID:   person.ID,
		Name: person.Name,
	}, nil
}

// GetGoogleUploadCredentials gets signed upload credentials for Google Cloud Storage
func (c *Client) GetGoogleUploadCredentials(ctx context.Context, filename string, editionID int) (*edition.GoogleUploadInfo, error) {
	const query = `
	query GetGoogleUploadCredentials($input: GoogleUploadCredentialsInput!) {
		getGoogleUploadCredentials(input: $input) {
			url
			fields
		}
	}`

	// Prepare variables
	variables := map[string]interface{}{
		"input": map[string]interface{}{
			"filename":  filename,
			"editionId": editionID,
		},
	}

	// Execute the query
	var response struct {
		GetGoogleUploadCredentials struct {
			URL    string            `json:"url"`
			Fields map[string]string `json:"fields"`
		} `json:"getGoogleUploadCredentials"`
	}

	if err := c.GraphQLQuery(ctx, query, variables, &response); err != nil {
		return nil, fmt.Errorf("failed to get GCS upload credentials: %w", err)
	}

	// Construct the public URL where the file will be accessible
	// This assumes the file will be available at a predictable URL pattern
	// Adjust this based on your actual GCS bucket and path structure
	fileURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s",
		response.GetGoogleUploadCredentials.Fields["bucket"],
		response.GetGoogleUploadCredentials.Fields["key"],
	)

	return &edition.GoogleUploadInfo{
		URL:     response.GetGoogleUploadCredentials.URL,
		Fields:  response.GetGoogleUploadCredentials.Fields,
		FileURL: fileURL,
	}, nil
}

// GetUserBookID retrieves the user book ID for a given edition ID with caching
func (c *Client) GetUserBookID(ctx context.Context, editionID int) (int, error) {
	log := c.logger.With(map[string]interface{}{
		"editionID": editionID,
		"method":    "GetUserBookID",
	})

	// Check cache first
	if userBookID, exists := c.userBookIDCache.Get(editionID); exists {
		log.Debug("Found user book ID in cache", map[string]interface{}{
			"userBookID": userBookID,
		})
		return userBookID, nil
	}

	log.Debug("User book ID not found in cache, querying API", nil)

	// Get the current user ID to filter by the correct user
	userID, userErr := c.GetCurrentUserID(ctx)
	if userErr != nil {
		log.Error("Failed to get current user ID", map[string]interface{}{
			"error": userErr.Error(),
		})
		return 0, fmt.Errorf("failed to get current user ID: %w", userErr)
	}

	// Define the GraphQL query
	const query = `
	query GetUserBookByEdition($editionId: Int!, $userId: Int!) {
	  user_books(
		where: {
		  edition_id: {_eq: $editionId},
		  user_id: {_eq: $userId}
		}, 
		limit: 1
	  ) {
		id
		edition_id
	  }
	}`

	// Define the response structure to match the actual GraphQL response
	// The response is an object with a user_books array at the top level
	var response struct {
		UserBooks []struct {
			ID        int `json:"id"`
			EditionID int `json:"edition_id"`
		} `json:"user_books"`
	}

	// Execute the query
	err := c.GraphQLQuery(ctx, query, map[string]interface{}{
		"editionId": editionID,
		"userId":    userID,
	}, &response)

	if err != nil {
		log.Error("Failed to query user books", map[string]interface{}{
			"error": err.Error(),
		})
		return 0, fmt.Errorf("failed to query user books: %w", err)
	}

	if len(response.UserBooks) == 0 {
		log.Debug("No user book found for edition", nil)
		return 0, nil
	}

	userBook := response.UserBooks[0]
	userBookID := userBook.ID

	log.Debug("Found existing user book", map[string]interface{}{
		"userBookID": userBookID,
		"editionID":  userBook.EditionID,
	})

	// Cache the result with default TTL
	c.userBookIDCache.Set(editionID, userBookID, UserBookIDCacheTTL)

	log.Debug("Cached user book ID", map[string]interface{}{
		"editionID":  editionID,
		"userBookID": userBookID,
	})

	return userBookID, nil
}

// statusNameToID maps status names to their corresponding IDs in the database
var statusNameToID = map[string]int{
	"WANT_TO_READ":      1,
	"CURRENTLY_READING": 2,
	"READ":              3,
	"FINISHED":          3, // FINISHED is an alias for READ in the API
}

// CreateUserBook creates a new user book entry for the given edition ID and status
func (c *Client) CreateUserBook(ctx context.Context, editionID, status string) (string, error) {
	// First, get the edition to ensure it exists and get the book_id
	edition, err := c.GetEdition(ctx, editionID)
	if err != nil {
		c.logger.Error("Failed to get edition details", map[string]interface{}{
			"error":     err.Error(),
			"editionID": editionID,
		})
		return "", fmt.Errorf("failed to get edition details: %w", err)
	}

	// Get status ID based on status string
	statusID, ok := statusNameToID[status]
	if !ok {
		return "", fmt.Errorf("invalid status: %s", status)
	}

	// Convert editionID to integer for the mutation
	editionIDInt, err := strconv.Atoi(editionID)
	if err != nil {
		c.logger.Error("Invalid edition ID format", map[string]interface{}{
			"error":     err.Error(),
			"editionID": editionID,
		})
		return "", fmt.Errorf("invalid edition ID format: %w", err)
	}

	// Convert bookID to integer for the mutation
	editionBookID, err := strconv.Atoi(edition.BookID)
	if err != nil {
		c.logger.Error("Invalid book ID format", map[string]interface{}{
			"error":   err.Error(),
			"book_id": edition.BookID,
		})
		return "", fmt.Errorf("invalid book ID format: %w", err)
	}

	// Mutation to create a new user book with the required book_id field
	mutation := `
	mutation InsertUserBook($object: UserBookCreateInput!) {
	  insert_user_book(object: $object) {
		id
		user_book { 
		  id 
		  status_id 
		}
		error
	  }
	}`

	// Prepare the input object for the mutation
	input := map[string]interface{}{
		"object": map[string]interface{}{
			"edition_id": editionIDInt,
			"book_id":    editionBookID,
			"status_id":  statusID,
		},
	}

	// Execute the mutation
	var result struct {
		InsertUserBook struct {
			ID       int `json:"id"`
			UserBook struct {
				ID       int `json:"id"`
				StatusID int `json:"status_id"`
			} `json:"user_book"`
			Error *string `json:"error,omitempty"`
		} `json:"insert_user_book"`
	}

	err = c.GraphQLMutation(ctx, mutation, input, &result)
	if err != nil {
		c.logger.Error("Failed to create user book", map[string]interface{}{
			"error":     err.Error(),
			"editionID": editionIDInt,
			"bookID":    editionBookID,
			"statusID":  statusID,
		})
		return "", fmt.Errorf("failed to create user book: %w", err)
	}

	if result.InsertUserBook.Error != nil {
		return "", fmt.Errorf("failed to create user book: %s", *result.InsertUserBook.Error)
	}

	userBookID := strconv.Itoa(result.InsertUserBook.UserBook.ID)

	c.logger.Info("Successfully created user book", map[string]interface{}{
		"userBookID": result.InsertUserBook.UserBook.ID,
		"statusID":   result.InsertUserBook.UserBook.StatusID,
	})

	return userBookID, nil
}

// SearchByISBNResponse represents the response when searching for a book by ISBN
type SearchByISBNResponse struct {
	Books []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		BookStatusID int    `json:"book_status_id"`
		CanonicalID  *int   `json:"canonical_id"`
		Editions     []struct {
			ID              string `json:"id"`
			ASIN            string `json:"asin"`
			ISBN13          string `json:"isbn_13"`
			ISBN10          string `json:"isbn_10"`
			ReadingFormatID *int   `json:"reading_format_id"`
			AudioSeconds    *int   `json:"audio_seconds"`
		} `json:"editions"`
	} `json:"books"`
}

// SearchByTitleAuthorResponse represents the response when searching for a book by title and author
type SearchByTitleAuthorResponse struct {
	Books []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		BookStatusID int    `json:"book_status_id"`
		CanonicalID  *int   `json:"canonical_id"`
		Editions     []struct {
			ID              string `json:"id"`
			ASIN            string `json:"asin"`
			ISBN13          string `json:"isbn_13"`
			ISBN10          string `json:"isbn_10"`
			ReadingFormatID *int   `json:"reading_format_id"`
			AudioSeconds    *int   `json:"audio_seconds"`
		} `json:"editions"`
	} `json:"books"`
}

// CheckBookOwnershipResponse represents the response from checking if a book is in the user's "Owned" list
type CheckBookOwnershipResponse []struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	ListBooks []struct {
		ID        int  `json:"id"`
		BookID    int  `json:"book_id"`
		EditionID *int `json:"edition_id"`
	} `json:"list_books"`
}

// CheckBookOwnership checks if a book is in the user's "Owned" list
func (c *Client) CheckBookOwnership(ctx context.Context, bookID int) (bool, error) {
	userID, err := c.GetCurrentUserID(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get current user ID: %w", err)
	}

	log := c.logger.With(map[string]interface{}{
		"bookID": bookID,
		"userID": userID,
		"method": "CheckBookOwnership",
	})

	query := `
	query CheckBookOwnership($userId: Int!, $bookId: Int!) {
	  lists(
		where: {
		  user_id: { _eq: $userId }
		  name: { _eq: "Owned" }
		  list_books: { book_id: { _eq: $bookId } }
		}
	  ) {
		id
		name
		list_books(where: { book_id: { _eq: $bookId } }) {
		  id
		  book_id
		  edition_id
		}
	  }
	}`

	var response CheckBookOwnershipResponse
	err = c.GraphQLQuery(ctx, query, map[string]interface{}{
		"userId": userID,
		"bookId": bookID,
	}, &response)

	if err != nil {
		log.Error("Failed to execute GraphQL query", map[string]interface{}{
			"error": err.Error(),
		})
		return false, fmt.Errorf("failed to check book ownership: %w", err)
	}

	// If we have a list with list_books, the book is owned
	owned := len(response) > 0 && len(response[0].ListBooks) > 0

	log.Debug("Checked book ownership status", map[string]interface{}{
		"is_owned": owned,
	})

	return owned, nil
}

// SearchBookByISBN searches for a book by its ISBN
func (c *Client) SearchBookByISBN(ctx context.Context, isbn string) (*models.HardcoverBook, error) {
	log := c.logger.With(map[string]interface{}{
		"isbn":   isbn,
		"method": "SearchBookByISBN",
	})

	// Try with ISBN-13 first, then ISBN-10 if needed
	isbnField := "isbn_13"
	if len(isbn) == 10 {
		isbnField = "isbn_10"
	}

	query := fmt.Sprintf(`
	query BookByISBN($identifier: String!) {
	  books(
	    where: { 
	      editions: { 
	        _and: [
	          { %s: { _eq: $identifier } },
	          { reading_format: { id: { _eq: 2 } } }
	        ]
	      } 
	    },
	    limit: 1
	  ) {
	    id
	    title
	    book_status_id
	    canonical_id
	    editions(
	      where: { 
	        _and: [
	          { %s: { _eq: $identifier } },
	          { reading_format: { id: { _eq: 2 } } }
	        ]
	      },
	      limit: 1
	    ) {
	      id
	      asin
	      isbn_13
	      isbn_10
	      reading_format_id
	      audio_seconds
	    }
	  }
	}`, isbnField, isbnField)

	var result SearchByISBNResponse
	err := c.GraphQLQuery(ctx, query, map[string]interface{}{
		"identifier": isbn,
	}, &result)

	if err != nil {
		log.Error("Failed to search book by ISBN", map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("failed to search book by ISBN: %w", err)
	}

	if len(result.Books) == 0 || len(result.Books[0].Editions) == 0 {
		log.Debug("No book found with the specified ISBN", map[string]interface{}{})
		return nil, nil
	}

	bookData := result.Books[0]
	edition := bookData.Editions[0]

	hcBook := &models.HardcoverBook{
		ID:           bookData.ID,
		Title:        bookData.Title,
		EditionID:    edition.ID,
		BookStatusID: bookData.BookStatusID,
		CanonicalID:  bookData.CanonicalID,
	}

	// Set optional fields if they exist
	if edition.ASIN != "" {
		hcBook.EditionASIN = edition.ASIN
	}
	if edition.ISBN13 != "" {
		hcBook.EditionISBN13 = edition.ISBN13
	}
	if edition.ISBN10 != "" {
		hcBook.EditionISBN10 = edition.ISBN10
	}

	log.Debug("Successfully found book by ISBN", map[string]interface{}{
		"book_id":    bookData.ID,
		"edition_id": edition.ID,
	})

	return hcBook, nil
}

// SearchBookByTitleAuthor searches for a book by its title and author
func (c *Client) SearchBookByTitleAuthor(ctx context.Context, title, author string) (*models.HardcoverBook, error) {
	log := c.logger.With(map[string]interface{}{
		"title":  title,
		"author": author,
		"method": "SearchBookByTitleAuthor",
	})

	// Clean and prepare search terms
	cleanTitle := strings.TrimSpace(title)
	cleanAuthor := strings.TrimSpace(author)

	if cleanTitle == "" {
		return nil, fmt.Errorf("title cannot be empty")
	}

	// For exact matching, we need to ensure we're not using wildcards
	cleanTitle = strings.Trim(cleanTitle, "%")
	cleanAuthor = strings.Trim(cleanAuthor, "%")

	// Build the query dynamically based on available search terms
	query := `
	query BookByTitleAuthor($title: String!` + func() string {
		if cleanAuthor != "" {
			return `, $author: String!`
		}
		return ""
	}() + `) {
	  books(where: { 
	    _and: [
	      { title: { _eq: $title } }` +
		func() string {
			if cleanAuthor != "" {
				return `,
	      { authors: { name: { _eq: $author } } }`
			}
			return ""
		}() + `,
	      { editions: { reading_format: { id: { _eq: 2 } } } }
	    ]
	  }, limit: 5) {
	    id
	    title
	    book_status_id
	    canonical_id
	    editions(where: { reading_format: { id: { _eq: 2 } } }, limit: 1) {
	      id
	      asin
	      isbn_13
	      isbn_10
	      reading_format {
	        id
	      }
	    }
	  }
	}`

	// Prepare variables
	variables := map[string]interface{}{
		"title": cleanTitle,
	}

	// Only add author to variables if it's not empty
	if cleanAuthor != "" {
		variables["author"] = fmt.Sprintf("%%%s%%", cleanAuthor)
	}

	// Log the actual query being executed
	log.Debug("Executing GraphQL query", map[string]interface{}{
		"query":     query,
		"variables": variables,
	})

	// Execute the query
	var response struct {
		Books []struct {
			ID           string `json:"id"`
			Title        string `json:"title"`
			BookStatusID int    `json:"book_status_id"`
			CanonicalID  *int   `json:"canonical_id"`
			Editions     []struct {
				ID              string `json:"id"`
				ASIN            string `json:"asin"`
				ISBN13          string `json:"isbn_13"`
				ISBN10          string `json:"isbn_10"`
				ReadingFormatID *int   `json:"reading_format_id"`
			} `json:"editions"`
		} `json:"books"`
	}

	err := c.GraphQLQuery(ctx, query, variables, &response)
	if err != nil {
		log.Error("Failed to execute GraphQL query", map[string]interface{}{
			"error":     err.Error(),
			"query":     query,
			"variables": variables,
		})
		return nil, fmt.Errorf("failed to search for book by title and author: %w", err)
	}

	if len(response.Books) == 0 {
		log.Debug("No books found matching the criteria", map[string]interface{}{
			"title":  cleanTitle,
			"author": cleanAuthor,
		})
		return nil, nil
	}

	// Get the first book and its first edition
	bookData := response.Books[0]
	if len(bookData.Editions) == 0 {
		log.Warn("Book has no audio editions", map[string]interface{}{
			"book_id": bookData.ID,
		})
		return nil, nil
	}

	edition := bookData.Editions[0]

	hcBook := &models.HardcoverBook{
		ID:           bookData.ID,
		Title:        bookData.Title,
		EditionID:    edition.ID,
		BookStatusID: bookData.BookStatusID,
		CanonicalID:  bookData.CanonicalID,
	}

	// Set optional fields if they exist
	if edition.ASIN != "" {
		hcBook.EditionASIN = edition.ASIN
	}
	if edition.ISBN13 != "" {
		hcBook.EditionISBN13 = edition.ISBN13
	}
	if edition.ISBN10 != "" {
		hcBook.EditionISBN10 = edition.ISBN10
	}

	log.Debug("Successfully found book by title and author", map[string]interface{}{
		"book_id":    bookData.ID,
		"edition_id": edition.ID,
		"title":      bookData.Title,
	})

	return hcBook, nil
}

// MarkEditionAsOwned marks an edition as owned in the user's "Owned" list
func (c *Client) MarkEditionAsOwned(ctx context.Context, editionID int) error {
	log := c.logger.With(map[string]interface{}{
		"edition_id": editionID,
		"method":     "MarkEditionAsOwned",
	})

	mutation := `
	mutation EditionOwned($id: Int!) {
	  ownership: edition_owned(id: $id) {
		id
		list_book {
		  id
		  book_id
		  edition_id
		}
	  }
	}`

	variables := map[string]interface{}{
		"id": editionID,
	}

	log.Debug("Marking edition as owned in Hardcover", map[string]interface{}{
		"edition_id": editionID,
	})

	var result struct {
		Data struct {
			Ownership struct {
				ID       *int `json:"id"`
				ListBook *struct {
					ID        int `json:"id"`
					BookID    int `json:"book_id"`
					EditionID int `json:"edition_id"`
				} `json:"list_book"`
			} `json:"ownership"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	err := c.GraphQLMutation(ctx, mutation, variables, &result)
	if err != nil {
		log.Error("Failed to execute GraphQL mutation", map[string]interface{}{
			"error": err.Error(),
		})
		return fmt.Errorf("failed to mark edition as owned: %w", err)
	}

	if len(result.Errors) > 0 {
		errMsgs := make([]string, 0, len(result.Errors))
		for _, e := range result.Errors {
			errMsgs = append(errMsgs, e.Message)
		}
		errMsg := fmt.Sprintf("graphql errors: %v", strings.Join(errMsgs, "; "))
		log.Error("GraphQL error marking edition as owned", map[string]interface{}{
			"error": errMsg,
		})
		return errors.New(errMsg)
	}

	log.Debug("Successfully marked edition as owned in Hardcover", map[string]interface{}{
		"edition_id": editionID,
	})

	return nil
}

// UpdateUserBook updates a user book
func (c *Client) UpdateUserBook(ctx context.Context, input UpdateUserBookInput) error {
	log := c.logger.With(map[string]interface{}{
		"method":     "UpdateUserBook",
		"id":         input.ID,
		"edition_id": input.EditionID,
	})

	// Define the GraphQL mutation
	const mutation = `
		mutation UpdateUserBook($id: Int!, $editionId: Int) {
			update_user_book_by_pk(
				pk_columns: {id: $id},
				_set: {edition_id: $editionId}
			) {
				id
				edition_id
			}
		}`

	// Execute the mutation
	var result struct {
		UpdateUserBookByPk *struct {
			ID        int  `json:"id"`
			EditionID *int `json:"edition_id"`
		} `json:"update_user_book_by_pk"`
	}

	var editionID *graphql.Int
	if input.EditionID != nil {
		temp := graphql.Int(*input.EditionID)
		editionID = &temp
	}

	vars := map[string]interface{}{
		"id":        graphql.Int(input.ID),
		"editionId": editionID,
	}

	err := c.executeGraphQLMutation(ctx, mutation, vars, &result)
	if err != nil {
		log.Error("Failed to execute GraphQL mutation", map[string]interface{}{
			"error": err.Error(),
		})
		return fmt.Errorf("failed to update user book: %w", err)
	}

	if result.UpdateUserBookByPk == nil {
		log.Warn("User book not found or not updated", map[string]interface{}{})
		return ErrUserBookNotFound
	}

	log.Info("Successfully updated user book", map[string]interface{}{
		"id":         result.UpdateUserBookByPk.ID,
		"edition_id": result.UpdateUserBookByPk.EditionID,
	})

	return nil
}

// CheckExistingFinishedRead checks if a user book has any finished reads
func (c *Client) CheckExistingFinishedRead(ctx context.Context, input CheckExistingFinishedReadInput) (CheckExistingFinishedReadResult, error) {
	log := c.logger.With(map[string]interface{}{
		"method":       "CheckExistingFinishedRead",
		"user_book_id": input.UserBookID,
	})

	// Define the GraphQL query
	const query = `
		query CheckExistingFinishedRead($userBookId: Int!) {
			user_book_reads(where: {
				user_book_id: {_eq: $userBookId},
				progress: {_gte: 0.99}
			}, order_by: {finished_at: desc}, limit: 1) {
				finished_at
			}
		}`

	// Define the result structure
	var result struct {
		UserBookReads []struct {
			FinishedAt *string `json:"finished_at"`
		} `json:"user_book_reads"`
	}

	// Execute the query using the helper method
	err := c.executeGraphQLQuery(ctx, query, map[string]interface{}{
		"userBookId": input.UserBookID,
	}, &result)

	if err != nil {
		log.Error("Failed to execute GraphQL query", map[string]interface{}{
			"error": err.Error(),
		})
		return CheckExistingFinishedReadResult{}, fmt.Errorf("failed to execute GraphQL query: %w", err)
	}

	// Check if there are any finished reads
	hasFinishedRead := len(result.UserBookReads) > 0
	var lastFinishedAt *string
	if hasFinishedRead {
		lastFinishedAt = result.UserBookReads[0].FinishedAt
	}

	log.Debug("Checked for existing finished reads", map[string]interface{}{
		"has_finished_read": hasFinishedRead,
		"last_finished_at":  stringValue(lastFinishedAt),
	})

	return CheckExistingFinishedReadResult{
		HasFinishedRead: hasFinishedRead,
		LastFinishedAt:  lastFinishedAt,
	}, nil
}
