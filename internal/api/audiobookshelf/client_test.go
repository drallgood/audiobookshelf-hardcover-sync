package audiobookshelf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	client := NewClient("http://example.com", "test-token")
	assert.NotNil(t, client)
	assert.Equal(t, "http://example.com", client.baseURL)
	assert.Equal(t, "test-token", client.token)
	assert.NotNil(t, client.client)
}

func TestGetLibraries(t *testing.T) {
	tests := []struct {
		name           string
		setupServer    func() *httptest.Server
		expectedResult []AudiobookshelfLibrary
		expectError    bool
	}{
		{
			name: "successful response",
			setupServer: func() *httptest.Server {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/api/libraries", r.URL.Path)
					assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

					response := struct {
						Libraries []AudiobookshelfLibrary `json:"libraries"`
					}{
						Libraries: []AudiobookshelfLibrary{
							{ID: "1", Name: "Library 1"},
							{ID: "2", Name: "Library 2"},
						},
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(response)
				})
				return httptest.NewServer(handler)
			},
			expectedResult: []AudiobookshelfLibrary{
				{ID: "1", Name: "Library 1"},
				{ID: "2", Name: "Library 2"},
			},
			expectError: false,
		},
		{
			name: "server error",
			setupServer: func() *httptest.Server {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				})
				return httptest.NewServer(handler)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			client := NewClient(server.URL, "test-token")
			libraries, err := client.GetLibraries(context.Background())

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedResult, libraries)
			}
		})
	}
}

func TestGetLibraryItems(t *testing.T) {
	tests := []struct {
		name           string
		setupServer    func() *httptest.Server
		libraryID      string
		expectError    bool
		expectedLength int
	}{
		{
			name: "successful response",
			setupServer: func() *httptest.Server {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/api/libraries/1/items", r.URL.Path)
					assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

					// Return a minimal valid response
					response := map[string]interface{}{
						"results": []map[string]interface{}{
							{
								"id": "book1",
								"title": "Test Book",
								"media": map[string]interface{}{
									"metadata": map[string]interface{}{
										"title": "Test Book",
										"authorName": "Test Author",
									},
								},
						},
					},
					"total": 1,
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(response)
			})
			return httptest.NewServer(handler)
		},
		libraryID:      "1",
		expectError:    false,
		expectedLength: 1,
	},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			client := NewClient(server.URL, "test-token")
			items, err := client.GetLibraryItems(context.Background(), tt.libraryID)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, items, tt.expectedLength)
			}
		})
	}
}

func TestGetUserProgress(t *testing.T) {
	tests := []struct {
		name          string
		setupServer   func() *httptest.Server
		expectError   bool
		expectedItems int
	}{
		{
			name: "successful response",
			setupServer: func() *httptest.Server {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/api/me", r.URL.Path)
					assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

					now := time.Now()
					progress := models.AudiobookshelfUserProgress{
						ID:       "user1",
						Username: "testuser",
						MediaProgress: []struct {
							ID            string  `json:"id"`
							LibraryItemID string  `json:"libraryItemId"`
							UserID        string  `json:"userId"`
							IsFinished    bool    `json:"isFinished"`
							Progress      float64 `json:"progress"`
							CurrentTime   float64 `json:"currentTime"`
							Duration      float64 `json:"duration"`
							StartedAt     int64   `json:"startedAt"`
							FinishedAt    int64   `json:"finishedAt"`
							LastUpdate    int64   `json:"lastUpdate"`
							TimeListening float64 `json:"timeListening"`
						}{
							{
								ID:            "progress1",
								LibraryItemID: "item1",
								UserID:        "user1",
								IsFinished:    false,
								Progress:      0.5,
								CurrentTime:   1800,
								Duration:      3600,
								StartedAt:     now.Add(-time.Hour).Unix(),
								LastUpdate:    now.Unix(),
								TimeListening: 1800,
							},
						},
						ListeningSessions: []struct {
							ID            string  `json:"id"`
							UserID        string  `json:"userId"`
							LibraryItemID string  `json:"libraryItemId"`
							MediaType     string  `json:"mediaType"`
							MediaMetadata struct {
								Title  string `json:"title"`
								Author string `json:"author"`
							} `json:"mediaMetadata"`
							Duration    float64 `json:"duration"`
							CurrentTime float64 `json:"currentTime"`
							Progress    float64 `json:"progress"`
							IsFinished  bool    `json:"isFinished"`
							StartedAt   int64   `json:"startedAt"`
							UpdatedAt   int64   `json:"updatedAt"`
						}{
							{
								ID:            "session1",
								UserID:        "user1",
								LibraryItemID: "item1",
								MediaType:     "book",
								MediaMetadata: struct {
									Title  string `json:"title"`
									Author string `json:"author"`
								}{
									Title:  "Test Book",
									Author: "Test Author",
								},
								Duration:    3600,
								CurrentTime: 1800,
								Progress:    0.5,
								IsFinished:  false,
								StartedAt:   now.Add(-time.Hour).Unix(),
								UpdatedAt:   now.Unix(),
							},
						},
					}

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(progress)
				})
				return httptest.NewServer(handler)
			},
			expectError:   false,
			expectedItems: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			client := NewClient(server.URL, "test-token")
			progress, err := client.GetUserProgress(context.Background())

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, progress)
				assert.Len(t, progress.MediaProgress, tt.expectedItems)
				assert.Len(t, progress.ListeningSessions, tt.expectedItems)
			}
		})
	}
}

func TestGetListeningSessions(t *testing.T) {
	tests := []struct {
		name          string
		setupServer   func() *httptest.Server
		since         time.Time
		expectError   bool
		expectedItems int
	}{
		{
			name: "successful response",
			setupServer: func() *httptest.Server {
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/api/me/listening-sessions", r.URL.Path)
					assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

					sessions := []models.AudiobookshelfBook{
						{
							ID: "book1",
							Media: struct {
								ID       string                       `json:"id"`
								Metadata models.AudiobookshelfMetadataStruct `json:"metadata"`
								CoverPath string                       `json:"coverPath"`
								Duration  float64                      `json:"duration"`
							}{
								Metadata: models.AudiobookshelfMetadataStruct{
									Title: "Test Book",
								},
							},
						},
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(sessions)
				})
				return httptest.NewServer(handler)
			},
			since:         time.Now().Add(-24 * time.Hour),
			expectError:   false,
			expectedItems: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.setupServer()
			defer server.Close()

			client := NewClient(server.URL, "test-token")
			sessions, err := client.GetListeningSessions(context.Background(), tt.since)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, sessions, tt.expectedItems)
			}
		})
	}
}
