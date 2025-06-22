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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hasura/go-graphql-client"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/cache"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
)

// graphqlOperation is a helper type for GraphQL operations
type graphqlOperation string

// Constants for GraphQL operation types
const (
	queryOperation    graphqlOperation = "query"
	mutationOperation graphqlOperation = "mutation"
)

// Common errors
var (
	ErrBookNotFound       = errors.New("book not found")
	ErrUserBookNotFound   = errors.New("user book not found")
	ErrInvalidInput      = errors.New("invalid input")
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
	// RateLimit specifies the minimum time between requests (default: DefaultRateLimit)
	RateLimit time.Duration
	// Burst specifies the burst size for rate limiting (default: DefaultBurst)
	Burst int
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
type Client struct {
	baseURL    string
	authToken  string
	httpClient *http.Client
	gqlClient  *graphql.Client
	logger     *logger.Logger
	
	// Current user ID and its mutex for thread-safe access
	currentUserID int
	currentUserMutex sync.RWMutex

	// Rate limiting
	rateLimiter *util.RateLimiter

	// Retry configuration
	maxRetries int
	retryDelay time.Duration

	// Caches
	userBookIDCache cache.Cache[int, int]     // editionID -> userBookID
	userCache      cache.Cache[string, any]   // Generic cache for user-specific data

	// Mutex for rate limiting
	rateLimitMutex sync.Mutex
	lastRequestTime time.Time
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

// stringValue is a helper function to safely get a string value from a string pointer
func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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
	return &ClientConfig{
		BaseURL:    DefaultBaseURL,
		Timeout:    DefaultTimeout,
		MaxRetries: DefaultMaxRetries,
		RetryDelay: DefaultRetryDelay,
		RateLimit:  100 * time.Millisecond, // 10 requests per second
		Burst:      10,                    // Allow bursts of up to 10 requests
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

	// Create rate limiter
	rateLimiter := util.NewRateLimiter(cfg.RateLimit, cfg.Burst)

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
	Body      []byte
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
	var response map[string]json.RawMessage
	err := c.executeGraphQLOperation(ctx, queryOperation, query, variables, &response)
	if err != nil {
		return fmt.Errorf("graphql query failed: %w", err)
	}

	// The actual data is in the first (and only) key of the response
	for _, data := range response {
		// If result is nil, just return without unmarshaling
		if result == nil {
			return nil
		}
		// Otherwise, unmarshal the data into the result
		return json.Unmarshal(data, result)
	}

	return errors.New("empty response from GraphQL server")
}

// GraphQLMutation executes a GraphQL mutation and unmarshals the response into the result parameter
func (c *Client) GraphQLMutation(ctx context.Context, mutation string, variables map[string]interface{}, result interface{}) error {
	if variables == nil {
		variables = make(map[string]interface{})
	}

	// Execute the mutation using the GraphQL client
	var response map[string]json.RawMessage
	err := c.executeGraphQLOperation(ctx, mutationOperation, mutation, variables, &response)
	if err != nil {
		return fmt.Errorf("graphql mutation failed: %w", err)
	}

	// The actual data is in the first (and only) key of the response
	for _, data := range response {
		// If result is nil, just return without unmarshaling
		if result == nil {
			return nil
		}
		// Otherwise, unmarshal the data into the result
		return json.Unmarshal(data, result)
	}

	return errors.New("empty response from GraphQL server")
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
				Body:      body,
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
	Action     *string `json:"action,omitempty"`
	EditionID  *int64  `json:"edition_id,omitempty"`
	FinishedAt *string `json:"finished_at,omitempty"`
	ID         *int64  `json:"id,omitempty"`
	StartedAt  *string `json:"started_at,omitempty"`
}

// InsertUserBookReadInput represents the input for creating a new user book read entry
type InsertUserBookReadInput struct {
	UserBookID   int64          `json:"user_book_id"`
	DatesRead    DatesReadInput `json:"user_book_read"`
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
	variables := map[string]interface{}{
		"user_book_id": input.UserBookID,
		"user_book_read": map[string]interface{}{
			"action":      input.DatesRead.Action,
			"edition_id":  input.DatesRead.EditionID,
			"finished_at": input.DatesRead.FinishedAt,
			"id":          input.DatesRead.ID,
			"started_at":  input.DatesRead.StartedAt,
		},
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
	ID              int64    `json:"id"`
	UserBookID      int64    `json:"user_book_id"`
	Progress        float64  `json:"progress"`
	ProgressSeconds *int     `json:"progress_seconds"`
	StartedAt       *string  `json:"started_at"`
	FinishedAt      *string  `json:"finished_at"`
	EditionID       *int64   `json:"edition_id"`
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

	// Define the mutation
	mutation := `
		mutation UpdateUserBookRead($id: Int!, $updates: jsonb!) {
			update_user_book_read(
				where: { id: { _eq: $id } },
				_set: { object: $updates }
			) {
				returning {
					id
				}
			}
		}
	`

	// Define the result type
	var result UpdateUserBookReadResponse

	vars := map[string]interface{}{
		"id":      input.ID,
		"updates": json.RawMessage(updateObj),
	}

	err = c.executeGraphQLMutation(ctx, mutation, vars, &result)
	if err != nil {
		c.logger.Error().Err(err).Msg("Failed to execute update mutation")
		return false, fmt.Errorf("failed to execute update mutation: %w", err)
	}

	// Check if any records were updated
	if len(result.UpdateUserBookRead.Returning) == 0 {
		c.logger.Warn().Int64("id", input.ID).Msg("No records were updated")
		return false, nil
	}

	updatedID := result.UpdateUserBookRead.Returning[0].ID
	c.logger.Info().
		Int64("updated_id", updatedID).
		Msg("Successfully updated user book read entry")

	return true, nil
}

type GetUserBookInput struct {
	ID int `json:"id"`
}

// UpdateUserBookInput represents the input for updating a user book
type UpdateUserBookInput struct {
	ID        int64   `json:"id"`
	EditionID *int64  `json:"edition_id,omitempty"`
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

	// Ensure we're not hitting rate limits
	if err := c.enforceRateLimit(); err != nil {
		log.Error().Err(err).Msg("Rate limit enforced")
		return 0, fmt.Errorf("rate limit enforced: %w", err)
	}

	// Define the response structure with GraphQL struct tags
	var resp struct {
		UserBooks []struct {
			ID        int `json:"id" graphql:"id"`
			BookID    int `json:"book_id" graphql:"book_id"`
			EditionID int `json:"edition_id" graphql:"edition_id"`
		} `json:"user_books" graphql:"user_books(where: {edition_id: {_eq: $editionId}})"`
	}

	// Execute the query
	log.Debug().Msg("Executing GraphQL query")
	err := c.gqlClient.Query(ctx, &resp, map[string]interface{}{
		"editionId": editionID,
	})

	if err != nil {
		log.Error().
			Err(err).
			Msg("Failed to query user books")
		return 0, fmt.Errorf("failed to query user books: %w", err)
	}

	if len(resp.UserBooks) == 0 {
		log.Debug().Msg("No user book found for edition")
		return 0, nil
	}

	userBook := resp.UserBooks[0]
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

// CreateUserBook creates a new user book entry for the given edition ID and status
func (c *Client) CreateUserBook(editionID, status string) (string, error) {
	// Default status if not provided
	if status == "" {
		status = "WANT_TO_READ"
	}

	// Mutation to create a new user book
	mutation := `
	mutation CreateUserBook($editionID: Int!, $status: String!) {
	  insert_user_books(objects: {edition_id: $editionID, status: $status}) {
		returning {
		  id
		  edition_id
		}
	  }
	}`

	var result struct {
		InsertUserBooks struct {
			Returning []struct {
				ID        int `json:"id"`
				EditionID int `json:"edition_id"`
			} `json:"returning"`
		} `json:"insert_user_books"`
	}

	editionIDInt, err := strconv.Atoi(editionID)
	if err != nil {
		return "", fmt.Errorf("invalid edition ID: %v", err)
	}

	err = c.executeGraphQLQuery(context.Background(), mutation, map[string]interface{}{
		"editionID": editionIDInt,
		"status":    status,
	}, &result)
	if err != nil {
		c.logger.Error().Err(err).Str("edition_id", editionID).Msg("Failed to create user book")
		return "", fmt.Errorf("failed to create user book: %w", err)
	}

	if len(result.InsertUserBooks.Returning) == 0 {
		return "", fmt.Errorf("failed to create user book: no records returned")
	}

	userBook := result.InsertUserBooks.Returning[0]

	c.logger.Info().
		Int("user_book_id", userBook.ID).
		Int("edition_id", userBook.EditionID).
		Msg("Created new user book entry")

	return strconv.Itoa(userBook.ID), nil
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

	// Define the response structure
	var result struct {
		UserBooks []struct {
			ID        int64   `json:"id"`
			BookID    int64   `json:"book_id"`
			EditionID *int64  `json:"edition_id"`
			StatusID  int     `json:"status_id"`
		} `json:"user_books"`
	}

	vars := map[string]interface{}{
		"id": graphql.Int(id),
	}

	// Execute the query using the defined query string
	err := c.executeGraphQLQuery(ctx, query, vars, &result)
	if err != nil {
		log.Error().Err(err).Msg("Failed to execute GraphQL query")
		return nil, fmt.Errorf("failed to get user book: %w", err)
	}

	if len(result.UserBooks) == 0 {
		log.Warn().Msg("User book not found")
		return nil, ErrUserBookNotFound
	}

	userBook := result.UserBooks[0]

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
