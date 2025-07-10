package hardcover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_SearchBookByISBN10(t *testing.T) {
	tests := []struct {
		name     string
		isbn10   string
		expected *models.HardcoverBook
		wantErr  bool
	}{
		{
			name:   "valid isbn10",
			isbn10: "1234567890",
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
			name:     "invalid isbn10",
			isbn10:   "",
			expected: nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Initialize logger
			logger.Setup(logger.Config{Level: "debug", Format: "json"})

			// Create a test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Parse the request body
				reqBody, err := io.ReadAll(r.Body)
				require.NoError(t, err)

				var graphqlReq map[string]interface{}
				err = json.Unmarshal(reqBody, &graphqlReq)
				require.NoError(t, err)

				// Check if this is a search by ISBN10
				query, ok := graphqlReq["query"].(string)
				assert.True(t, ok)
				assert.Contains(t, query, "BookByISBN")

				// If the test case has an empty ISBN10, return an error
				if tt.isbn10 == "" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}

				// Prepare a mock response
				responseData := map[string]interface{}{
					"data": map[string]interface{}{
						"books": []map[string]interface{}{},
					},
				}

				// For valid ISBN10, add book data
				if tt.isbn10 == "1234567890" {
					audioSeconds := 3600
					edition := map[string]interface{}{
						"id":                "456",
						"asin":              nil,
						"isbn_13":           "9781234567890",
						"isbn_10":           "1234567890",
						"reading_format_id": 2,
						"audio_seconds":     &audioSeconds,
					}

					book := map[string]interface{}{
						"id":             "123",
						"title":          "Test Book",
						"book_status_id": 1,
						"editions":       []map[string]interface{}{edition},
					}

					responseData["data"].(map[string]interface{})["books"] = []map[string]interface{}{book}
				}

				// Return the response
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(responseData); err != nil {
					t.Fatalf("Failed to encode response: %v", err)
				}
			}))
			defer server.Close()

			// Create client using helper function
			client := CreateTestClient(server)

			// Call method
			book, err := client.SearchBookByISBN10(context.Background(), tt.isbn10)

			// Check error
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)

			// Check book
			assert.Equal(t, tt.expected, book)
		})
	}
}
