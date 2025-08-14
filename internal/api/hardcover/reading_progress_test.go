package hardcover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/cache"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_InsertUserBookRead(t *testing.T) {
	// Set up the logger
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})
	log := logger.Get()

	tests := []struct {
		name        string
		input       InsertUserBookReadInput
		mockResponse interface{}
		expectError bool
		expectedID  int
	}{
		{
			name: "successful insert",
			input: InsertUserBookReadInput{
				UserBookID: 123,
				DatesRead: DatesReadInput{
					ProgressSeconds: func() *int { i := int(0.75 * 3600); return &i }(),
					StartedAt:       func() *string { s := "2023-01-01T00:00:00Z"; return &s }(),
					FinishedAt:      func() *string { s := "2023-01-02T00:00:00Z"; return &s }(),
				},
			},
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"insert_user_book_read": map[string]interface{}{
						"id": 456,
						"error": nil,
					},
				},
			},
			expectError: false,
			expectedID:  456,
		},
		{
			name: "insert with minimal data",
			input: InsertUserBookReadInput{
				UserBookID: 123,
				DatesRead: DatesReadInput{
					ProgressSeconds: func() *int { i := int(1.0 * 3600); return &i }(),
				},
			},
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"insert_user_book_read": map[string]interface{}{
						"id": 789,
						"error": nil,
					},
				},
			},
			expectError: false,
			expectedID:  789,
		},
		{
			name: "graphql error",
			input: InsertUserBookReadInput{
				UserBookID: 123,
				DatesRead: DatesReadInput{
					ProgressSeconds: func() *int { i := int(0.5 * 3600); return &i }(),
				},
			},
			mockResponse: map[string]interface{}{
				"errors": []map[string]interface{}{
					{"message": "User book not found"},
				},
			},
			expectError: true,
		},
		{
			name: "null response",
			input: InsertUserBookReadInput{
				UserBookID: 123,
				DatesRead: DatesReadInput{
					ProgressSeconds: func() *int { i := int(0.5 * 3600); return &i }(),
				},
			},
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"insert_user_book_read": nil,
				},
			},
			expectError: true,
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
					if _, err := w.Write([]byte(`{"error": "Failed to read request body"}`)); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}  
					return
				}
				
				// Parse GraphQL request
				var req map[string]interface{}
				if err := json.Unmarshal(body, &req); err != nil {
					t.Logf("Failed to parse request JSON: %v\nBody: %s", err, string(body))
					w.WriteHeader(http.StatusBadRequest)
					if _, err := w.Write([]byte(`{"error": "Invalid JSON"}`)); err != nil {
					t.Fatalf("Failed to write response: %v", err)
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
						t.Fatalf("Failed to marshal response: %v", err)
					}
					if _, err := w.Write(responseJSON); err != nil {
						t.Fatalf("Failed to write response: %v", err)
					}
					return
				} else if strings.Contains(query, "InsertUserBookRead") || strings.Contains(query, "insert_user_book_read") {
					// Handle InsertUserBookRead mutation
					w.WriteHeader(http.StatusOK)
					respBytes, err := json.Marshal(tt.mockResponse)
					if err != nil {
						t.Fatalf("Failed to marshal mock response: %v", err)
					}
					if _, err := w.Write(respBytes); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}
					return
				}
				
				// Unknown GraphQL query
				t.Logf("Unhandled GraphQL query: %s", query)
				w.WriteHeader(http.StatusBadRequest)
				if _, err := w.Write([]byte(`{"error": "Unhandled GraphQL query"}`)); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}  
			}))
			defer server.Close()

			// Create client with all necessary fields
			client := &Client{
				baseURL:          server.URL,
				authToken:        "test-token",
				httpClient:       server.Client(),
				logger:           log,
				rateLimiter:      util.NewRateLimiter(10*time.Millisecond, 1, 1, log),
				maxRetries:       3,
				retryDelay:       10*time.Millisecond,
				userBookIDCache:  cache.NewMemoryCache[int, int](log),
				userCache:        cache.NewMemoryCache[string, any](log),
			}

			// Call method
			readID, err := client.InsertUserBookRead(context.Background(), tt.input)

			// Check results
			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedID, readID)
			}
		})
	}
}

func TestClient_UpdateUserBookRead(t *testing.T) {
	// Set up the logger
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})
	log := logger.Get()

	tests := []struct {
		name        string
		input       UpdateUserBookReadInput
		mockResponse interface{}
		expectError bool
		expected    bool
	}{
		{
			name: "successful update",
			input: UpdateUserBookReadInput{
				ID: 123,
				Object: map[string]interface{}{
					"progress_seconds": int(0.5 * 3600),
					"started_at":      "2023-01-01T00:00:00Z",
					"finished_at":     "2023-01-02T00:00:00Z",
				},
			},
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"update_user_book_read": map[string]interface{}{
						"id": 123,
					},
				},
			},
			expectError: false,
			expected:    true,
		},
		{
			name: "minimal update",
			input: UpdateUserBookReadInput{
				ID: 123,
				Object: map[string]interface{}{
					"progress_seconds": int(0.25 * 3600),
				},
			},
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"update_user_book_read": map[string]interface{}{
						"id": 123,
					},
				},
			},
			expectError: false,
			expected:    true,
		},
		{
			name: "null response",
			input: UpdateUserBookReadInput{
				ID: 123,
				Object: map[string]interface{}{
					"progress_seconds": int(0.5 * 3600),
				},
			},
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"update_user_book_read": map[string]interface{}{
						"id":           123,
						"error":        nil,
						"user_book_read": nil,
					},
				},
			},
			expectError: false,
			expected:    true,
		},
		{
			name: "graphql error",
			input: UpdateUserBookReadInput{
				ID: 123,
				Object: map[string]interface{}{
					"progress_seconds": int(0.5 * 3600),
				},
			},
			mockResponse: map[string]interface{}{
				"errors": []map[string]interface{}{
					{"message": "Read record not found"},
				},
			},
			expectError: true,
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
					if _, err := w.Write([]byte(`{"error": "Failed to read request body"}`)); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}  
					return
				}
				
				// Parse GraphQL request
				var req map[string]interface{}
				if err := json.Unmarshal(body, &req); err != nil {
					t.Logf("Failed to parse request JSON: %v\nBody: %s", err, string(body))
					w.WriteHeader(http.StatusBadRequest)
					if _, err := w.Write([]byte(`{"error": "Invalid JSON"}`)); err != nil {
					t.Fatalf("Failed to write response: %v", err)
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
						t.Fatalf("Failed to marshal response: %v", err)
					}
					if _, err := w.Write(responseJSON); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}
					return
				} else if strings.Contains(query, "UpdateUserBookRead") || strings.Contains(query, "update_user_book_read") {
					// Handle UpdateUserBookRead mutation
					w.WriteHeader(http.StatusOK)
					respBytes, err := json.Marshal(tt.mockResponse)
					if err != nil {
						t.Fatalf("Failed to marshal mock response: %v", err)
					}
					if _, err := w.Write(respBytes); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}
					return
				}
				
				// Unknown GraphQL query
				t.Logf("Unhandled GraphQL query: %s", query)
				w.WriteHeader(http.StatusBadRequest)
				if _, err := w.Write([]byte(`{"error": "Unhandled GraphQL query"}`)); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}  
			}))
			defer server.Close()

			// Create client with all necessary fields
			client := &Client{
				baseURL:          server.URL,
				authToken:        "test-token",
				httpClient:       server.Client(),
				logger:           log,
				rateLimiter:      util.NewRateLimiter(10*time.Millisecond, 1, 1, log),
				maxRetries:       3,
				retryDelay:       10*time.Millisecond,
				userBookIDCache:  cache.NewMemoryCache[int, int](log),
				userCache:        cache.NewMemoryCache[string, any](log),
			}

			// Call method
			updated, err := client.UpdateUserBookRead(context.Background(), tt.input)

			// Check results
			if tt.expectError {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, updated)
			assert.Equal(t, tt.expected, updated)
		})
	}
}

func TestClient_GetUserBookReads(t *testing.T) {
	// Set up the logger
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})
	log := logger.Get()

	tests := []struct {
		name            string
		input           GetUserBookReadsInput
		mockResponse    interface{}
		expectError     bool
		expectedCount   int
		expectedReadIDs []int
	}{
		{
			name: "successful query with results",
			input: GetUserBookReadsInput{
				UserBookID: 123,
			},
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"user_book_reads": []map[string]interface{}{
						{
							"id":           456,
							"user_book_id": 123,
							"progress":     0.5,
							"started_at":   "2023-01-01T00:00:00Z",
							"finished_at":  nil,
						},
						{
							"id":           457,
							"user_book_id": 123,
							"progress":     1.0,
							"started_at":   "2023-01-01T00:00:00Z",
							"finished_at":  "2023-01-02T00:00:00Z",
						},
					},
				},
			},
			expectError:     false,
			expectedCount:   2,
			expectedReadIDs: []int{456, 457},
		},
		{
			name: "filter unfinished reads",
			input: GetUserBookReadsInput{
				UserBookID: 123,
				Status:     "unfinished",
			},
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"user_book_reads": []map[string]interface{}{
						{
							"id":           456,
							"user_book_id": 123,
							"progress":     0.5,
							"started_at":   "2023-01-01T00:00:00Z",
							"finished_at":  nil,
						},
					},
				},
			},
			expectError:     false,
			expectedCount:   1,
			expectedReadIDs: []int{456},
		},
		{
			name: "filter finished reads",
			input: GetUserBookReadsInput{
				UserBookID: 123,
				Status:     "finished",
			},
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"user_book_reads": []map[string]interface{}{
						{
							"id":           457,
							"user_book_id": 123,
							"progress":     1.0,
							"started_at":   "2023-01-01T00:00:00Z",
							"finished_at":  "2023-01-02T00:00:00Z",
						},
					},
				},
			},
			expectError:     false,
			expectedCount:   1,
			expectedReadIDs: []int{457},
		},
		{
			name: "empty result",
			input: GetUserBookReadsInput{
				UserBookID: 456,
			},
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"user_book_reads": []map[string]interface{}{},
				},
			},
			expectError:     false,
			expectedCount:   0,
			expectedReadIDs: []int{},
		},
		{
			name: "graphql error",
			input: GetUserBookReadsInput{
				UserBookID: 123,
			},
			mockResponse: map[string]interface{}{
				"errors": []map[string]interface{}{
					{"message": "Access denied"},
				},
			},
			expectError:     true,
			expectedCount:   0,
			expectedReadIDs: nil,
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
					if _, err := w.Write([]byte(`{"error": "Failed to read request body"}`)); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}  
					return
				}
				
				// Parse GraphQL request
				var req map[string]interface{}
				if err := json.Unmarshal(body, &req); err != nil {
					t.Logf("Failed to parse request JSON: %v\nBody: %s", err, string(body))
					w.WriteHeader(http.StatusBadRequest)
					if _, err := w.Write([]byte(`{"error": "Invalid JSON"}`)); err != nil {
					t.Fatalf("Failed to write response: %v", err)
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
						t.Fatalf("Failed to marshal response: %v", err)
					}
					if _, err := w.Write(responseJSON); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}
					return
				} else if strings.Contains(query, "GetUserBookReads") || strings.Contains(query, "user_book_read") {
					// Handle GetUserBookReads query
					w.WriteHeader(http.StatusOK)
					respBytes, err := json.Marshal(tt.mockResponse)
					if err != nil {
						t.Fatalf("Failed to marshal mock response: %v", err)
					}
					if _, err := w.Write(respBytes); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}
					return
				}
				
				// Unknown GraphQL query
				t.Logf("Unhandled GraphQL query: %s", query)
				w.WriteHeader(http.StatusBadRequest)
				if _, err := w.Write([]byte(`{"error": "Unhandled GraphQL query"}`)); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}  
			}))
			defer server.Close()

			// Create client with all necessary fields
			client := &Client{
				baseURL:          server.URL,
				authToken:        "test-token",
				httpClient:       server.Client(),
				logger:           log,
				rateLimiter:      util.NewRateLimiter(10*time.Millisecond, 1, 1, log),
				maxRetries:       3,
				retryDelay:       10*time.Millisecond,
				userBookIDCache:  cache.NewMemoryCache[int, int](log),
				userCache:        cache.NewMemoryCache[string, any](log),
			}

			// Call method
			reads, err := client.GetUserBookReads(context.Background(), tt.input)

			// Check results
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, reads)
				return
			}

			// Verify successful results
			require.NoError(t, err)
			require.NotNil(t, reads)
			assert.Equal(t, tt.expectedCount, len(reads))

			// Verify read IDs
			if tt.expectedReadIDs != nil {
				actualIDs := make([]int, len(reads))
				for i, read := range reads {
					actualIDs[i] = int(read.ID) // Convert int64 to int
				}
				assert.ElementsMatch(t, tt.expectedReadIDs, actualIDs)
			}
		})
	}
}

func TestClient_CheckExistingFinishedRead(t *testing.T) {
	tests := []struct {
		name               string
		input              CheckExistingFinishedReadInput
		responseJSON       string
		expectError        bool
		expectedHasRead    bool
		expectedFinishedAt *string
	}{
		{
			name: "has finished read",
			input: CheckExistingFinishedReadInput{
				UserBookID: 123,
			},
			responseJSON: `{"data":{"user_book_reads":[{"finished_at":"2023-01-02T00:00:00Z"}]}}`,
			expectError:        false,
			expectedHasRead:    true,
			expectedFinishedAt: func() *string { s := "2023-01-02T00:00:00Z"; return &s }(),
		},
		{
			name: "no finished read",
			input: CheckExistingFinishedReadInput{
				UserBookID: 456,
			},
			responseJSON:       `{"data":{"user_book_reads":[]}}`,
			expectError:        false,
			expectedHasRead:    false,
			expectedFinishedAt: nil,
		},
		{
			name: "graphql error",
			input: CheckExistingFinishedReadInput{
				UserBookID: 123,
			},
			responseJSON: `{"errors":[{"message":"GraphQL error"}]}`,
			expectError: true,
		},
		{
			name: "null response",
			input: CheckExistingFinishedReadInput{
				UserBookID: 789,
			},
			// The implementation actually handles this case gracefully
			responseJSON: `{"data":null}`,
			expectError:  false,
		},
		{
			name: "empty result response",
			input: CheckExistingFinishedReadInput{
				UserBookID: 999,
			},
			// This should trigger the "failed to unmarshal GraphQL data" error
			responseJSON: `{"data":{"user_book_reads":""}}`,
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server that always returns the predefined response JSON
			// This ensures no dynamic handling issues
			mockResponse := tt.responseJSON
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Always set the content type header
				w.Header().Set("Content-Type", "application/json")
				// Write the fixed JSON response string directly
				if _, err := w.Write([]byte(mockResponse)); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}
			}))
			defer server.Close()

			// Initialize logger
			logger.Setup(logger.Config{Level: "debug", Format: "json"})
			log := logger.Get()

			// Create client
			client := &Client{
				baseURL:     server.URL,
				authToken:   "test-token",
				httpClient:  server.Client(),
				logger:      log,
				rateLimiter: util.NewRateLimiter(10*time.Millisecond, 1, 10, log), // Fast rate limiting for tests
				maxRetries:  3,
				retryDelay:  time.Millisecond,
			}

			// Call method
			result, err := client.CheckExistingFinishedRead(context.Background(), tt.input)

			// Check results
			if tt.expectError {
				assert.Error(t, err)
				
				// For empty_result_response test case, we need to check if the error message contains "unmarshal"
				if tt.name == "empty result response" {
					assert.Contains(t, err.Error(), "unmarshal", "empty result test case should fail with unmarshal error")
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedHasRead, result.HasFinishedRead)
				if tt.expectedFinishedAt != nil {
					require.NotNil(t, result.LastFinishedAt)
					assert.Equal(t, *tt.expectedFinishedAt, *result.LastFinishedAt)
				} else {
					assert.Nil(t, result.LastFinishedAt)
				}
			}
		})
	}
}

func TestClient_GetGoogleUploadCredentials(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		editionID   int
		response    interface{} // Using a Go struct to generate the response
		expectError bool
		expectedURL string
	}{
		{
			name:      "successful credentials request",
			filename:  "test-audio.mp3",
			editionID: 123,
			response: map[string]interface{}{
				"data": map[string]interface{}{
					"google_upload_credentials": map[string]interface{}{
						"url": "https://storage.googleapis.com/upload",
						"fields": map[string]interface{}{
							"bucket":        "my-hardcover-bucket",
							"key":           "uploads/123/file.mp3",
							"Content-Type":  "audio/mpeg",
							"Authorization": "Bearer upload-token",
						},
					},
				},
			},
			expectError: false,
			expectedURL: "https://storage.googleapis.com/my-hardcover-bucket/uploads/123/file.mp3",
		},
		{
			name:      "graphql error",
			filename:  "test-audio.mp3",
			editionID: 123,
			response: map[string]interface{}{
				"errors": []map[string]interface{}{
					{"message": "Upload not allowed"},
				},
			},
			expectError: true,
		},
		{
			name:      "null response",
			filename:  "test-audio.mp3",
			editionID: 123,
			response: map[string]interface{}{
				"data": map[string]interface{}{
					"google_upload_credentials": nil,
				},
			},
			expectError: true,
		},
		{
			name:      "empty url response",
			filename:  "test-audio.mp3",
			editionID: 123,
			response: map[string]interface{}{
				"data": map[string]interface{}{
					"google_upload_credentials": map[string]interface{}{
						"url": "",
						"fields": map[string]interface{}{
							"bucket":        "my-hardcover-bucket",
							"key":           "uploads/123/file.mp3",
							"Content-Type":  "audio/mpeg",
						},
					},
				},
			},
			expectError: true,
		},
		{
			name:      "key without bucket",
			filename:  "test-audio.mp3",
			editionID: 123,
			response: map[string]interface{}{
				"data": map[string]interface{}{
					"google_upload_credentials": map[string]interface{}{
						"url": "https://storage.googleapis.com/upload",
						"fields": map[string]interface{}{
							"key":           "uploads/123/file.mp3",
							"Content-Type":  "audio/mpeg",
						},
					},
				},
			},
			expectError: false,
			expectedURL: "https://storage.googleapis.com/upload/uploads/123/file.mp3",
		},
		{
			name:      "malformed response",
			filename:  "test-audio.mp3",
			editionID: 123,
			response: "malformed", // Special marker for malformed response
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server with handler based on response type
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				
				if tt.response == "malformed" {
					// Special case for malformed response
					if _, err := w.Write([]byte(`{"data": {"google_`)); err != nil {
					t.Fatalf("Failed to write response: %v", err)
				}
					return
				}
				
				// Use the standard encoding/json package to properly encode the response
				if err := json.NewEncoder(w).Encode(tt.response); err != nil {
					t.Fatalf("Failed to encode response: %v", err)
				}
			}))
			defer server.Close()

			// Initialize logger
			logger.Setup(logger.Config{Level: "debug", Format: "json"})
			log := logger.Get()

			// Create client
			client := &Client{
				baseURL:     server.URL,
				authToken:   "test-token",
				httpClient:  server.Client(),
				logger:      log,
				rateLimiter: util.NewRateLimiter(10*time.Millisecond, 1, 10, log), // Fast rate limiting for tests
				maxRetries:  3,
				retryDelay:  time.Millisecond,
			}

			// Call method
			uploadInfo, err := client.GetGoogleUploadCredentials(context.Background(), tt.filename, tt.editionID)

			// Check results
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, uploadInfo)
			} else {
				require.NoError(t, err)
				require.NotNil(t, uploadInfo)
				assert.Equal(t, tt.expectedURL, uploadInfo.FileURL)
				assert.NotEmpty(t, uploadInfo.Fields)
			}
		})
	}
}
