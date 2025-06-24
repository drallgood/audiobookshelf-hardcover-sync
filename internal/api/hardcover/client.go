package hardcover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hasura/go-graphql-client"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/cache"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
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
	rateLimitMutex   sync.Mutex
	lastRequestTime  time.Time
}

// getAuthHeader returns the properly formatted Authorization header value
func (c *Client) getAuthHeader() string {
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

// isRetryableError checks if an error is retryable
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for network errors
	if _, ok := err.(interface{ Timeout() bool }); ok {
		return true
	}

	// Check for HTTP 5xx errors
	if httpErr, ok := err.(*HTTPError); ok {
		return httpErr.StatusCode >= 500
	}

	// Check for connection refused/reset errors
	return strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "connection reset") ||
		strings.Contains(err.Error(), "i/o timeout")
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
		log.Debug().
			Str("base_url", cfg.BaseURL).
			Dur("timeout", cfg.Timeout).
			Int("max_retries", cfg.MaxRetries).
			Dur("retry_delay", cfg.RetryDelay).
			Msg("Creating new Hardcover client with config")
	} else {
		// This should not happen as we set a default logger below
		logger.Get().Warn().Msg("No logger provided to NewClientWithConfig")
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
	log.Debug().
		Str("log_level", log.GetLevel().String()).
		Msg("Logger initialized for Hardcover client")

	// Create a child logger with component info
	childLogger := logger.WithContext(map[string]interface{}{
		"component": "hardcover-client",
	})

	childLogger.Debug().Msg("Created child logger for Hardcover client")

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
	childLogger.Debug().Msg("Created new Hardcover client")

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
	l.logger.Info().
		Str("method", req.Method).
		Str("url", req.URL.String()).
		Msg("Sending request")

	// Make the request
	resp, err := l.rt.RoundTrip(req)
	if err != nil {
		l.logger.Error().
			Err(err).
			Str("method", req.Method).
			Str("url", req.URL.String()).
			Msg("Request failed")
		return nil, err
	}

	// Read the response body
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		l.logger.Error().
			Err(err).
			Str("method", req.Method).
			Str("url", req.URL.String()).
			Msg("Failed to read response body")
		return nil, err
	}

	// Create log event based on status code
	logEvent := l.logger.Info()
	if resp.StatusCode >= 400 {
		logEvent = l.logger.Error()
	}

	// Log response
	logEvent.
		Int("status", resp.StatusCode).
		Str("status_text", resp.Status).
		Str("path", req.URL.Path).
		Int("response_length", len(body)).
		Msg("Received response")

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
		r.Header.Set("Authorization", c.getAuthHeader())
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
		c.logger.Debug().
			Str("method", req.Method).
			Str("url", req.URL.String()).
			Str("operation", string(op)).
			Str("query", query).
			Interface("variables", variables).
			Msg("Executing GraphQL request")

		// Execute the request
		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("HTTP request failed: %w", err)
			c.logger.Error().
				Err(lastErr).
				Int("attempt", attempt+1).
				Msg("GraphQL request failed")
			continue
		}

		// Read the response body
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			lastErr = fmt.Errorf("failed to read response body: %w", err)
			c.logger.Error().
				Err(lastErr).
				Int("attempt", attempt+1).
				Msg("Failed to read response body")
			continue
		}

		// Log the response
		c.logger.Debug().
			Int("status", resp.StatusCode).
			Str("status_text", resp.Status).
			Bytes("response", body).
			Msg("Received GraphQL response")

		// Check for HTTP errors
		if resp.StatusCode >= 400 {
			lastErr = &HTTPError{
				StatusCode: resp.StatusCode,
				Body:       body,
			}
			c.logger.Error().
				Err(lastErr).
				Int("attempt", attempt+1).
				Msg("GraphQL request failed with HTTP error")
			continue
		}

		// Parse the response
		var gqlResp struct {
			Data   json.RawMessage `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors,omitempty"`
		}

		if err := json.Unmarshal(body, &gqlResp); err != nil {
			lastErr = fmt.Errorf("failed to unmarshal GraphQL response: %w", err)
			c.logger.Error().
				Err(lastErr).
				Int("attempt", attempt+1).
				Bytes("response", body).
				Msg("Failed to unmarshal GraphQL response")
			continue
		}

		// Check for GraphQL errors
		if len(gqlResp.Errors) > 0 {
			lastErr = fmt.Errorf("GraphQL error: %v", gqlResp.Errors[0].Message)
			c.logger.Error().
				Err(lastErr).
				Int("attempt", attempt+1).
				Interface("errors", gqlResp.Errors).
				Msg("GraphQL operation failed")
			continue
		}

		// If no result is expected, we're done
		if result == nil {
			return nil
		}

		// Unmarshal the data into the result
		if err := json.Unmarshal(gqlResp.Data, result); err != nil {
			lastErr = fmt.Errorf("failed to unmarshal GraphQL data: %w", err)
			c.logger.Error().
				Err(lastErr).
				Int("attempt", attempt+1).
				Str("data", string(gqlResp.Data)).
				Msg("Failed to unmarshal GraphQL data")
			continue
		}

		return nil
	}

	// If we get here, all retry attempts failed
	if lastErr != nil {
		c.logger.Error().
			Err(lastErr).
			Str("operation", string(op)).
			Str("query", query).
			Interface("variables", variables).
			Int("max_retries", c.maxRetries).
			Msg("GraphQL operation failed after all retries")
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
		c.logger.Debug().
			Int("user_id", userID).
			Msg("Returning cached user ID")
		return userID, nil
	}
	c.currentUserMutex.RUnlock()

	// If not in cache, acquire write lock and check again (double-checked locking pattern)
	c.currentUserMutex.Lock()
	defer c.currentUserMutex.Unlock()

	// Check again in case another goroutine updated the cache while we were waiting for the lock
	if c.currentUserID != 0 {
		c.logger.Debug().
			Int("user_id", c.currentUserID).
			Msg("Returning user ID from cache (after acquiring lock)")
		return c.currentUserID, nil
	}

	c.logger.Debug().Msg("User ID not in cache, fetching from Hardcover API")

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

	c.logger.Debug().
		Int("user_id", userID).
		Msg("Successfully retrieved and cached current user ID from Hardcover")

	return userID, nil
}

// SearchBookByISBN13 searches for a book in the Hardcover database by ISBN-13
func (c *Client) SearchBookByISBN13(ctx context.Context, isbn13 string) (*models.HardcoverBook, error) {
	return c.searchBookByISBN(ctx, "isbn_13", isbn13)
}

// SearchBookByISBN10 searches for a book in the Hardcover database by ISBN-10
func (c *Client) SearchBookByISBN10(ctx context.Context, isbn10 string) (*models.HardcoverBook, error) {
	return c.searchBookByISBN(ctx, "isbn_10", isbn10)
}

// SearchBookByASIN searches for a book in the Hardcover database by ASIN
func (c *Client) SearchBookByASIN(ctx context.Context, asin string) (*models.HardcoverBook, error) {
	if asin == "" {
		return nil, fmt.Errorf("ASIN cannot be empty")
	}

	log := c.logger.With().
		Str("asin", asin).
		Str("method", "SearchBookByASIN").
		Logger()

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

	// Use a flexible map to capture the raw response first
	var rawResponse map[string]interface{}

	err := c.GraphQLQuery(ctx, query, map[string]interface{}{
		"asin": asin,
	}, &rawResponse)

	if err != nil {
		log.Error().
			Err(err).
			Str("asin", asin).
			Msg("Failed to search book by ASIN")
		return nil, fmt.Errorf("failed to search book by ASIN: %w", err)
	}

	// Debug log the raw response
	log.Debug().
		Str("raw_response", fmt.Sprintf("%+v", rawResponse)).
		Msg("Raw GraphQL response")

	// Debug log the raw response structure
	log.Debug().
		Str("response_type", fmt.Sprintf("%T", rawResponse)).
		Msg("Raw response type")

	// Try to extract the books from the response
	var books []map[string]interface{}
	
	// First, try to get the data field as a map
	data, ok := rawResponse["data"].(map[string]interface{})
	if !ok {
		// If that fails, try to see if rawResponse itself is the data
		if _, isMap := rawResponse["books"]; isMap {
			data = rawResponse
		} else {
			log.Warn().
				Msg("No 'data' key found in response and response is not a direct data object")
		}
	}

	if data != nil {
		log.Debug().
			Str("data_keys", fmt.Sprintf("%v", getMapKeys(data))).
			Msg("Data keys in response")

		if booksData, ok := data["books"]; ok {
			switch v := booksData.(type) {
			case []interface{}:
				log.Debug().
					Int("books_count", len(v)).
					Msg("Found books array in response")

				for i, b := range v {
					if book, ok := b.(map[string]interface{}); ok {
						books = append(books, book)
						log.Debug().
							Int("book_index", i).
							Str("book_id", fmt.Sprintf("%v", book["id"])).
							Str("title", fmt.Sprintf("%v", book["title"])).
							Msg("Found book in response")
					}
				}
			default:
				log.Warn().
					Str("books_type", fmt.Sprintf("%T", v)).
					Msg("Unexpected type for books data")
			}
		} else {
			log.Warn().
				Msg("No 'books' key found in data")
		}
	}

	log.Debug().
		Int("books_count", len(books)).
		Msg("Extracted books from response")

	if err != nil {
		log.Error().
			Err(err).
			Str("asin", asin).
			Msg("Failed to search book by ASIN")
		return nil, fmt.Errorf("failed to search book by ASIN: %w", err)
	}

	// Check if any books were found
	if len(books) == 0 {
		log.Debug().
			Str("asin", asin).
			Msg("No books found with the given ASIN")
		return nil, nil
	}

	// Process the first book
	bookData := books[0]
	log.Debug().
		Str("book_data", fmt.Sprintf("%+v", bookData)).
		Msg("Processing first book")

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
		log.Warn().
			Str("book_id", hcBook.ID).
			Msg("No editions found for book")
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
	log.Debug().
		Str("book_id", hcBook.ID).
		Str("title", hcBook.Title).
		Str("edition_id", hcBook.EditionID).
		Msg("Successfully found book by ASIN")

	log.Debug().
		Str("book_id", hcBook.ID).
		Str("edition_id", hcBook.EditionID).
		Str("title", hcBook.Title).
		Msg("Successfully found book by ASIN")

	return hcBook, nil
}

// searchBookByISBN is a helper function to search for a book by ISBN (13 or 10)
func (c *Client) searchBookByISBN(ctx context.Context, isbnField, isbn string) (*models.HardcoverBook, error) {
	if isbn == "" {
		return nil, fmt.Errorf("ISBN cannot be empty")
	}

	log := c.logger.With().
		Str("isbn", isbn).
		Str("isbn_field", isbnField).
		Str("method", "searchBookByISBN").
		Logger()

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

	var result struct {
		Data struct {
			Books []*Book `json:"books"`
		} `json:"data"`
	}

	// Execute the GraphQL query
	err := c.GraphQLQuery(ctx, query, map[string]interface{}{
		"isbn": normalizedISBN,
	}, &result)

	if err != nil {
		log.Error().
			Err(err).
			Str("isbn", isbn).
			Msg("Failed to search book by ISBN")
		return nil, fmt.Errorf("failed to search book by ISBN: %w", err)
	}

	// Check if any books were found
	if len(result.Data.Books) == 0 {
		log.Debug().Msg("No books found with the given ISBN")
		return nil, nil
	}

	bookData := result.Data.Books[0]

	// Check if we have any editions
	if len(bookData.Editions) == 0 {
		log.Warn().
			Str("book_id", bookData.ID.String()).
			Msg("No editions found for book")
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
			log.Warn().
				Err(err).
				Str("canonical_id", *bookData.CanonicalID).
				Msg("Failed to parse canonical_id as integer")
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

	log.Debug().
		Str("book_id", hcBook.ID).
		Str("edition_id", hcBook.EditionID).
		Msg("Successfully found book by ISBN")

	return hcBook, nil
}

// SearchBooks searches for books in the Hardcover database
func (c *Client) SearchBooks(ctx context.Context, query string) ([]models.SearchResult, error) {
	const endpoint = "/search/books"
	log := c.logger.With().
		Str("endpoint", endpoint).
		Str("query", query).
		Logger()

	// Build URL with query parameters
	url := fmt.Sprintf("%s%s?q=%s&limit=5", c.baseURL, endpoint, query)

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create request")
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", c.getAuthHeader())
	req.Header.Set("Accept", "application/json")

	// Execute request
	log.Debug().Msg("Searching for books")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Error().Err(err).Msg("Failed to search for books")
		return nil, fmt.Errorf("failed to search for books: %w", err)
	}
	defer resp.Body.Close()

	// Read response body for error handling
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read response body")
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("unexpected status code: %d: %s", resp.StatusCode, resp.Status)
		log.Error().
			Int("status_code", resp.StatusCode).
			Str("status", resp.Status).
			Str("response", string(body)).
			Str("request_url", req.URL.String()).
			Str("request_method", req.Method).
			Str("auth_header", req.Header.Get("Authorization")[:10]+"...").
			Msg(errMsg)

		// Try to parse error response if it's JSON
		var errResp struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if jsonErr := json.Unmarshal(body, &errResp); jsonErr == nil {
			if errResp.Error != "" {
				errMsg = fmt.Sprintf("%s: %s", errMsg, errResp.Error)
			}
			if errResp.Message != "" {
				errMsg = fmt.Sprintf("%s: %s", errMsg, errResp.Message)
			}
		}

		return nil, fmt.Errorf(errMsg)
	}

	// Parse response
	var searchResp models.SearchResponse
	if err := json.Unmarshal(body, &searchResp); err != nil {
		log.Error().
			Err(err).
			Str("response", string(body)).
			Msg("Failed to decode response")
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	log.Info().
		Int("count", len(searchResp.Results)).
		Msg("Successfully searched for books")

	return searchResp.Results, nil
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
	log := c.logger.With().
		Str("endpoint", endpoint).
		Str("book_id", bookID).
		Float64("progress", progress).
		Str("status", status).
		Bool("mark_as_owned", markAsOwned).
		Logger()

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
		log.Error().Err(err).Msg("Failed to marshal request body")
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
		log.Error().Err(err).Msg("Failed to create request")
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", c.getAuthHeader())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Execute request
	log.Debug().Msg("Updating reading progress")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Error().Err(err).Msg("Failed to update reading progress")
		return fmt.Errorf("failed to update reading progress: %w", err)
	}
	defer resp.Body.Close()

	// Read response body for error handling
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read response body")
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		log.Error().
			Int("status", resp.StatusCode).
			Str("response", string(body)).
			Msg("Failed to update reading progress")

		if resp.StatusCode == http.StatusNotFound {
			return ErrBookNotFound
		}
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	log.Info().Msg("Successfully updated reading progress")
	return nil
}

// UploadCoverImage uploads a cover image to a book in Hardcover
func (c *Client) UploadCoverImage(ctx context.Context, bookID, imageURL, description string) error {
	const endpoint = "/covers/upload"
	log := c.logger.With().
		Str("endpoint", endpoint).
		Str("book_id", bookID).
		Str("image_url", imageURL).
		Logger()

	// Download the image
	log.Debug().Msg("Downloading cover image")
	imgReq, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create download request")
		return fmt.Errorf("failed to create download request: %w", err)
	}

	imgResp, err := c.httpClient.Do(imgReq)
	if err != nil {
		log.Error().Err(err).Msg("Failed to download cover image")
		return fmt.Errorf("failed to download cover image: %w", err)
	}
	defer imgResp.Body.Close()

	if imgResp.StatusCode < 200 || imgResp.StatusCode >= 300 {
		log.Error().Int("status", imgResp.StatusCode).Msg("Failed to download cover image")
		return fmt.Errorf("failed to download cover image: status %d", imgResp.StatusCode)
	}

	// Read image data
	imgData, err := io.ReadAll(imgResp.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read image data")
		return fmt.Errorf("failed to read image data: %w", err)
	}

	// Create a buffer to store the multipart form data
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Add the book ID
	if err := writer.WriteField("book_id", bookID); err != nil {
		log.Error().Err(err).Msg("Failed to write book ID field")
		return fmt.Errorf("failed to write book ID field: %w", err)
	}

	// Add the description if provided
	if description != "" {
		if err := writer.WriteField("description", description); err != nil {
			log.Error().Err(err).Msg("Failed to write description field")
			return fmt.Errorf("failed to write description field: %w", err)
		}
	}

	// Add the image file
	part, err := writer.CreateFormFile("file", "cover.jpg")
	if err != nil {
		log.Error().Err(err).Msg("Failed to create form file")
		return fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := part.Write(imgData); err != nil {
		log.Error().Err(err).Msg("Failed to write image data to form")
		return fmt.Errorf("failed to write image data to form: %w", err)
	}

	// Close the writer to finalize the multipart message
	if err := writer.Close(); err != nil {
		log.Error().Err(err).Msg("Failed to close multipart writer")
		return fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Create the upload request
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+endpoint,
		&requestBody,
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create upload request")
		return fmt.Errorf("failed to create upload request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", c.getAuthHeader())
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	// Execute the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Error().Err(err).Msg("Failed to upload cover image")
		return fmt.Errorf("failed to upload cover image: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		log.Error().
			Int("status", resp.StatusCode).
			Str("response", string(body)).
			Msg("Failed to upload cover image")
		return fmt.Errorf("failed to upload cover image: status %d", resp.StatusCode)
	}

	log.Info().Msg("Successfully uploaded cover image")
	return nil
}

// DatesReadInput represents the input for date-related fields when creating or updating a user book read entry
type DatesReadInput struct {
	Action         *string `json:"action,omitempty"`
	EditionID      *int64  `json:"edition_id,omitempty"`
	FinishedAt     *string `json:"finished_at,omitempty"`
	ID             *int64  `json:"id,omitempty"`
	StartedAt      *string `json:"started_at,omitempty"`
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
		"user_book_id":  input.UserBookID,
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
		log.Error().
			Err(err).
			Msg("Failed to execute GraphQL query")
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

	log.Debug().Msg("No existing read entry found for today")
	return nil, nil
}

// UpdateUserBookRead updates an existing user book read entry
func (c *Client) UpdateUserBookRead(ctx context.Context, input UpdateUserBookReadInput) (bool, error) {
	c.logger.Debug().
		Int64("id", input.ID).
		Interface("object", input.Object).
		Msg("Updating user book read")

	// Convert the input to JSON for the mutation
	updateObj, err := json.Marshal(input.Object)
	if err != nil {
		c.logger.Error().Err(err).Msg("Failed to marshal update object")
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
		c.logger.Error().Err(err).Msg("Failed to unmarshal update object to DatesReadInput")
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
		c.logger.Error().Err(err).Msg("Failed to execute update mutation")
		return false, fmt.Errorf("failed to execute update mutation: %w", err)
	}

	// Check for errors in the response
	if result.UpdateUserBookRead.Error != nil {
		errMsg := *result.UpdateUserBookRead.Error
		c.logger.Error().
			Str("error", errMsg).
			Msg("Error in update_user_book_read response")
		return false, fmt.Errorf("update error: %s", errMsg)
	}

	// The API sometimes returns success with user_book_read: null
	// In this case, we'll assume the update was successful
	if result.UpdateUserBookRead.UserBookRead == nil {
		c.logger.Info().
			Int64("id", input.ID).
			Msg("Successfully updated user book read (no user_book_read in response but no error)")
		return true, nil
	}

	updatedID := result.UpdateUserBookRead.UserBookRead.ID
	c.logger.Info().
		Int("updated_id", updatedID).
		Msg("Successfully updated user book read entry")

	return true, nil
}

type GetUserBookInput struct {
	ID int `json:"id"`
}

// UpdateUserBookInput represents the input for updating a user book
type UpdateUserBookInput struct {
	ID        int64  `json:"id"`
	EditionID *int64 `json:"edition_id,omitempty"`
}

// GetUserBookResult represents the result of getting a user book
// GetUserBookResult represents the result of getting a user book
type GetUserBookResult struct {
	ID        int64    `json:"id"`
	BookID    int64    `json:"book_id"`
	EditionID *int64   `json:"edition_id"`
	Status    string   `json:"status"`
	Progress  *float64 `json:"progress,omitempty"`
}

// GetEdition retrieves edition details including book_id for a given edition_id
func (c *Client) GetEdition(ctx context.Context, editionID string) (*models.Edition, error) {
	log := c.logger.With().
		Str("edition_id", editionID).
		Str("method", "GetEdition").
		Logger()

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
			}
		}`

	// Define the response structure that matches the GraphQL response
	var response struct {
		Editions []struct {
			ID     int     `json:"id"`
			BookID int     `json:"book_id"`
			Title  *string `json:"title"`
			ISBN10 *string `json:"isbn_10"`
			ISBN13 *string `json:"isbn_13"`
			ASIN   *string `json:"asin"`
		} `json:"editions"`
	}

	// Execute the query
	err = c.GraphQLQuery(ctx, query, map[string]interface{}{
		"editionId": editionIDInt,
	}, &response)

	editions := response.Editions

	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to execute GraphQL query")
		return nil, fmt.Errorf("failed to get edition: %w", err)
	}

	if len(editions) == 0 {
		log.Debug().
			Str("edition_id", editionID).
			Msg("Edition not found in response")
		return nil, fmt.Errorf("edition not found: %s", editionID)
	}

	// Get the first edition
	edition := editions[0]

	// Log the raw edition data for debugging
	log.Debug().
		Int("id", edition.ID).
		Int("book_id", edition.BookID).
		Str("title", safeString(edition.Title)).
		Str("isbn_10", safeString(edition.ISBN10)).
		Str("isbn_13", safeString(edition.ISBN13)).
		Str("asin", safeString(edition.ASIN)).
		Msg("Retrieved edition details")

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

	log.Debug().
		Str("book_id", editionModel.BookID).
		Str("title", editionModel.Title).
		Msg("Retrieved edition details")

	return editionModel, nil
}

// GetUserBookID retrieves the user book ID for a given edition ID with caching
func (c *Client) GetUserBookID(ctx context.Context, editionID int) (int, error) {
	log := c.logger.With().
		Int("editionID", editionID).
		Str("method", "GetUserBookID").
		Logger()

	// Check cache first
	if userBookID, exists := c.userBookIDCache.Get(editionID); exists {
		log.Debug().
			Int("userBookID", userBookID).
			Msg("Found user book ID in cache")
		return userBookID, nil
	}

	log.Debug().Msg("User book ID not found in cache, querying API")

	// Get the current user ID to filter by the correct user
	userID, userErr := c.GetCurrentUserID(ctx)
	if userErr != nil {
		log.Error().Err(userErr).Msg("Failed to get current user ID")
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
		log.Error().
			Err(err).
			Msg("Failed to query user books")
		return 0, fmt.Errorf("failed to query user books: %w", err)
	}

	if len(response.UserBooks) == 0 {
		log.Debug().Msg("No user book found for edition")
		return 0, nil
	}

	userBook := response.UserBooks[0]
	userBookID := userBook.ID

	log.Debug().
		Int("userBookID", userBookID).
		Int("editionID", userBook.EditionID).
		Msg("Found existing user book")

	// Cache the result with default TTL
	c.userBookIDCache.Set(editionID, userBookID, UserBookIDCacheTTL)

	log.Debug().
		Int("editionID", editionID).
		Int("userBookID", userBookID).
		Dur("ttl", UserBookIDCacheTTL).
		Msg("Cached user book ID")

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
		c.logger.Error().
			Err(err).
			Str("edition_id", editionID).
			Msg("Failed to get edition details")
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
		c.logger.Error().
			Err(err).
			Str("edition_id", editionID).
			Msg("Invalid edition ID format")
		return "", fmt.Errorf("invalid edition ID format: %w", err)
	}

	// Convert bookID to integer for the mutation
	editionBookID, err := strconv.Atoi(edition.BookID)
	if err != nil {
		c.logger.Error().
			Err(err).
			Str("book_id", edition.BookID).
			Msg("Invalid book ID format")
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
		c.logger.Error().
			Err(err).
			Int("edition_id", editionIDInt).
			Int("book_id", editionBookID).
			Int("status_id", statusID).
			Msg("Failed to create user book")
		return "", fmt.Errorf("failed to create user book: %w", err)
	}

	if result.InsertUserBook.Error != nil {
		return "", fmt.Errorf("failed to create user book: %s", *result.InsertUserBook.Error)
	}

	userBookID := strconv.Itoa(result.InsertUserBook.UserBook.ID)

	c.logger.Info().
		Int("user_book_id", result.InsertUserBook.UserBook.ID).
		Int("status_id", result.InsertUserBook.UserBook.StatusID).
		Msg("Successfully created user book")

	return userBookID, nil
}

// CheckBookOwnershipInput represents the input for checking if a book is owned
// by the current user in their "Owned" list
type CheckBookOwnershipInput struct {
	BookID int `json:"book_id"`
}

// ListBook represents a book in a user's list
type ListBook struct {
	ID        int  `json:"id"`
	BookID    int  `json:"book_id"`
	EditionID *int `json:"edition_id"`
}

// List represents a user's list with its books
type List struct {
	ID        int        `json:"id"`
	Name      string     `json:"name"`
	ListBooks []ListBook `json:"list_books"`
}

// CheckBookOwnershipResponse represents the response from the CheckBookOwnership query
type CheckBookOwnershipResponse []struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	ListBooks []struct {
		ID        int `json:"id"`
		BookID    int `json:"book_id"`
		EditionID int `json:"edition_id"`
	} `json:"list_books"`
}

// SearchByASINResponse represents the response when searching for a book by ASIN
type SearchByASINResponse struct {
	Books []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		BookStatusID int    `json:"book_status_id"`
		CanonicalID  *int   `json:"canonical_id"`
		Editions     []struct {
			ID     string `json:"id"`
			ASIN   string `json:"asin"`
			ISBN13 string `json:"isbn_13"`
			ISBN10 string `json:"isbn_10"`
		} `json:"editions"`
	} `json:"books"`
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

// CheckBookOwnership checks if a book is in the user's "Owned" list
func (c *Client) CheckBookOwnership(ctx context.Context, bookID int) (bool, error) {
	userID, err := c.GetCurrentUserID(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get current user ID: %w", err)
	}

	log := c.logger.With().
		Int("book_id", bookID).
		Int("user_id", userID).
		Str("method", "CheckBookOwnership").
		Logger()

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
		log.Error().Err(err).Msg("Failed to execute GraphQL query")
		return false, fmt.Errorf("failed to check book ownership: %w", err)
	}

	// If we have a list with list_books, the book is owned
	owned := len(response) > 0 && len(response[0].ListBooks) > 0

	log.Debug().
		Bool("is_owned", owned).
		Msg("Checked book ownership status")

	return owned, nil
}

// SearchBookByISBN searches for a book by its ISBN
func (c *Client) SearchBookByISBN(ctx context.Context, isbn string) (*models.HardcoverBook, error) {
	log := c.logger.With().
		Str("isbn", isbn).
		Str("method", "SearchBookByISBN").
		Logger()

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
		log.Error().Err(err).Msg("Failed to search book by ISBN")
		return nil, fmt.Errorf("failed to search book by ISBN: %w", err)
	}

	if len(result.Books) == 0 || len(result.Books[0].Editions) == 0 {
		log.Debug().Msg("No book found with the specified ISBN")
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

	log.Debug().
		Str("book_id", bookData.ID).
		Str("edition_id", edition.ID).
		Msg("Successfully found book by ISBN")

	return hcBook, nil
}

// SearchBookByTitleAuthor searches for a book by its title and author
func (c *Client) SearchBookByTitleAuthor(ctx context.Context, title, author string) (*models.HardcoverBook, error) {
	log := c.logger.With().
		Str("title", title).
		Str("author", author).
		Str("method", "SearchBookByTitleAuthor").
		Logger()

	// Clean and prepare search terms
	cleanTitle := strings.TrimSpace(title)
	cleanAuthor := strings.TrimSpace(author)

	if cleanTitle == "" {
		return nil, fmt.Errorf("title cannot be empty")
	}

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
	log.Debug().
		Str("query", query).
		Interface("variables", variables).
		Msg("Executing GraphQL query")

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
		log.Error().
			Err(err).
			Str("query", query).
			Interface("variables", variables).
			Msg("Failed to execute GraphQL query")
		return nil, fmt.Errorf("failed to search for book by title and author: %w", err)
	}

	if len(response.Books) == 0 {
		log.Debug().
			Str("title", cleanTitle).
			Str("author", cleanAuthor).
			Msg("No books found matching the criteria")
		return nil, nil
	}

	// Get the first book and its first edition
	bookData := response.Books[0]
	if len(bookData.Editions) == 0 {
		log.Warn().
			Str("book_id", bookData.ID).
			Msg("Book has no audio editions")
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

	log.Debug().
		Str("book_id", bookData.ID).
		Str("edition_id", edition.ID).
		Str("title", bookData.Title).
		Msg("Successfully found book by title and author")

	return hcBook, nil
}

// MarkEditionAsOwned marks an edition as owned in the user's "Owned" list
func (c *Client) MarkEditionAsOwned(ctx context.Context, editionID int) error {
	log := c.logger.With().
		Int("edition_id", editionID).
		Str("method", "MarkEditionAsOwned").
		Logger()

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

	log.Debug().Msg("Marking edition as owned in Hardcover")

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
		log.Error().Err(err).Msg("Failed to execute GraphQL mutation")
		return fmt.Errorf("failed to mark edition as owned: %w", err)
	}

	if len(result.Errors) > 0 {
		errMsgs := make([]string, 0, len(result.Errors))
		for _, e := range result.Errors {
			errMsgs = append(errMsgs, e.Message)
		}
		errMsg := fmt.Sprintf("graphql errors: %v", strings.Join(errMsgs, "; "))
		log.Error().Msg(errMsg)
		return errors.New(errMsg)
	}

	log.Debug().
		Int("edition_id", editionID).
		Msg("Successfully marked edition as owned in Hardcover")

	return nil
}

// GetUserBook retrieves a user book by ID
func (c *Client) GetUserBook(ctx context.Context, id int64) (*GetUserBookResult, error) {
	log := c.logger.With().
		Int64("user_book_id", id).
		Str("method", "GetUserBook").
		Logger()

	// Define the GraphQL query
	const query = `
		query GetUserBook($id: Int!) {
			user_books(where: {id: {_eq: $id}}, limit: 1) {
				id
				edition_id
				book_id
				status_id
			}
		}`

	// Define the response structure for user books
	var response struct {
		UserBooks []struct {
			ID        int64  `json:"id"`
			BookID    int64  `json:"book_id"`
			EditionID *int64 `json:"edition_id"`
			StatusID  int    `json:"status_id"`
		} `json:"user_books"`
	}

	// Execute the query using the defined query string
	err := c.executeGraphQLQuery(ctx, query, map[string]interface{}{
		"id": id,
	}, &response)
	if err != nil {
		log.Error().Err(err).Msg("Failed to execute GraphQL query")
		return nil, fmt.Errorf("failed to get user book: %w", err)
	}

	if len(response.UserBooks) == 0 {
		log.Warn().Msg("User book not found")
		return nil, ErrUserBookNotFound
	}

	userBook := response.UserBooks[0]

	// Map status_id to status string
	status := "UNKNOWN"
	switch userBook.StatusID {
	case 1:
		status = "WANT_TO_READ"
	case 2:
		status = "READING"
	case 3:
		status = "FINISHED"
	}

	log.Debug().
		Int64("id", userBook.ID).
		Int64("book_id", userBook.BookID).
		Interface("edition_id", userBook.EditionID).
		Int("status_id", userBook.StatusID).
		Str("status", status).
		Msg("Retrieved user book")

	return &GetUserBookResult{
		ID:        userBook.ID,
		BookID:    userBook.BookID,
		EditionID: userBook.EditionID,
		Status:    status,
	}, nil
}

// UpdateUserBook updates a user book
func (c *Client) UpdateUserBook(ctx context.Context, input UpdateUserBookInput) error {
	log := c.logger.With().
		Str("method", "UpdateUserBook").
		Int64("id", input.ID).
		Interface("edition_id", input.EditionID).
		Logger()

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
		log.Error().Err(err).Msg("Failed to execute GraphQL mutation")
		return fmt.Errorf("failed to update user book: %w", err)
	}

	if result.UpdateUserBookByPk == nil {
		log.Warn().Msg("User book not found or not updated")
		return ErrUserBookNotFound
	}

	log.Info().
		Int("id", result.UpdateUserBookByPk.ID).
		Interface("edition_id", result.UpdateUserBookByPk.EditionID).
		Msg("Successfully updated user book")

	return nil
}

// CheckExistingFinishedRead checks if a user book has any finished reads
func (c *Client) CheckExistingFinishedRead(ctx context.Context, input CheckExistingFinishedReadInput) (CheckExistingFinishedReadResult, error) {
	log := c.logger.With().
		Str("method", "CheckExistingFinishedRead").
		Int("user_book_id", input.UserBookID).
		Logger()

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
		log.Error().Err(err).Msg("Failed to execute GraphQL query")
		return CheckExistingFinishedReadResult{}, fmt.Errorf("failed to execute GraphQL query: %w", err)
	}

	// Check if there are any finished reads
	hasFinishedRead := len(result.UserBookReads) > 0
	var lastFinishedAt *string
	if hasFinishedRead {
		lastFinishedAt = result.UserBookReads[0].FinishedAt
	}

	log.Debug().
		Bool("has_finished_read", hasFinishedRead).
		Str("last_finished_at", stringValue(lastFinishedAt)).
		Msg("Checked for existing finished reads")

	return CheckExistingFinishedReadResult{
		HasFinishedRead: hasFinishedRead,
		LastFinishedAt:  lastFinishedAt,
	}, nil
}
