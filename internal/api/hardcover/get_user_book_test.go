package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
)

func TestClient_GetUserBook(t *testing.T) {
	// Set up the logger
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})

	tests := []struct {
		name           string
		userBookID     string
		mockResponse   interface{}
		mockStatusCode int
		expected       *models.HardcoverBook
		expectError    bool
	}{
		{
			name:       "successful retrieval - READING status",
			userBookID: "123",
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"user_books": []map[string]interface{}{
						{
							"id":        123,
							"book_id":   456,
							"status_id": 2, // READING status
							"book": map[string]interface{}{
								"id":    456,
								"title": "Test Book",
							},
							"edition_id": 789,
							"edition": map[string]interface{}{
								"id":      789,
								"asin":    "B00TEST123",
								"isbn_13": "9781234567890",
								"isbn_10": "1234567890",
							},
						},
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expected: &models.HardcoverBook{
				ID:           "456",
				Title:        "Test Book",
				UserBookID:   "123",
				BookStatusID: 2, // READING status
				EditionID:    "789",
				EditionASIN:  "B00TEST123",
				EditionISBN13: "9781234567890",
				EditionISBN10: "1234567890",
			},
			expectError: false,
		},
		{
			name:       "successful retrieval - FINISHED status",
			userBookID: "456",
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"user_books": []map[string]interface{}{
						{
							"id":        456,
							"book_id":   789,
							"status_id": 3, // FINISHED/READ status
							"book": map[string]interface{}{
								"id":    789,
								"title": "Finished Book",
							},
							"edition_id": 101,
							"edition": map[string]interface{}{
								"id":      101,
								"asin":    "B00DONE456",
								"isbn_13": "9780987654321",
								"isbn_10": "0987654321",
							},
						},
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expected: &models.HardcoverBook{
				ID:           "789",
				Title:        "Finished Book",
				UserBookID:   "456",
				BookStatusID: 3, // FINISHED/READ status
				EditionID:    "101",
				EditionASIN:  "B00DONE456",
				EditionISBN13: "9780987654321",
				EditionISBN10: "0987654321",
			},
			expectError: false,
		},
		{
			name:       "user book not found",
			userBookID: "999",
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"user_books": []map[string]interface{}{},
				},
			},
			mockStatusCode: http.StatusOK,
			expected:       nil,
			expectError:    true,
		},
		{
			name:       "graphql error",
			userBookID: "888",
			mockResponse: map[string]interface{}{
				"data": nil,
				"errors": []map[string]interface{}{
					{
						"message": "GraphQL error: user book not found",
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expected:       nil,
			expectError:    true,
		},
		{
			name:           "http error",
			userBookID:     "777",
			mockResponse:   "Internal Server Error",
			mockStatusCode: http.StatusInternalServerError,
			expected:       nil,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server that will mock the Hardcover API
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// First, check if this is a GetCurrentUserID query
				if HandleGetCurrentUserIDQuery(t, w, r) {
					return
				}
				
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

			// Create a client that points to our test server using the helper
			client := CreateTestClient(server)

			// Call the method being tested
			got, err := client.GetUserBook(context.Background(), tt.userBookID)

			// Check for expected errors
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, got)
				return
			}

			// Check for unexpected errors
			assert.NoError(t, err)

			// Check the result
			if tt.expected == nil {
				assert.Nil(t, got)
				return
			}

			assert.NotNil(t, got)
			assert.Equal(t, tt.expected.ID, got.ID)
			assert.Equal(t, tt.expected.Title, got.Title)
			assert.Equal(t, tt.expected.UserBookID, got.UserBookID)
			assert.Equal(t, tt.expected.BookStatusID, got.BookStatusID)
			assert.Equal(t, tt.expected.EditionID, got.EditionID)
			assert.Equal(t, tt.expected.EditionASIN, got.EditionASIN)
			assert.Equal(t, tt.expected.EditionISBN13, got.EditionISBN13)
			assert.Equal(t, tt.expected.EditionISBN10, got.EditionISBN10)
		})
	}
}
