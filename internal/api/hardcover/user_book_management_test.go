package hardcover

import (
"context"
"encoding/json"
"net/http"
"net/http/httptest"
"testing"
"time"

"github.com/drallgood/audiobookshelf-hardcover-sync/internal/cache"
"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
"github.com/stretchr/testify/assert"
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
				rateLimiter:     util.NewRateLimiter(10*time.Millisecond, 1, 10, log), // Fast rate limiting for tests
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
