package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/cache"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetCurrentUserID tests the GetCurrentUserID function
func TestGetCurrentUserID(t *testing.T) {
	tests := []struct {
		name           string
		mockResponse   map[string]interface{}
		mockStatusCode int
		expectError    bool
		expectedID     int
	}{
		{
			name: "success",
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"me": []map[string]interface{}{
						{"id": 1001},
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expectError:    false,
			expectedID:     1001,
		},
		{
			name: "graphql error",
			mockResponse: map[string]interface{}{
				"errors": []map[string]interface{}{
					{"message": "Unauthorized"},
				},
				"data": nil,
			},
			mockStatusCode: http.StatusOK,
			expectError:    true,
			expectedID:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up a test server to mock the Hardcover API
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.mockStatusCode)

				respBytes, err := json.Marshal(tt.mockResponse)
				if err != nil {
					t.Fatalf("Failed to marshal mock response: %v", err)
				}
				if _, err := w.Write(respBytes); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}
			}))
			defer server.Close()

			// Set up logger with error level to reduce noise in tests
			logger.Setup(logger.Config{
				Level:  "error",
				Format: "json",
			})
			log := logger.Get()

			// Create client
			client := &Client{
				baseURL:         server.URL,
				authToken:       "test-token",
				httpClient:      server.Client(),
				logger:          log,
				rateLimiter:     util.NewRateLimiter(10*time.Millisecond, 10, log), // Fast rate limiting for tests
				userBookIDCache: cache.NewMemoryCache[int, int](log),
				userCache:       cache.NewMemoryCache[string, any](log),
			}

			// Call the GetCurrentUserID method
			id, err := client.GetCurrentUserID(context.Background())

			// Check if an error was expected
			if tt.expectError {
				assert.Error(t, err)
				return
			}

			// Otherwise check for success
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedID, id)
		})
	}
}

func TestGetCurrentUserIDCoalescesConcurrentFetches(t *testing.T) {
	var requests atomic.Int32
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(requestStarted)
		}
		<-releaseResponse
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"data":{"me":[{"id":1001}]}}`))
		require.NoError(t, err)
	}))
	defer server.Close()

	log := logger.Get()
	client := &Client{
		baseURL:         server.URL,
		authToken:       "test-token",
		httpClient:      server.Client(),
		logger:          log,
		rateLimiter:     util.NewRateLimiter(time.Millisecond, 2, log),
		userBookIDCache: cache.NewMemoryCache[int, int](log),
		userCache:       cache.NewMemoryCache[string, any](log),
	}

	type result struct {
		id  int
		err error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			id, err := client.GetCurrentUserID(context.Background())
			results <- result{id: id, err: err}
		}()
	}

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("current-user request did not start")
	}
	close(releaseResponse)

	for range 2 {
		got := <-results
		require.NoError(t, got.err)
		assert.Equal(t, 1001, got.id)
	}
	assert.Equal(t, int32(1), requests.Load())
}

type panicOnceTransport struct {
	next     http.RoundTripper
	panicked atomic.Bool
}

func (p *panicOnceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if p.panicked.CompareAndSwap(false, true) {
		panic("transport failure")
	}
	return p.next.RoundTrip(req)
}

func TestGetCurrentUserIDRecoversAfterFetchPanic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"data":{"me":[{"id":1001}]}}`))
		require.NoError(t, err)
	}))
	defer server.Close()

	log := logger.Get()
	httpClient := server.Client()
	httpClient.Transport = &panicOnceTransport{next: httpClient.Transport}
	client := &Client{
		baseURL:    server.URL,
		authToken:  "test-token",
		httpClient: httpClient,
		logger:     log,
		// A single permit proves a panic does not leak the concurrency permit.
		rateLimiter:     util.NewRateLimiter(time.Millisecond, 1, log),
		userBookIDCache: cache.NewMemoryCache[int, int](log),
		userCache:       cache.NewMemoryCache[string, any](log),
	}

	assert.Panics(t, func() {
		_, _ = client.GetCurrentUserID(context.Background())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	id, err := client.GetCurrentUserID(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1001, id)
}
