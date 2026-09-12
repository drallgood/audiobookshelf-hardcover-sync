package hardcover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraphQLQuery_BookByASIN tests the GraphQL query functionality with a mock API server
// This is a unit test that doesn't require a real token
func TestGraphQLQuery_BookByASIN(t *testing.T) {
	// Initialize the logger
	logger.Setup(logger.Config{
		Level:      "debug",
		TimeFormat: "2006-01-02T15:04:05Z07:00",
	})

	// Get the logger
	log := logger.Get()

	// Create a mock server that returns a predefined response for ASIN queries
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the request body once
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err, "Error reading request body")

		// Create a new reader with the body content for parsing
		r.Body = io.NopCloser(strings.NewReader(string(body)))

		// Check if it's a GetCurrentUserID query first
		if HandleGetCurrentUserIDQuery(t, w, r) {
			return
		}

		// Reuse the body content for our own parsing
		r.Body = io.NopCloser(strings.NewReader(string(body)))

		// Parse the request body to check if it's the ASIN query
		var reqBody struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}

		// Decode the request body
		err = json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err, "Error decoding request body")

		// Check if this is our ASIN query
		if _, ok := reqBody.Variables["asin"]; ok {
			// Create a simple JSON response that matches the expected structure
			responseJSON := `{
				"data": {
					"books": [
						{
							"id": 123,
							"title": "Test Audiobook Title",
							"book_status_id": 1,
							"canonical_id": 456,
							"editions": [
								{
									"id": 789,
									"asin": "B00I8OW9R2",
									"isbn_13": null,
									"isbn_10": null,
									"reading_format_id": 2,
									"audio_seconds": 12345
								}
							]
						}
					]
				}
			}`

			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(responseJSON))
			if err != nil {
				t.Fatalf("Failed to write response: %v", err)
			}
			return
		}

		// If we get here, it's an unknown query
		http.Error(w, "Unexpected query", http.StatusBadRequest)
	}))
	defer server.Close()

	// Create a client that uses our mock server
	client := CreateTestClient(server)
	client.logger = log

	// Define the query and variables
	query := `query BookByASIN($asin: String!) {
  books(
    where: { 
      editions: { 
        asin: { _eq: $asin }
        reading_format_id: { _eq: 2 }
      }
    }
    limit: 1
  ) {
    id
    title
    book_status_id
    canonical_id
    editions(
      where: { 
        asin: { _eq: $asin }
        reading_format_id: { _eq: 2 }
      }
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

	variables := map[string]interface{}{
		"asin": "B00I8OW9R2",
	}

	// Define the response structure
	type BookEdition struct {
		ID              int     `json:"id"`
		ASIN            *string `json:"asin"`
		ISBN13          *string `json:"isbn_13"`
		ISBN10          *string `json:"isbn_10"`
		ReadingFormatID int     `json:"reading_format_id"`
		AudioSeconds    *int    `json:"audio_seconds"`
	}

	type Book struct {
		ID           int           `json:"id"`
		Title        string        `json:"title"`
		BookStatusID int           `json:"book_status_id"`
		CanonicalID  int           `json:"canonical_id"`
		Editions     []BookEdition `json:"editions"`
	}

	// Response structure is defined inline below

	// Define a response structure that exactly matches what the client will use for unmarshaling
	// The client will unmarshal only the "data" field from the response,
	// so our struct must match the structure inside the data field
	var response struct {
		Books []Book `json:"books"`
	}

	// Execute the query
	err := client.GraphQLQuery(context.Background(), query, variables, &response)
	require.NoError(t, err, "GraphQL query should not return an error")

	t.Logf("Response received: %+v", response)

	// Assert the response
	require.NotEmpty(t, response.Books, "Expected at least one book in the response")
	book := response.Books[0]
	assert.NotEmpty(t, book.Title, "Book title should not be empty")
	assert.Equal(t, "Test Audiobook Title", book.Title, "Book title should match the mock response")
	assert.NotEmpty(t, book.Editions, "Book should have at least one edition")

	edition := book.Editions[0]
	assert.Equal(t, 2, edition.ReadingFormatID, "Reading format ID should be 2 (audiobook)")
	assert.Equal(t, "B00I8OW9R2", *edition.ASIN, "ASIN should match the query parameter")
	assert.Equal(t, 12345, *edition.AudioSeconds, "Audio seconds should match the mock response")
}

func TestGraphQLQuery_RetriesOn429ThenSucceeds(t *testing.T) {
	logger.Setup(logger.Config{Level: "debug", Format: "json"})
	log := logger.Get()

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"Throttled"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[{"id":1}]}}`))
	}))
	defer server.Close()

	client := CreateTestClient(server)
	client.logger = log
	client.maxRetries = 3
	client.retryDelay = 1 * time.Millisecond
	client.rateLimiter = util.NewRateLimiter(time.Nanosecond, 100, log)

	var response struct {
		Books []struct {
			ID int `json:"id"`
		} `json:"books"`
	}

	err := client.GraphQLQuery(context.Background(), `query RetryTest { books { id } }`, nil, &response)
	require.NoError(t, err)
	require.Len(t, response.Books, 1)
	assert.Equal(t, 3, int(atomic.LoadInt32(&attempts)))
	assert.Equal(t, 1, response.Books[0].ID)
}

func TestGraphQLQuery_FailsFastOn400(t *testing.T) {
	logger.Setup(logger.Config{Level: "debug", Format: "json"})
	log := logger.Get()

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := CreateTestClient(server)
	client.logger = log
	client.maxRetries = 3
	client.retryDelay = 1 * time.Millisecond
	client.rateLimiter = util.NewRateLimiter(time.Nanosecond, 100, log)

	var response struct {
		Books []struct {
			ID int `json:"id"`
		} `json:"books"`
	}

	err := client.GraphQLQuery(context.Background(), `query FailFastTest { books { id } }`, nil, &response)
	require.Error(t, err)
	assert.Equal(t, 1, int(atomic.LoadInt32(&attempts)))
	assert.Contains(t, err.Error(), "non-retryable HTTP error")
}

func TestGraphQLQuery_BoundsRetryAfterPause(t *testing.T) {
	logger.Setup(logger.Config{Level: "error", Format: "json"})
	log := logger.Get()

	previousMaxBackoff := util.DefaultMaxBackoff
	util.DefaultMaxBackoff = 25 * time.Millisecond
	t.Cleanup(func() { util.DefaultMaxBackoff = previousMaxBackoff })

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[{"id":1}]}}`))
	}))
	defer server.Close()

	client := CreateTestClient(server)
	client.logger = log
	client.maxRetries = 1
	client.retryDelay = time.Millisecond
	client.rateLimiter = util.NewRateLimiter(time.Nanosecond, 1, log)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var response struct {
		Books []struct {
			ID int `json:"id"`
		} `json:"books"`
	}

	err := client.GraphQLQuery(ctx, `query RetryAfterTest { books { id } }`, nil, &response)
	require.NoError(t, err)
	assert.Equal(t, int32(2), atomic.LoadInt32(&attempts))
	assert.Equal(t, 1, response.Books[0].ID)
}

func TestGraphQLQuery_DailyResetOverridesRetryAfter(t *testing.T) {
	logger.Setup(logger.Config{Level: "error", Format: "json"})
	log := logger.Get()

	previousMaxBackoff := util.DefaultMaxBackoff
	util.DefaultMaxBackoff = 25 * time.Millisecond
	t.Cleanup(func() { util.DefaultMaxBackoff = previousMaxBackoff })

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("RateLimit", `"Free";r=59;t=1, "daily";r=0;t=1`)
			w.Header().Set("RateLimit-Policy", `"Free";q=60;w=60, "daily";q=5000;w=86400`)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"books":[{"id":1}]}}`))
	}))
	defer server.Close()

	client := CreateTestClient(server)
	client.logger = log
	client.maxRetries = 1
	client.retryDelay = time.Millisecond
	client.rateLimiter = util.NewRateLimiter(time.Nanosecond, 1, log)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var response struct {
		Books []struct {
			ID int `json:"id"`
		} `json:"books"`
	}

	err := client.GraphQLQuery(ctx, `query DailyResetTest { books { id } }`, nil, &response)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))
}

func TestGraphQLQuery_MaxConcurrentLimitsActiveRequests(t *testing.T) {
	logger.Setup(logger.Config{Level: "error", Format: "json"})
	log := logger.Get()

	var active, maxActive int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt32(&active, 1)
		for {
			previous := atomic.LoadInt32(&maxActive)
			if current <= previous || atomic.CompareAndSwapInt32(&maxActive, previous, current) {
				break
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		atomic.AddInt32(&active, -1)
		_, _ = w.Write([]byte(`{"data":{"books":[{"id":1}]}}`))
	}))
	defer server.Close()

	client := CreateTestClient(server)
	client.logger = log
	client.rateLimiter = util.NewRateLimiter(time.Nanosecond, 1, log)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var response1, response2 struct {
		Books []struct {
			ID int `json:"id"`
		} `json:"books"`
	}
	errs := make(chan error, 2)
	go func() {
		errs <- client.GraphQLQuery(ctx, `query ConcurrentTest { books { id } }`, nil, &response1)
	}()
	go func() {
		errs <- client.GraphQLQuery(ctx, `query ConcurrentTest { books { id } }`, nil, &response2)
	}()

	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first request did not reach the server")
	}
	select {
	case <-entered:
		t.Fatal("second request reached the server while the first was active")
	case <-time.After(50 * time.Millisecond):
	}

	release <- struct{}{}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("second request did not reach the server after the first completed")
	}
	release <- struct{}{}

	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	assert.Equal(t, int32(1), atomic.LoadInt32(&maxActive))
}

func TestGraphQLQuery_UsesConfiguredTimeoutAndReleasesPermit(t *testing.T) {
	logger.Setup(logger.Config{Level: "error", Format: "json"})
	log := logger.Get()

	var requests int32
	firstStarted := make(chan struct{})
	firstFinished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&requests, 1) {
		case 1:
			close(firstStarted)
			// Keep the handler stalled longer than the configured client timeout.
			// Do not rely on the server observing the client-side connection close.
			time.Sleep(200 * time.Millisecond)
			close(firstFinished)
		case 2:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"books":[{"id":1}]}}`)
		default:
			t.Errorf("unexpected request count: %d", atomic.LoadInt32(&requests))
		}
	}))
	defer server.Close()

	client := NewClientWithConfig(&ClientConfig{
		BaseURL:       server.URL,
		Timeout:       25 * time.Millisecond,
		MaxRetries:    0,
		RetryDelay:    time.Millisecond,
		RateLimit:     time.Nanosecond,
		MaxConcurrent: 1,
	}, "test-token", log)

	var firstResponse struct {
		Books []struct {
			ID int `json:"id"`
		} `json:"books"`
	}
	firstErr := make(chan error, 1)
	startedAt := time.Now()
	go func() {
		firstErr <- client.GraphQLQuery(context.Background(), `query TimeoutTest { books { id } }`, nil, &firstResponse)
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("stalled request did not reach the server")
	}

	select {
	case err := <-firstErr:
		require.Error(t, err)
		assert.Less(t, time.Since(startedAt), 150*time.Millisecond)
	case <-time.After(time.Second):
		t.Fatal("configured HTTP timeout did not end the stalled request")
	}
	var secondResponse struct {
		Books []struct {
			ID int `json:"id"`
		} `json:"books"`
	}
	secondErr := make(chan error, 1)
	go func() {
		secondErr <- client.GraphQLQuery(context.Background(), `query ReusePermit { books { id } }`, nil, &secondResponse)
	}()
	select {
	case err := <-secondErr:
		require.NoError(t, err)
		assert.Equal(t, 1, secondResponse.Books[0].ID)
	case <-time.After(time.Second):
		t.Fatal("rate-limiter permit was not released after the timed-out request")
	}
	select {
	case <-firstFinished:
	case <-time.After(time.Second):
		t.Fatal("stalled test handler did not finish")
	}
}
