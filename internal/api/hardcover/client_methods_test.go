package hardcover

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
	"github.com/stretchr/testify/assert"
)

// newTestClient creates a new test client with a test HTTP server
func newTestClient(t *testing.T) (*Client, *httptest.Server) {
	t.Helper()

	// Create a test server that will mock the Hardcover API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the request body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Parse the GraphQL request
		var reqBody struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.Unmarshal(body, &reqBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		t.Logf("Received request with query: %s", reqBody.Query)
		t.Logf("Request variables: %+v", reqBody.Variables)

		// Prepare the response based on the query
		// Always initialize response with an empty books array by default
		response := map[string]interface{}{
			"data": map[string]interface{}{
				"books": []map[string]interface{}{}, // Always an array, never null
			},
		}

		if strings.Contains(reqBody.Query, "BookByISBN") {
			// Get the ISBN from the variables
			isbn, ok := reqBody.Variables["isbn"].(string)
			if !ok {
				http.Error(w, "missing or invalid ISBN in variables", http.StatusBadRequest)
				return
			}

			t.Logf("Processing ISBN search for: %s", isbn)

			// For the test case where we expect a book not found
			if isbn == "9780000000000" {
				t.Log("Returning empty books list for not found case")
				// response already has an empty books array from initialization
			} else if isbn == "9781234567890" || isbn == "1234567890" {
				t.Log("Returning test book data for ISBN:", isbn)
				// This is a book search by ISBN
				audioSeconds := 3600
				edition := map[string]interface{}{
					"id":                "456",
					"asin":              nil,
					"isbn_13":          "9781234567890",
					"isbn_10":          "1234567890",
					"reading_format_id": 2,
					"audio_seconds":    &audioSeconds,
				}

				book := map[string]interface{}{
					"id":            "123",
					"title":         "Test Book",
					"book_status_id": 1,
					"canonical_id":  nil,
					"editions":      []map[string]interface{}{edition},
				}

				// Update the response with the book data
				response["data"].(map[string]interface{})["books"] = []map[string]interface{}{book}
			}
		}

		// Write the response
		w.Header().Set("Content-Type", "application/json")
		jsonResponse, _ := json.Marshal(response)
		t.Logf("Sending response: %s", string(jsonResponse))
		if _, err := w.Write(jsonResponse); err != nil {
			t.Fatalf("Failed to write response: %v", err)
		}
	}))

	// Create a no-op rate limiter for testing
	rateLimiter := util.NewRateLimiter(1*time.Second, 10, 10, logger.Get())

	// Create a test logger that writes to a buffer
	var buf bytes.Buffer
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
		Output: &buf,
	})
	testLogger := logger.Get()
	t.Cleanup(func() {
		// Reset the logger after the test
		logger.ResetForTesting()
	})

	// Create a client that points to our test server
	client := &Client{
		baseURL:     server.URL, // Use the test server's URL
		authToken:   "test-token",
		httpClient:  server.Client(),
		logger:      testLogger,
		rateLimiter: rateLimiter,
	}

	return client, server
}

func TestClient_GetEditionByISBN13(t *testing.T) {
	tests := []struct {
		name        string
		isbn13      string
		expectError bool
		expected    *models.Edition
	}{
		{
			name:   "successful search",
			isbn13: "9781234567890",
			expected: &models.Edition{
				ID:     "456",
				BookID: "123",
				Title:  "Test Book",
				ISBN13: "9781234567890",
				ISBN10: "1234567890",
			},
			expectError: false,
		},
		{
			name:        "empty isbn",
			isbn13:      "",
			expected:    nil,
			expectError: true,
		},
		{
			name:        "book not found",
			isbn13:      "9780000000000", // This will trigger the not found case in our test server
			expected:    nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new test client with test server
			client, server := newTestClient(t)
			defer server.Close()

			// Call the method being tested
			got, err := client.GetEditionByISBN13(context.Background(), tt.isbn13)

			// Check for expected errors
			if tt.expectError {
				assert.Error(t, err)
				return
			}

			// Check for unexpected errors
			if !assert.NoError(t, err) {
				return
			}

			// Check the result
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestClient_SearchBookByISBN13(t *testing.T) {
	tests := []struct {
		name     string
		isbn13   string
		expected *models.HardcoverBook
		wantErr  bool
	}{
		{
			name:   "successful search",
			isbn13: "9781234567890",
			expected: &models.HardcoverBook{
				ID:            "123",
				Title:         "Test Book",
				EditionID:     "456",
				BookStatusID:  1,
				EditionISBN13: "9781234567890",
				EditionISBN10: "1234567890",
			},
			wantErr: false,
		},
		{
			name:     "empty isbn",
			isbn13:   "",
			expected: nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a buffer to capture log output
			var logBuffer bytes.Buffer
			
			// Set up the logger to write to our buffer
			logger.Setup(logger.Config{
				Level:  "debug",
				Format: "text",
				Output: &logBuffer,
			})

			// Create a new test client with test server
			client, testServer := newTestClient(t)
			defer testServer.Close()

			// Configure the client with our test logger
			client.logger = logger.Get()

			t.Logf("Starting test case: %s", tt.name)
			t.Logf("Test server URL: %s", testServer.URL)

			// Call the method being tested
			ctx := context.Background()
			got, err := client.SearchBookByISBN13(ctx, tt.isbn13)
			
			// Log the results and any errors
			t.Logf("Test case: %s", tt.name)
			t.Logf("SearchBookByISBN13 result: %+v, error: %v", got, err)
			
			// Output the captured logs
			if logBuffer.Len() > 0 {
				t.Log("=== Client Logs ===")
				t.Log(logBuffer.String())
				t.Log("==================")
			}
			
			// If we expected an error, check that first
			if tt.wantErr {
				if err == nil {
					t.Error("Expected an error but got none")
				}
				return
			}
			
			// If we didn't expect an error but got one, fail the test
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			
			// If we expected a nil result but got a non-nil result, fail the test
			if tt.expected == nil && got != nil {
				t.Error("Expected nil result but got non-nil")
				return
			}
			
			// If we expected a non-nil result but got nil, fail the test
			if tt.expected != nil && got == nil {
				t.Error("Expected non-nil result but got nil")
				return
			}
			
			// If both are nil, we're done
			if tt.expected == nil && got == nil {
				return
			}

			// Assert the results
			if (err != nil) != tt.wantErr {
				t.Errorf("Client.SearchBookByISBN13() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Skip further checks if we expected an error
			if tt.wantErr {
				return
			}

			// Check individual fields instead of using DeepEqual
			if got == nil && tt.expected != nil {
				t.Error("Expected non-nil result, got nil")
				return
			}

			if got.ID != tt.expected.ID {
				t.Errorf("ID = %v, want %v", got.ID, tt.expected.ID)
			}

			if got.Title != tt.expected.Title {
				t.Errorf("Title = %v, want %v", got.Title, tt.expected.Title)
			}

			if got.EditionID != tt.expected.EditionID {
				t.Errorf("EditionID = %v, want %v", got.EditionID, tt.expected.EditionID)
			}

			if got.BookStatusID != tt.expected.BookStatusID {
				t.Errorf("BookStatusID = %v, want %v", got.BookStatusID, tt.expected.BookStatusID)
			}

			if got.EditionISBN13 != tt.expected.EditionISBN13 {
				t.Errorf("EditionISBN13 = %v, want %v", got.EditionISBN13, tt.expected.EditionISBN13)
			}

			if got.EditionISBN10 != tt.expected.EditionISBN10 {
				t.Errorf("EditionISBN10 = %v, want %v", got.EditionISBN10, tt.expected.EditionISBN10)
			}
		})
	}
}
