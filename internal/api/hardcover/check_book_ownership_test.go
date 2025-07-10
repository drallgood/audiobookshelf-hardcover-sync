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

func TestClient_CheckBookOwnership(t *testing.T) {
	// Set up the logger
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})

	tests := []struct {
		name           string
		bookID         string
		mockResponse   interface{}
		mockStatusCode int
		expected       bool
		expectError    bool
	}{
		{
			name:   "book is owned",
			bookID: "123",
			mockResponse: []map[string]interface{}{
				{
					"id":   1,
					"name": "Owned",
					"list_books": []map[string]interface{}{
						{
							"id":        1,
							"book_id":   123,
							"edition_id": nil,
						},
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expected:       true,
			expectError:    false,
		},
		{
			name:   "book is not owned",
			bookID: "456",
			mockResponse: []map[string]interface{}{
				{
					"id":   1,
					"name": "Owned",
					"list_books": []map[string]interface{}{}, // Empty list for 'not owned'
				},
			},
			mockStatusCode: http.StatusOK,
			expected:       false,
			expectError:    false,
		},
		{
			name:   "empty owned list",
			bookID: "123",
			mockResponse: []map[string]interface{}{
				{
					"id":        1,
					"name":      "Owned",
					"list_books": []map[string]interface{}{},
				},
			},
			mockStatusCode: http.StatusOK,
			expected:       false,
			expectError:    false,
		},
		{
			name:   "no owned list",
			bookID: "123",
			mockResponse: []map[string]interface{}{},
			mockStatusCode: http.StatusOK,
			expected:       false,
			expectError:    false,
		},
		{
			name:   "graphql error",
			bookID: "123",
			mockResponse: map[string]interface{}{
				"errors": []map[string]interface{}{
					{
						"message": "GraphQL error: list not found",
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expected:       false,
			expectError:    true,
		},
		{
			name:           "http error",
			bookID:         "123",
			mockResponse:   "Internal Server Error",
			mockStatusCode: http.StatusInternalServerError,
			expected:       false,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server that will mock the Hardcover API
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Set content type for JSON responses
				w.Header().Set("Content-Type", "application/json")
				
				// Check if this is a GetCurrentUserID query and handle it if it is
				if HandleGetCurrentUserIDQuery(t, w, r) {
					return
				}
				
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
				vars, _ := req["variables"].(map[string]interface{})
				
				t.Logf("Received GraphQL query: %s with variables: %+v", query, vars)
				
				if strings.Contains(query, "CheckBookOwnership") {
					// Handle CheckBookOwnership query
					// Log bookId from variables for debugging
					if bookIDVar, ok := vars["bookId"]; ok {
						t.Logf("Received bookId: %v", bookIDVar)
					} else {
						t.Logf("No bookId variable in request")
					}

					// Find test case based on name in tt
					switch tt.name {
					case "graphql error":
						// Handle GraphQL error - this should not be wrapped in a data field
						t.Logf("Returning GraphQL error response for test case: %s", tt.name)
						// Use the mock response directly without wrapping in data field
						responseJSON, err := json.Marshal(tt.mockResponse)
						if err != nil {
							t.Fatalf("Failed to marshal GraphQL error response: %v", err)
						}
						w.WriteHeader(http.StatusOK) // GraphQL errors use 200 OK status code
						t.Logf("Sending GraphQL error response: %s", string(responseJSON))
						_, err = w.Write(responseJSON)
						if err != nil {
							t.Fatalf("Failed to write GraphQL error response: %v", err)
						}
					
					case "http error":
						// Handle HTTP error - use raw text response without JSON wrapper
						t.Logf("Returning HTTP error response for test case: %s", tt.name)
						w.Header().Del("Content-Type") // Remove JSON content type
						w.Header().Set("Content-Type", "text/plain")
						w.WriteHeader(http.StatusInternalServerError)
						errorMsg := "Internal Server Error"
						t.Logf("Sending plain text error response: %s", errorMsg)
						_, err := w.Write([]byte(errorMsg))
						if err != nil {
							t.Fatalf("Failed to write error message: %v", err)
						}
					
					default:
						// Handle normal cases
						t.Logf("Sending response for test case: %s", tt.name)
						var responseJSON []byte
						var err error
						
						// The CheckBookOwnership function expects a direct slice, not nested under data.lists
						responseWrapper := map[string]interface{}{"data": tt.mockResponse}
						
						responseJSON, err = json.Marshal(responseWrapper)
						if err != nil {
							t.Fatalf("Failed to marshal mock response: %v", err)
						}
						
						w.WriteHeader(tt.mockStatusCode)
						t.Logf("Sending response for %s: %s", tt.name, string(responseJSON))
						_, err = w.Write(responseJSON)
						if err != nil {
							t.Fatalf("Failed to write response JSON: %v", err)
						}
					}
				} else {
					// Unknown GraphQL query
					t.Logf("Unhandled GraphQL query: %s", query)
					w.WriteHeader(http.StatusBadRequest)
					_, err := w.Write([]byte(`{"error": "Unhandled GraphQL query"}`)) 
					if err != nil {
						t.Fatalf("Failed to write error response: %v", err)
					} 
				}
			}))
			defer server.Close()

			// Create a client that points to our test server using the helper
			client := CreateTestClient(server)

			// Convert bookID from string to int
			bookIDInt, _ := strconv.Atoi(tt.bookID)
			
			// Special handling for specific test cases
			switch tt.name {
			case "graphql error", "http error":
				// For error cases, we expect an error and false ownership
				isOwned, err := client.CheckBookOwnership(context.Background(), bookIDInt)
				assert.Error(t, err, "Expected an error for test case: %s", tt.name)
				assert.False(t, isOwned, "Expected false ownership for error case: %s", tt.name)
				t.Logf("Received expected error for %s: %v", tt.name, err)
				return
			}
			
			// Call the method being tested for non-error cases
			isOwned, err := client.CheckBookOwnership(context.Background(), bookIDInt)

			// Check for unexpected errors
			assert.NoError(t, err, "Unexpected error for test case: %s", tt.name)

			// Check the result
			assert.Equal(t, tt.expected, isOwned, "Ownership check failed for test case: %s", tt.name)
		})
	}
}

// Using the CheckBookOwnershipResponse type from client.go
