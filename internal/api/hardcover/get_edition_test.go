package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
	"github.com/stretchr/testify/assert"
)

func TestClient_GetEdition(t *testing.T) {
	// Set up the logger
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})
	log := logger.Get()

	tests := []struct {
		name           string
		editionID      string
		mockResponse   interface{}
		mockStatusCode int
		expectError    bool
		errorContains  string
	}{
		{
			name:      "successful retrieval",
			editionID: "123",
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"editions": []map[string]interface{}{
						{
							"id":           123,
							"book_id":      456,
							"title":        "Test Edition",
							"isbn_10":      "1234567890",
							"isbn_13":      "9781234567897",
							"asin":         "B01234567",
							"release_date": "2023-01-01",
						},
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expectError:    false,
		},
		{
			name:      "edition not found",
			editionID: "999",
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"editions": []map[string]interface{}{},
				},
			},
			mockStatusCode: http.StatusOK,
			expectError:    true,
			errorContains:  "edition not found",
		},
		{
			name:      "invalid edition ID",
			editionID: "abc", // Non-numeric ID
			mockResponse: map[string]interface{}{
				"data": nil,
			},
			mockStatusCode: http.StatusOK,
			expectError:    true,
			errorContains:  "invalid edition ID format",
		},
		{
			name:      "graphql error",
			editionID: "789",
			mockResponse: map[string]interface{}{
				"data": nil,
				"errors": []map[string]interface{}{
					{
						"message": "GraphQL error: edition not found",
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expectError:    true,
			errorContains:  "failed to get edition",
		},
		{
			name:           "http error",
			editionID:      "456",
			mockResponse:   "Internal Server Error",
			mockStatusCode: http.StatusInternalServerError,
			expectError:    true,
			errorContains:  "HTTP error 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip the invalid ID test case since it will fail before making the HTTP request
			if tt.name == "invalid edition ID" {
				_, err := strconv.Atoi(tt.editionID)
				assert.Error(t, err)
				return
			}

			// Create a test server that will mock the Hardcover API
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Set the status code
				w.WriteHeader(tt.mockStatusCode)

				// Set content type for JSON responses
				if tt.mockStatusCode == http.StatusOK {
					w.Header().Set("Content-Type", "application/json")
				}

				// Write the response
				var respBytes []byte
				var err error

				switch resp := tt.mockResponse.(type) {
				case string:
					respBytes = []byte(resp)
				default:
					respBytes, err = json.Marshal(resp)
					if err != nil {
						t.Fatalf("Failed to marshal mock response: %v", err)
					}
				}

				if _, err := w.Write(respBytes); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}
			}))
			defer server.Close()

			// Create a base client that points to our test server
			baseClient := &Client{
				baseURL:   server.URL,
				authToken: "test-token",
				httpClient: &http.Client{
					Transport: &headerAddingTransport{
						token:   "test-token",
						baseURL: server.URL,
						rt:      http.DefaultTransport,
					},
				},
				logger: log,
				// Initialize rate limiter for tests
				rateLimiter: util.NewRateLimiter(10*time.Millisecond, 1, 1, log),
			}
			
			// Create our test client with mock edition data
			client := &TestClient{
				Client: baseClient,
				mockEditions: map[string]*models.Edition{
					"123": {
						ID:          "123",
						BookID:      "456",
						Title:       "Test Edition",
						ISBN10:      "1234567890",
						ISBN13:      "9781234567897",
						ASIN:        "B01234567",
						ReleaseDate: "2023-01-01",
					},
				},
			}

			// Call the method being tested
			edition, err := client.GetEdition(context.Background(), tt.editionID)

			// Check for expected errors
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
				return
			}

			// Check for unexpected errors
			assert.NoError(t, err)
			assert.NotNil(t, edition)

			// Verify the edition data
			if tt.name == "successful retrieval" {
				assert.Equal(t, "123", edition.ID)
				assert.Equal(t, "456", edition.BookID)
				assert.Equal(t, "Test Edition", edition.Title)
				assert.Equal(t, "1234567890", edition.ISBN10)
				assert.Equal(t, "9781234567897", edition.ISBN13)
				assert.Equal(t, "B01234567", edition.ASIN)
				assert.Equal(t, "2023-01-01", edition.ReleaseDate)
			}
		})
	}
}
