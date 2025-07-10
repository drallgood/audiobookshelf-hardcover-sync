package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_CheckExistingUserBookRead(t *testing.T) {
	tests := []struct {
		name           string
		input          CheckExistingUserBookReadInput
		mockHandler    func(t *testing.T, w http.ResponseWriter, r *http.Request)
		expectError    bool
		expectResult   bool
		expectedReadID int
	}{
		{
			name: "existing read found",
			input: CheckExistingUserBookReadInput{
				UserBookID: 123,
				Date:       "2023-01-01",
			},
			mockHandler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				response := map[string]interface{}{
					"data": map[string]interface{}{
						"user_book_reads": []map[string]interface{}{
							{
								"id":               456,
								"edition_id":       789,
								"reading_format_id": 2,
								"progress_seconds": 300,
								"started_at":       "2023-01-01",
							},
						},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Fatalf("Failed to encode response: %v", err)
				}
			},
			expectError:    false,
			expectResult:   true,
			expectedReadID: 456,
		},
		{
			name: "no existing read found",
			input: CheckExistingUserBookReadInput{
				UserBookID: 123,
				Date:       "2023-01-02",
			},
			mockHandler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				response := map[string]interface{}{
					"data": map[string]interface{}{
						"user_book_reads": []map[string]interface{}{},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Fatalf("Failed to encode response: %v", err)
				}
			},
			expectError:    false,
			expectResult:   false,
			expectedReadID: 0,
		},
		{
			name: "graphql error",
			input: CheckExistingUserBookReadInput{
				UserBookID: 123,
				Date:       "2023-01-03",
			},
			mockHandler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				response := map[string]interface{}{
					"errors": []map[string]interface{}{
						{
							"message": "GraphQL error",
						},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Fatalf("Failed to encode response: %v", err)
				}
			},
			expectError:    true,
			expectResult:   false,
			expectedReadID: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server with properly initialized client
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tt.mockHandler(t, w, r)
			}))
			defer server.Close()
			
			// Create client using helper
			client := CreateTestClient(server)

			// Call method
			result, err := client.CheckExistingUserBookRead(context.Background(), tt.input)

			// Check results
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				if tt.expectResult {
					require.NotNil(t, result)
					assert.Equal(t, tt.expectedReadID, result.ID)
				} else {
					assert.Nil(t, result)
				}
			}
		})
	}
}