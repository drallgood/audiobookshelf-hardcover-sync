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

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/util"
	"github.com/stretchr/testify/assert"
)

func TestClient_UpdateUserBookStatus(t *testing.T) {
	// Set up the logger
	logger.Setup(logger.Config{
		Level:  "debug",
		Format: "json",
	})
	log := logger.Get()

	tests := []struct {
		name           string
		userBookID     int64
		status         string
		mockResponse   interface{}
		mockStatusCode int
		expectError    bool
	}{
		{
			name:       "successful update to FINISHED",
			userBookID: 123,
			status:     "FINISHED",
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"update_user_book": map[string]interface{}{
						"id":             123,
						"book_status_id": 3, // FINISHED status
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expectError:    false,
		},
		{
			name:       "successful update to READING",
			userBookID: 456,
			status:     "READING",
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"update_user_book": map[string]interface{}{
						"id":             456,
						"book_status_id": 2, // READING status
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expectError:    false,
		},
		{
			name:       "user book not found",
			userBookID: 789,
			status:     "FINISHED",
			mockResponse: map[string]interface{}{
				"data": map[string]interface{}{
					"update_user_book": nil,
				},
			},
			mockStatusCode: http.StatusOK,
			expectError:    true,
		},
		{
			name:       "graphql error",
			userBookID: 999,
			status:     "FINISHED",
			mockResponse: map[string]interface{}{
				"data": nil,
				"errors": []map[string]interface{}{
					{
						"message": "GraphQL error: user book not found",
					},
				},
			},
			mockStatusCode: http.StatusOK,
			expectError:    true,
		},
		{
			name:           "http error",
			userBookID:     111,
			status:         "FINISHED",
			mockResponse:   "Internal Server Error",
			mockStatusCode: http.StatusInternalServerError,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test server that will mock the Hardcover API
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Parse the request body to check for GetCurrentUserID queries
				body, readErr := io.ReadAll(r.Body)
				if readErr != nil {
					http.Error(w, readErr.Error(), http.StatusInternalServerError)
					return
				}
				// We need to restore the request body for other handlers
				r.Body = io.NopCloser(strings.NewReader(string(body)))

				// Parse the GraphQL request
				var reqBody struct {
					Query     string                 `json:"query"`
					Variables map[string]interface{} `json:"variables"`
				}
				if unmarshalErr := json.Unmarshal(body, &reqBody); unmarshalErr != nil {
					http.Error(w, unmarshalErr.Error(), http.StatusBadRequest)
					return
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

			// Create a proper client that points to our test server
			client := &Client{
				baseURL:     server.URL,
				authToken:   "test-token",
				httpClient:  &http.Client{},
				logger:      log,
				maxRetries:  3,
				retryDelay:  time.Millisecond * 100,
				rateLimiter: util.NewRateLimiter(10*time.Millisecond, 10, log), // Initialize rate limiter with reasonable test values
			}

			// Prepare the expected input that the server should receive
			input := UpdateUserBookStatusInput{
				ID:       tt.userBookID,
				StatusID: 1, // Using a dummy status ID for testing
				Status:   tt.status,
			}

			// Call the method being tested
			statusErr := client.UpdateUserBookStatus(context.Background(), input)

			// Check for expected errors
			if tt.expectError {
				assert.Error(t, statusErr)
			} else {
				assert.NoError(t, statusErr)
			}
		})
	}
}

func TestClient_UpdateUserBookStatusCompletionDate(t *testing.T) {
	for _, tc := range []struct {
		name, status, date string
		dryRun             bool
	}{
		{name: "dated finish", status: "FINISHED", date: "2025-06-01"},
		{name: "reading leaves completion date unchanged", status: "READING"},
		{name: "dry run", status: "FINISHED", date: "2025-06-01", dryRun: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var request struct {
					Variables struct {
						ID     int                    `json:"id"`
						Object map[string]interface{} `json:"object"`
					} `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				assert.Equal(t, 789, request.Variables.ID)
				if tc.date == "" {
					assert.Equal(t, float64(2), request.Variables.Object["status_id"])
					assert.NotContains(t, request.Variables.Object, "last_read_date")
				} else {
					assert.Equal(t, float64(3), request.Variables.Object["status_id"])
					assert.Equal(t, tc.date, request.Variables.Object["last_read_date"])
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"update_user_book":{"id":789,"error":null}}}`))
			}))
			defer server.Close()
			cfg := DefaultClientConfig()
			cfg.BaseURL, cfg.RateLimit, cfg.MaxRetries = server.URL, time.Nanosecond, 0
			client := NewClientWithConfig(cfg, "test-token", logger.Get())
			client.SetDryRun(tc.dryRun)
			assert.NoError(t, client.UpdateUserBookStatus(context.Background(), UpdateUserBookStatusInput{ID: 789, Status: tc.status, LastReadDate: tc.date}))
			if tc.dryRun {
				assert.Zero(t, requests)
			} else {
				assert.Equal(t, 1, requests)
			}
		})
	}
}
