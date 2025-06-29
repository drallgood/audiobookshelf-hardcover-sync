package hardcover

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/stretchr/testify/require"
)

func TestClient_SearchPeople(t *testing.T) {
	// Set up test cases
	tests := []struct {
		name         string
		handler      http.HandlerFunc
		personType   string
		expectedName string
		expectedErr  bool
		errMessage   string
	}{
		{
			name:       "successful author search",
			personType: "author",
			handler: func(w http.ResponseWriter, r *http.Request) {
				// Read the request body
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, fmt.Sprintf("failed to read request body: %v", err), http.StatusBadRequest)
					return
				}

				// Parse the request to get the query and variables
				var reqBody struct {
					Query     string `json:"query"`
					Variables struct {
						Name  string `json:"name"`
						Limit int    `json:"limit"`
					} `json:"variables"`
				}

				if err := json.Unmarshal(body, &reqBody); err != nil {
					http.Error(w, fmt.Sprintf("failed to parse request body: %v", err), http.StatusBadRequest)
					return
				}

				// Check if this is a search query
				if strings.Contains(reqBody.Query, "query SearchPeopleDirect") {
					// Return a test author
					author := map[string]interface{}{
						"id":           123,
						"name":         "Test Author",
						"books_count":  10,
						"canonical_id": nil,
					}

					response := map[string]interface{}{
						"data": map[string]interface{}{
							"authors": []map[string]interface{}{author},
						},
					}

					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(response); err != nil {
						http.Error(w, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
					}
					return
				}

				// If we get here, it's an unexpected query
				errMsg := fmt.Sprintf("unexpected query: %s", reqBody.Query)
				http.Error(w, errMsg, http.StatusBadRequest)
			},
			expectedName: "Test Author",
			expectedErr:  false,
		},
		{
			name:       "successful narrator search",
			personType: "narrator",
			handler: func(w http.ResponseWriter, r *http.Request) {
				// Read the request body
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, fmt.Sprintf("failed to read request body: %v", err), http.StatusBadRequest)
					return
				}

				// Parse the request to get the query and variables
				var reqBody struct {
					Query     string `json:"query"`
					Variables struct {
						Name  string `json:"name"`
						Limit int    `json:"limit"`
					} `json:"variables"`
				}

				if err := json.Unmarshal(body, &reqBody); err != nil {
					http.Error(w, fmt.Sprintf("failed to parse request body: %v", err), http.StatusBadRequest)
					return
				}

				// Check if this is a search query
				if strings.Contains(reqBody.Query, "query SearchPeopleDirect") {
					// Return a test narrator
					narrator := map[string]interface{}{
						"id":           456,
						"name":         "Test Narrator",
						"books_count":  5,
						"canonical_id": nil,
					}

					response := map[string]interface{}{
						"data": map[string]interface{}{
							"authors": []map[string]interface{}{narrator},
						},
					}

					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(response); err != nil {
						http.Error(w, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
					}
					return
				}

				// If we get here, it's an unexpected query
				errMsg := fmt.Sprintf("unexpected query: %s", reqBody.Query)
				http.Error(w, errMsg, http.StatusBadRequest)
			},
			expectedName: "Test Narrator",
			expectedErr:  false,
		},
		{
			name:       "search with GraphQL errors",
			personType: "author",
			handler: func(w http.ResponseWriter, r *http.Request) {
				response := map[string]interface{}{
					"errors": []map[string]interface{}{
						{"message": "Internal server error"},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				if err := json.NewEncoder(w).Encode(response); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			},
			expectedErr: true,
			errMessage:  "direct person search failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server with the provided handler
			ts := httptest.NewServer(tt.handler)
			defer ts.Close()

			// Initialize logger if not already done
			logger.Setup(logger.Config{Level: "debug", Format: "json"})
			log := logger.Get()

			// Create a client with the test server URL and required dependencies
			client := &Client{
				baseURL:     ts.URL,
				httpClient:  http.DefaultClient,
				logger:      log,
				rateLimiter: util.NewRateLimiter(time.Second, 1, 10, log), // 1 request per second, burst of 1, max 10 concurrent
				maxRetries:  3,
				retryDelay:  time.Second,
			}

			// Call the appropriate method based on person type
			var authors []models.Author
			var err error
			if tt.personType == "author" {
				authors, err = client.SearchAuthors(context.Background(), "test", 10)
			} else {
				authors, err = client.SearchNarrators(context.Background(), "test", 10)
			}

			// Check for expected errors
			if tt.expectedErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMessage)
				return
			}

			// Check for successful response
			require.NoError(t, err)
			require.NotEmpty(t, authors)
			assert.Equal(t, tt.expectedName, authors[0].Name)
		})
	}
}

func TestClient_SearchPeople_UnsupportedType(t *testing.T) {
	// This test is no longer needed as we're handling the person type at a higher level
	// and the SearchPeople function is not directly exposed anymore
	t.Skip("Test no longer applicable as person type is handled at a higher level")
}
