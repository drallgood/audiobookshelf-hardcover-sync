package hardcover

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/cache"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
)

// CreateTestClient creates a properly initialized client for testing with all required fields
func CreateTestClient(server *httptest.Server) *Client {
	// Set up a new logger with ERROR level to reduce test noise
	logger.Setup(logger.Config{
		Level:  "error", // Use error level to suppress debug/info/warning logs during tests
		Format: "json",
	})
	log := logger.Get()

	// Create a client with all required fields properly initialized
	return &Client{
		baseURL:   server.URL,
		authToken: "test-token",
		httpClient: &http.Client{
			Transport: &headerAddingTransport{
				token:   "test-token",
				baseURL: server.URL,
				rt:      http.DefaultTransport,
			},
		},
		logger:          log,
		rateLimiter:     util.NewRateLimiter(10*time.Millisecond, 5, 10, log),
		maxRetries:      0, // Disable retries in tests to reduce noise
		retryDelay:      time.Millisecond,
		userBookIDCache: cache.NewMemoryCache[int, int](log),
		userCache:       cache.NewMemoryCache[string, any](log),
	}
}



// CreateTestClientWithHandler creates a test client with the provided handler
func CreateTestClientWithHandler(handler http.HandlerFunc) (*Client, *httptest.Server) {
	server := httptest.NewServer(handler)
	return CreateTestClient(server), server
}

// HandleGetCurrentUserIDRequest is a helper function for test HTTP handlers to properly respond to GetCurrentUserID queries.
// It returns a properly formatted response with the me field as an array containing the provided user ID.
func HandleGetCurrentUserIDRequest(w http.ResponseWriter, userID int) {
	response := map[string]interface{}{
		"data": map[string]interface{}{
			"me": []map[string]interface{}{
				{"id": userID},
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		// In a production app we would handle this error properly
		// In tests, we'll just log it and continue as it's unlikely to happen during testing
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
	}
}
// HandleGetCurrentUserIDQuery checks if the request is a GetCurrentUserID query and handles it if it is.
// Returns true if the request was handled, false otherwise.
// This helps ensure all test handlers properly respond to user ID requests.
func HandleGetCurrentUserIDQuery(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return true
	}
	// We need to restore the request body for other handlers
	r.Body = io.NopCloser(strings.NewReader(string(body)))

	// Parse the GraphQL request
	var reqBody struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables"`
	}
	if err := json.Unmarshal(body, &reqBody); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return true
	}

	// Check if this is a GetCurrentUserID query
	if strings.Contains(reqBody.Query, "GetCurrentUserID") || strings.Contains(reqBody.Query, "me {\n\t\t\t\tid") {
		t.Log("Handling GetCurrentUserID query")
		response := map[string]interface{}{
			"data": map[string]interface{}{
				"me": []map[string]interface{}{
					{"id": 1001}, // Mock user ID
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("Failed to encode GetCurrentUserID response: %v", err)
		}
		return true
	}

	return false
}
