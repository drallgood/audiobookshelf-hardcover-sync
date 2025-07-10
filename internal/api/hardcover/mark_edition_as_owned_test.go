package hardcover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/assert"
)

func TestClient_MarkEditionAsOwned(t *testing.T) {
	// Set up the logger
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})

	tests := []struct {
		name           string
		editionID      string
		mockResponse   interface{}
		mockStatusCode int
		expectError    bool
	}{
		{
			name:      "successful marking as owned",
			editionID: "123",
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"ownership": map[string]interface{}{
						"id": 456,
						"list_book": map[string]interface{}{
							"id": 789,
							"book_id": 123,
							"edition_id": 123,
						},
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expectError:    false,
		},
		{
			name:      "already owned",
			editionID: "456",
			mockResponse: map[string]interface{}{
				"errors": []map[string]interface{}{
					{
						"message": "Uniqueness violation. duplicate key value violates unique constraint",
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expectError:    true, // The client now returns an error, which the caller is expected to handle
		},
		{
			name:      "other graphql error",
			editionID: "789",
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"ownership": nil,
				},
				"errors": []map[string]interface{}{
					{
						"message": "Internal server error",
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expectError:    true, // Non-uniqueness errors should be reported
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
		},
		{
			name:           "http error",
			editionID:      "999",
			mockResponse:   "Internal Server Error",
			mockStatusCode: http.StatusInternalServerError,
			expectError:    true,
		},
		{
			name:         "null response",
			editionID:    "999",
			mockResponse: nil,
			mockStatusCode: http.StatusInternalServerError,
			expectError:  true,
		},
		{
			name:         "server error",
			editionID:    "999",
			mockResponse: "invalid JSON response",
			mockStatusCode: http.StatusOK,
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server that will mock the Hardcover API
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Set content type for JSON responses
				w.Header().Set("Content-Type", "application/json")
				
				// Parse the request body
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Logf("Failed to read request body: %v", err)
					w.WriteHeader(http.StatusInternalServerError)
					_, err := w.Write([]byte(`{"error": "Failed to read request body"}`))  
					if err != nil {
						t.Fatalf("Failed to write error response: %v", err)
					}
					return
				}
				
				// Parse GraphQL request
				var req map[string]interface{}
				if err := json.Unmarshal(body, &req); err != nil {
					t.Logf("Failed to parse request JSON: %v\nBody: %s", err, string(body))
					w.WriteHeader(http.StatusBadRequest)
					_, err := w.Write([]byte(`{"error": "Invalid JSON"}`))  
					if err != nil {
						t.Fatalf("Failed to write error response: %v", err)
					}
					return
				}
				
				// Extract GraphQL query and variables
				query, _ := req["query"].(string)
				
				// Handle different types of queries
				if strings.Contains(query, "GetCurrentUserID") {
					// Handle GetCurrentUserID query
					response := map[string]interface{}{
						"data": map[string]interface{}{
							"me": []map[string]interface{}{
								{"id": 42},
							},
						},
					}
					w.WriteHeader(http.StatusOK)
					responseJSON, err := json.Marshal(response)
					if err != nil {
						t.Fatalf("Failed to marshal JSON response: %v", err)
					}
					_, err = w.Write(responseJSON)
					if err != nil {
						t.Fatalf("Failed to write response: %v", err)
					}
					return
				} else if strings.Contains(query, "EditionOwned") || strings.Contains(query, "edition_owned") {
					// Handle MarkEditionAsOwned mutation based on test case
					if tt.mockStatusCode != http.StatusOK {
						// Handle HTTP error
						w.WriteHeader(tt.mockStatusCode)
						_, err := w.Write([]byte(tt.mockResponse.(string)))
						if err != nil {
							t.Fatalf("Failed to write error response: %v", err)
						}
						return
					}
					
					// Handle GraphQL response
					w.WriteHeader(http.StatusOK)
					respBytes, err := json.Marshal(tt.mockResponse)
					if err != nil {
						t.Fatalf("Failed to marshal mock response: %v", err)
					}
					_, err = w.Write(respBytes)
					if err != nil {
						t.Fatalf("Failed to write response: %v", err)
					}
					return
				}
				
				// Unknown GraphQL query
				t.Logf("Unhandled GraphQL query: %s", query)
				w.WriteHeader(http.StatusBadRequest)
				var writeErr error
				_, writeErr = w.Write([]byte(`{"error": "Unhandled GraphQL query"}`))  
				if writeErr != nil {
					t.Fatalf("Failed to write error response: %v", writeErr)
				}
			}))
			defer server.Close()

			// Create a client that points to our test server using helper
			client := CreateTestClient(server)

			// Convert editionID from string to int
			editionIDInt, _ := strconv.Atoi(tt.editionID)
			// Call the method being tested
			err := client.MarkEditionAsOwned(context.Background(), editionIDInt)

			// Check for expected errors
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
